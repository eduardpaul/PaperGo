// Package searchcontent makes the text of files searchable through the
// API, in steps a workflow can recompose:
//
//   - POST /v1/items/{id}/search/chunks splits text read by
//     POST /v1/items/{id}/text, or text given directly, into chunks and
//     stages them in the search store under a generation. A generation is
//     identified by the operation that started it; a later chunk request
//     can add to it.
//   - GET /v1/search/generations/{id}/chunks reads a generation's chunks,
//     and PUT /v1/search/generations/{id}/enrichments sets model-written
//     context on them.
//   - POST /v1/search/generations/{id}/embeddings embeds them for semantic
//     search, and POST /v1/search/generations/{id}/publish makes the
//     generation the searchable content of its file.
//
// It also keeps entries current: POST /v1/workspaces/{id}/search/sync
// rewrites the search documents of resources, and
// GET /v1/workspaces/{id}/search/term-entries lists those holding terms.
package searchcontent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/search"
	"papergo/internal/storage"
)

// Operation kinds.
const (
	KindChunk   = "search.chunk"
	KindEmbed   = "search.embed"
	KindPublish = "search.publish"
)

// ErrNoSearch refuses content requests in a deployment without a search
// store.
var ErrNoSearch = errors.New("search is not available in this deployment")

// Indexer writes file content to the deployment's search store.
type Indexer struct {
	Storage storage.Store
	// Search is nil when search is not configured.
	Search search.Store
	// Embeddings is nil when semantic search is off.
	Embeddings ai.Embedder
}

// maxText bounds text given to a chunk request directly.
const maxText = 1 << 20

// Embedding bounds: chunk inputs per embedding call and per sweep, and how
// long a vector no chunk uses is kept.
const (
	embedBatch     = 64
	sweepLimit     = 2000
	vectorKeepTime = 7 * 24 * time.Hour
)

// Register makes s run the operations.
func (x *Indexer) Register(s *dms.Service) error {
	for kind, h := range map[string]dms.OperationHandler{
		KindChunk:   {Scope: "document.write", Validate: x.validateChunk, Execute: x.chunk},
		KindEmbed:   {Scope: "document.write", Validate: x.validateGeneration, Execute: x.embed},
		KindPublish: {Scope: "document.write", Validate: x.validateGeneration, Execute: x.publish},
	} {
		if err := s.RegisterOperationHandler(kind, h); err != nil {
			return err
		}
	}
	return nil
}

// ChunkRequest says what text to chunk and how.
type ChunkRequest struct {
	// TextID is the text_id of a read of the file's text.
	TextID string `json:"text_id,omitempty"`
	// Text is text given directly, such as a summary.
	Text string `json:"text,omitempty"`
	// BlobID is the file version; default the item's current file.
	BlobID string `json:"blob_id,omitempty"`
	// Chunker is window (default), pages, paragraphs, headings or whole.
	Chunker   string `json:"chunker,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
	Overlap   *int   `json:"overlap,omitempty"`
	MaxChunks int    `json:"max_chunks,omitempty"`
	// Kind is content (default), summary or extract.
	Kind string `json:"kind,omitempty"`
	// Generation adds the chunks to a generation the caller started for
	// the same file version; default a new generation.
	Generation string `json:"generation,omitempty"`
}

// ChunkResult is a finished chunk operation's result.
type ChunkResult struct {
	Generation string `json:"generation"`
	Chunks     int    `json:"chunks"`
	Truncated  bool   `json:"truncated"`
	ItemID     string `json:"item_id"`
	BlobID     string `json:"blob_id"`
}

// chunkJob is a chunk operation's payload. An empty Generation starts the
// operation's own.
type chunkJob struct {
	ItemID     string              `json:"item_id"`
	BlobID     string              `json:"blob_id"`
	Generation string              `json:"generation,omitempty"`
	Chunker    string              `json:"chunker"`
	Kind       string              `json:"kind"`
	Options    search.ChunkOptions `json:"options"`
	ObjectKey  string              `json:"object_key,omitempty"`
	Text       string              `json:"text,omitempty"`
	Language   string              `json:"language,omitempty"`
}

// StartChunk stages chunks of the item's file text as subject.
func (x *Indexer) StartChunk(ctx context.Context, s *dms.Service, subject, itemID string, in ChunkRequest) (*dms.OperationView, error) {
	if x.Search == nil {
		return nil, dms.Invalid(ErrNoSearch.Error())
	}
	j := chunkJob{ItemID: itemID, Chunker: in.Chunker, Kind: in.Kind, Text: in.Text,
		Options: search.ChunkOptions{MaxChars: in.MaxChars, Overlap: search.DefaultOverlap, MaxChunks: in.MaxChunks}}
	if j.Chunker == "" {
		j.Chunker = "window"
	}
	if !slices.Contains(search.ChunkerKeys(), j.Chunker) {
		return nil, dms.Invalid("unknown chunker " + j.Chunker)
	}
	if j.Kind == "" {
		j.Kind = search.KindContent
	}
	if !slices.Contains([]string{search.KindContent, search.KindSummary, search.KindExtract}, j.Kind) {
		return nil, dms.Invalid("kind must be content, summary or extract")
	}
	if j.Options.MaxChars == 0 {
		j.Options.MaxChars = search.DefaultMaxChars
	}
	if j.Options.MaxChars < 100 || j.Options.MaxChars > 20000 {
		return nil, dms.Invalid("max_chars must be from 100 to 20000")
	}
	if in.Overlap != nil {
		if *in.Overlap < 0 || *in.Overlap > 5000 {
			return nil, dms.Invalid("overlap must be from 0 to 5000")
		}
		j.Options.Overlap = *in.Overlap
		if j.Options.Overlap == 0 {
			j.Options.Overlap = -1
		}
	}
	if j.Options.MaxChunks == 0 {
		j.Options.MaxChunks = search.DefaultMaxChunks
	}
	if j.Options.MaxChunks < 1 || j.Options.MaxChunks > 2000 {
		return nil, dms.Invalid("max_chunks must be from 1 to 2000")
	}
	b, err := s.GetBlob(ctx, subject, itemID, in.BlobID)
	if err != nil {
		return nil, err
	}
	j.BlobID = b.ID
	switch {
	case in.TextID != "" && in.Text != "":
		return nil, dms.Invalid("give text_id or text, not both")
	case in.TextID != "":
		text, err := s.DAL.Blobs().Text(ctx, in.TextID)
		if err != nil || text.Sha256 != b.Sha256 {
			return nil, dms.Invalid("text_id is not a text of the file")
		}
		j.ObjectKey, j.Language = text.ObjectKey, text.Language
	case in.Text != "":
		if len(in.Text) > maxText {
			return nil, dms.Invalid(fmt.Sprintf("text is larger than %d bytes; read the file's text and pass text_id", maxText))
		}
	default:
		return nil, dms.Invalid("give text_id or text")
	}
	j.Generation = in.Generation
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, itemID, KindChunk, payload); err != nil || out.Replayed {
			return err
		}
		return t.LeaseOperationBlobs(ctx, out.ID, []string{b.ID})
	})
	return out, err
}

// validateChunk runs in the accepting transaction: a generation added to
// must be one the caller started for the same file version.
func (x *Indexer) validateChunk(ctx context.Context, t *dms.Service, subject, itemID string, payload json.RawMessage) error {
	var j chunkJob
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID {
		return dms.Invalid("invalid chunk payload")
	}
	if j.Generation == "" {
		return nil
	}
	g, err := generationOf(ctx, t, subject, j.Generation)
	if err != nil {
		return err
	}
	if g.ItemID != j.ItemID || g.BlobID != j.BlobID {
		return dms.Invalid("generation belongs to another file")
	}
	return nil
}

// chunk splits the text and stages the chunks under the generation,
// replacing those this operation staged before.
func (x *Indexer) chunk(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	if x.Search == nil {
		return ErrNoSearch
	}
	var j chunkJob
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	if j.Generation == "" {
		j.Generation = op.ID
	}
	pages := []search.Page{{Text: j.Text}}
	if j.ObjectKey != "" {
		f, err := x.Storage.Open(ctx, j.ObjectKey)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return err
		}
		var stored struct {
			Pages []doctext.Page `json:"pages"`
		}
		if err = json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("the stored text is unreadable: %w", err)
		}
		pages = make([]search.Page, len(stored.Pages))
		for i, p := range stored.Pages {
			pages[i] = search.Page{Number: p.Number, Text: p.Text}
		}
	}
	chunks, truncated, err := search.Split(j.Chunker, pages, j.Kind, j.Options)
	if err != nil {
		return err
	}
	for i := range chunks {
		chunks[i].Part, chunks[i].Language = op.ID, j.Language
	}
	if err = x.Search.StageContent(ctx, j.ItemID, j.BlobID, j.Generation, chunks); err != nil {
		return err
	}
	return finish(ctx, s, op, ChunkResult{Generation: j.Generation, Chunks: len(chunks), Truncated: truncated, ItemID: j.ItemID, BlobID: j.BlobID})
}

// generation is the file version a generation stages content of.
type generation struct {
	ID, ItemID, BlobID string
}

// generationOf returns the generation started by the chunk operation id,
// which subject must be able to read. A generation another generation's
// chunks were added to is that generation.
func generationOf(ctx context.Context, s *dms.Service, subject, id string) (generation, error) {
	view, err := s.Operation(ctx, subject, id)
	if err != nil {
		return generation{}, err
	}
	if view.Kind != KindChunk {
		return generation{}, dms.ErrNotFound
	}
	op, err := s.DAL.Operations().Find(ctx, id)
	if err != nil {
		return generation{}, err
	}
	var j chunkJob
	if err = json.Unmarshal(op.Payload, &j); err != nil {
		return generation{}, err
	}
	if j.Generation != "" {
		return generation{}, dms.Invalid("name the generation, " + j.Generation + ", not a chunk request that added to it")
	}
	if op.Status != operations.StatusSucceeded {
		return generation{}, dms.Invalid("the generation has not been staged")
	}
	return generation{ID: id, ItemID: j.ItemID, BlobID: j.BlobID}, nil
}

// generationJob is the payload of an operation on a generation.
type generationJob struct {
	Generation string `json:"generation"`
	ItemID     string `json:"item_id"`
	BlobID     string `json:"blob_id"`
}

// StartGeneration starts an embed or publish operation on a generation the
// caller started.
func (x *Indexer) StartGeneration(ctx context.Context, s *dms.Service, subject, kind, id string) (*dms.OperationView, error) {
	if x.Search == nil {
		return nil, dms.Invalid(ErrNoSearch.Error())
	}
	g, err := generationOf(ctx, s, subject, id)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(generationJob{Generation: g.ID, ItemID: g.ItemID, BlobID: g.BlobID})
	if err != nil {
		return nil, err
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, g.ItemID, kind, payload); err != nil || out.Replayed {
			return err
		}
		return t.LeaseOperationBlobs(ctx, out.ID, []string{g.BlobID})
	})
	return out, err
}

func (x *Indexer) validateGeneration(ctx context.Context, t *dms.Service, subject, itemID string, payload json.RawMessage) error {
	var j generationJob
	if err := json.Unmarshal(payload, &j); err != nil || j.ItemID != itemID {
		return dms.Invalid("invalid generation payload")
	}
	g, err := generationOf(ctx, t, subject, j.Generation)
	if err != nil {
		return err
	}
	if g.ItemID != j.ItemID || g.BlobID != j.BlobID {
		return dms.Invalid("invalid generation payload")
	}
	return nil
}

// EmbedResult is a finished embed operation's result. Reason says why
// vectors were left to the sweep, which embeds published chunks without
// them.
type EmbedResult struct {
	Generation string `json:"generation"`
	Model      string `json:"model,omitempty"`
	Embedded   int    `json:"embedded"`
	Reused     int    `json:"reused"`
	Reason     string `json:"reason,omitempty"`
}

// embed gives the generation's chunk inputs without a vector of the model
// their vectors. A provider failure is a result, not an error: the sweep
// embeds the chunks once they are published.
func (x *Indexer) embed(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j generationJob
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	res := EmbedResult{Generation: j.Generation}
	if x.Embeddings == nil {
		res.Reason = "No embedding model is configured."
		return finish(ctx, s, op, res)
	}
	model, err := x.Embeddings.Key(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res.Reason = "The embedding model is unavailable: " + err.Error()
		return finish(ctx, s, op, res)
	}
	res.Model = model
	todo, inputs, err := x.Search.GenerationToEmbed(ctx, model, j.Generation)
	if err != nil {
		return err
	}
	res.Reused = inputs - len(todo)
	res.Embedded, err = x.embedInputs(ctx, model, todo)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res.Reason = "The embedding model failed: " + err.Error()
	}
	return finish(ctx, s, op, res)
}

// embedInputs embeds chunk inputs in batches and stores each batch's
// vectors, so a failure keeps what was done; it reports how many were.
func (x *Indexer) embedInputs(ctx context.Context, model string, todo []search.ChunkToEmbed) (int, error) {
	done := 0
	for start := 0; start < len(todo); start += embedBatch {
		batch := todo[start:min(len(todo), start+embedBatch)]
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Input
		}
		vectors, err := x.Embeddings.Embed(ctx, texts)
		if err != nil {
			return done, err
		}
		pairs := make([]search.HashVector, len(batch))
		for i, c := range batch {
			pairs[i] = search.HashVector{InputHash: c.InputHash, Vector: vectors[i]}
		}
		if err = x.Search.SetVectors(ctx, model, pairs); err != nil {
			return done, err
		}
		done += len(batch)
	}
	return done, nil
}

// Sweep embeds published chunks without a vector of the current model, at
// most 2,000 per call, then deletes vectors no chunk has used for a week.
// A provider failure stops it until the next sweep.
func (x *Indexer) Sweep(ctx context.Context, _ time.Time) error {
	if x.Search == nil || x.Embeddings == nil {
		return nil
	}
	model, err := x.Embeddings.Key(ctx)
	if err != nil {
		return err
	}
	for embedded := 0; embedded < sweepLimit; {
		todo, err := x.Search.ChunksToEmbed(ctx, model, min(embedBatch, sweepLimit-embedded))
		if err != nil {
			return err
		}
		if len(todo) == 0 {
			break
		}
		n, err := x.embedInputs(ctx, model, todo)
		embedded += n
		if err != nil {
			return err
		}
	}
	_, err = x.Search.CollectVectors(ctx, model, time.Now(), vectorKeepTime)
	return err
}

// PublishResult is a finished publish operation's result. A generation is
// not published, with the reason, when its item is gone or no longer has
// the file it was made from, so a slow workflow never replaces a newer
// file's content.
type PublishResult struct {
	Generation string `json:"generation"`
	Published  bool   `json:"published"`
	ItemID     string `json:"item_id"`
	BlobID     string `json:"blob_id"`
	Reason     string `json:"reason,omitempty"`
}

func (x *Indexer) publish(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var j generationJob
	if err := json.Unmarshal(op.Payload, &j); err != nil {
		return err
	}
	res := PublishResult{Generation: j.Generation, ItemID: j.ItemID, BlobID: j.BlobID}
	files, err := s.ItemFiles(ctx, j.ItemID)
	if err != nil {
		return err
	}
	switch {
	case files == nil:
		res.Reason = "The item is gone."
	case !slices.Contains(files, j.BlobID):
		res.Reason = "A newer file replaced the one the content was made from."
	default:
		if err = x.Search.PublishContent(ctx, j.ItemID, j.BlobID, j.Generation); err != nil {
			return err
		}
		res.Published = true
	}
	return finish(ctx, s, op, res)
}

// finish ends op successfully with result, under its claim.
func finish(ctx context.Context, s *dms.Service, op *operations.Operation, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		op.Status, op.Result, op.Progress, op.CompletedAt = operations.StatusSucceeded, raw, 100, &completed
		return t.SaveOperation(ctx, op)
	})
}

// Chunk is a staged or published chunk of a generation.
type Chunk struct {
	Part       string `json:"part"`
	Ordinal    int    `json:"ordinal"`
	Page       int    `json:"page,omitempty"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Enrichment string `json:"enrichment,omitempty"`
	// Truncated says the text was cut to keep the page within its budget.
	Truncated bool `json:"truncated,omitempty"`
}

// ChunkPage is a page of a generation's chunks.
type ChunkPage struct {
	Chunks     []Chunk `json:"chunks"`
	NextOffset int     `json:"next_offset"`
	More       bool    `json:"more"`
}

// pageText bounds the text of one page of chunks.
const pageText = 200_000

// Chunks returns up to limit chunks of a generation from offset, in part
// and ordinal order.
func (x *Indexer) Chunks(ctx context.Context, s *dms.Service, subject, id string, offset, limit int) (ChunkPage, error) {
	if x.Search == nil {
		return ChunkPage{}, dms.Invalid(ErrNoSearch.Error())
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return ChunkPage{}, dms.Invalid("offset must not be negative and limit must be from 1 to 100")
	}
	g, err := generationOf(ctx, s, subject, id)
	if err != nil {
		return ChunkPage{}, err
	}
	if _, err = s.GetBlob(ctx, subject, g.ItemID, g.BlobID); err != nil {
		return ChunkPage{}, err
	}
	staged, err := x.Search.GenerationChunks(ctx, g.ID)
	if err != nil {
		return ChunkPage{}, err
	}
	out := ChunkPage{Chunks: []Chunk{}, NextOffset: offset}
	budget := pageText
	for out.NextOffset < len(staged) && len(out.Chunks) < limit && budget > 0 {
		c := staged[out.NextOffset]
		text, truncated := c.Text, false
		if r := []rune(text); len(r) > budget {
			text, truncated = string(r[:budget]), true
		}
		budget -= len([]rune(text))
		out.Chunks = append(out.Chunks, Chunk{Part: c.Part, Ordinal: c.Ordinal, Page: c.Page, Kind: c.Kind, Text: text, Enrichment: c.Enrichment, Truncated: truncated})
		out.NextOffset++
	}
	out.More = out.NextOffset < len(staged)
	return out, nil
}

// Enrichment is model-written context for one chunk.
type Enrichment struct {
	Part    string `json:"part"`
	Ordinal int    `json:"ordinal"`
	Text    string `json:"text"`
}

// Enrich sets the enrichment of chunks of a generation the caller started.
// Enrichment changes a chunk's embedding input, so embed after enriching.
func (x *Indexer) Enrich(ctx context.Context, s *dms.Service, subject, id string, in []Enrichment) error {
	if x.Search == nil {
		return dms.Invalid(ErrNoSearch.Error())
	}
	if len(in) == 0 || len(in) > 2000 {
		return dms.Invalid("give 1 to 2000 enrichments")
	}
	out := make([]search.Enrichment, len(in))
	for i, e := range in {
		if e.Part == "" || len([]rune(e.Text)) > 4000 {
			return dms.Invalid("each enrichment names its chunk's part and has at most 4000 characters")
		}
		out[i] = search.Enrichment{Part: e.Part, Ordinal: e.Ordinal, Text: e.Text}
	}
	g, err := generationOf(ctx, s, subject, id)
	if err != nil {
		return err
	}
	if _, err = s.GetBlob(ctx, subject, g.ItemID, g.BlobID); err != nil {
		return err
	}
	return x.Search.SetEnrichments(ctx, g.ID, out)
}
