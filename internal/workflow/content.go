package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/forms"
	"papergo/internal/search"
)

// File content in search is built by workflow steps people can recompose:
// search.chunk splits text into chunks and stages them under a generation,
// search.publish makes the generation the file's searchable content after
// fixed checks. The search.content built-in fills the search.content role
// with document.read_text, search.chunk, search.embed (embed.go) and
// search.publish.

// RoleSearchContent keeps the text of items' files searchable.
const RoleSearchContent = "search.content"

// ErrNoSearch fails search activities in a deployment without a search
// store.
var ErrNoSearch = errors.New("search is not available in this deployment")

// maxChunkSource bounds text given to search.chunk directly.
const maxChunkSource = 1 << 20

const portStale = "stale"

// contentOutputs are the outputs of the search.content role.
const contentOutputs = `{"type": "object", "required": ["blob_id"], "properties": {
	"blob_id": {"type": "string", "description": "The file version indexed."},
	"text_id": {"type": "string", "description": "The text read, for document.read_text's readers."},
	"pages": {"type": "integer"}, "language": {"type": "string"},
	"generation": {"type": "string", "description": "The content published."},
	"chunks": {"type": "integer"}}}`

func init() {
	register(&Activity{Key: "search.chunk", Kind: "action",
		Description: "Splits the text of the item's file into chunks and stages them in the search index under a generation, which search.publish makes searchable. The text is a text_id from document.read_text, or text given directly (such as a summary); staging into an existing generation adds to it.",
		InputSchema: inputs(itemIDProp + `,
			"text_id": {"type": "string", "title": "Text", "description": "The text_id of document.read_text."},
			"text": {"type": "string", "title": "Text given directly", "description": "Instead of text_id: text such as a summary, not on a page."},
			"blob_id": {"type": "string", "title": "File version", "description": "Default the item's current file."},
			"chunker": {"type": "string", "title": "Chunker", "enum": ["window", "pages", "paragraphs", "headings", "whole"], "default": "window", "description": "window: overlapping windows of each page; pages: a chunk per page; paragraphs: paragraphs merged up to the size; headings: sections, each chunk starting with its heading; whole: the whole text as one chunk."},
			"max_chars": {"type": "integer", "title": "Chunk size", "minimum": 100, "maximum": 20000, "default": 1200},
			"overlap": {"type": "integer", "title": "Overlap", "minimum": 0, "maximum": 5000, "default": 150, "description": "Characters consecutive windows share."},
			"max_chunks": {"type": "integer", "title": "Maximum chunks", "minimum": 1, "maximum": 2000, "default": 400},
			"kind": {"type": "string", "title": "Kind", "enum": ["content", "summary", "extract"], "default": "content"},
			"generation": {"type": "string", "title": "Generation", "description": "A generation of this run to add to; default a new one."}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"generation": {"type": "string"}, "chunks": {"type": "integer"}, "truncated": {"type": "boolean"},
			"item_id": {"type": "string"}, "blob_id": {"type": "string"}}}`),
		run: runSearchChunk, work: workSearchChunk, commit: commitSearchChunk})
	register(&Activity{Key: "search.publish", Kind: "action", Ports: []string{"done", portStale},
		Description: "Makes a generation of this run the searchable content of the item's file, replacing the content it had. Follows stale, changing nothing, when the item is gone, no longer has that file, or the generation is not this run's: a slow run never overwrites a newer file's content.",
		InputSchema: inputs(itemIDProp+`,
			"generation": {"type": "string", "title": "Generation", "description": "The generation of search.chunk."},
			"blob_id": {"type": "string", "title": "File version", "description": "Default the item's current file."}`, "generation"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {"published": {"type": "boolean"}, "generation": {"type": "string"}, "item_id": {"type": "string"}, "blob_id": {"type": "string"}, "reason": {"type": "string"}}}`),
		run:          runSearchPublish, work: workSearchPublish, commit: commitSearchPublish})

	RegisterRole(&Role{Key: RoleSearchContent, Name: "File content in search",
		Description:  "Reads, chunks and publishes the text of files so search finds it, with the page. Turned off for a collection, its files' text leaves search as their content is replaced.",
		OutputSchema: forms.MustParse(contentOutputs)})
	RegisterBuiltIn(&BuiltIn{Key: "search.content", Name: "Index file content", Role: RoleSearchContent, Default: true, System: true,
		Description: "When a PDF or an image is added, reads its text (OCR for scans and photos when configured), splits it into chunks, embeds them for semantic search when an embedding model is configured, and makes it searchable with its pages. A reindex with content runs it again for files without content.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"ocr": {"type": "string", "title": "OCR", "enum": ["auto", "always", "never"], "default": "auto"},
			"max_pages": {"type": "integer", "title": "Maximum pages", "minimum": 1, "maximum": 2000, "default": 500},
			"chunker": {"type": "string", "title": "Chunker", "enum": ["window", "pages", "paragraphs", "headings", "whole"], "default": "window"},
			"max_chars": {"type": "integer", "title": "Chunk size", "minimum": 100, "maximum": 20000, "default": 1200},
			"overlap": {"type": "integer", "title": "Overlap", "minimum": 0, "maximum": 5000, "default": 150},
			"max_chunks": {"type": "integer", "title": "Maximum chunks", "minimum": 1, "maximum": 2000, "default": 400},
			"embed": {"type": "string", "title": "Embedding", "enum": ["immediate", "later"], "default": "immediate", "description": "When an embedding model is configured: embed the chunks before publishing, or leave them to the sweep."}
		}}`),
		Definition: `{
			"triggers": [
				{"type": "file.added", "collection_id": "{param:collection}", "media_types": ["application/pdf", "image/*"], "sources": ["upload", "webdav", "restore", "workflow", "reindex"]},
				{"type": "manual", "collection_id": "{param:collection}"}
			],
			"concurrency": "coalesce",
			"output_schema": ` + contentOutputs + `,
			"outputs": {"blob_id": "{step:read.blob_id}", "text_id": "{step:read.text_id}", "pages": "{step:read.pages}", "language": "{step:read.language}",
				"generation": "{step:chunk.generation}", "chunks": "{step:chunk.chunks}"},
			"flow": {"start": "read", "nodes": {
				"read": {"activity": "document.read_text", "inputs": {"ocr": "{param:ocr}", "max_pages": "{param:max_pages}"}, "next": {"done": "chunk", "no_text": "end", "unsupported": "end"}},
				"chunk": {"activity": "search.chunk", "inputs": {"text_id": "{step:read.text_id}", "blob_id": "{step:read.blob_id}", "chunker": "{param:chunker}",
					"max_chars": "{param:max_chars}", "overlap": "{param:overlap}", "max_chunks": "{param:max_chunks}"}, "next": {"done": "embed"}},
				"embed": {"activity": "search.embed", "inputs": {"generation": "{step:chunk.generation}", "mode": "{param:embed}"}, "next": {"done": "publish", "deferred": "publish"}},
				"publish": {"activity": "search.publish", "inputs": {"generation": "{step:chunk.generation}", "blob_id": "{step:read.blob_id}"}, "next": {"done": "end", "stale": "end"}},
				"end": {"activity": "end"}
			}}
		}`,
	})
}

// chunkJob is search.chunk's work: where the text is and how to split it.
type chunkJob struct {
	ItemID     string              `json:"item_id"`
	BlobID     string              `json:"blob_id"`
	Generation string              `json:"generation"`
	Part       string              `json:"part"`
	Chunker    string              `json:"chunker"`
	Kind       string              `json:"kind"`
	Options    search.ChunkOptions `json:"options"`
	ObjectKey  string              `json:"object_key,omitempty"`
	ObjectSize int64               `json:"object_size,omitempty"`
	Text       string              `json:"text,omitempty"`
	Language   string              `json:"language,omitempty"`
}

type chunkResult struct {
	Chunks    int  `json:"chunks"`
	Truncated bool `json:"truncated"`
}

// whole reads an optional whole-number input within bounds.
func (e *env) whole(in map[string]any, name string, v *int, low, high int) error {
	f := float64(*v)
	if err := e.number(in, name, &f); err != nil {
		return err
	}
	if f != float64(int(f)) || int(f) < low || int(f) > high {
		return fmt.Errorf("%s must be a whole number from %d to %d", name, low, high)
	}
	*v = int(f)
	return nil
}

// choice reads an optional text input that must be one of values.
func (e *env) choice(in map[string]any, name string, v *string, values []string) error {
	raw, ok := in[name]
	if !ok {
		return nil
	}
	text, err := e.scope.text(raw)
	if err != nil {
		return err
	}
	if text == "" {
		return nil
	}
	if !slices.Contains(values, text) {
		return fmt.Errorf("%s must be one of %s", name, strings.Join(values, ", "))
	}
	*v = text
	return nil
}

// optionalText reads an optional text input.
func (e *env) optionalText(in map[string]any, name string) (string, error) {
	raw, ok := in[name]
	if !ok {
		return "", nil
	}
	text, err := e.scope.text(raw)
	return strings.TrimSpace(text), err
}

// generation is the content generation a node starts: the run's, named by
// the node, so search.publish can tell a run's own generations.
func (e *env) generation() string { return e.runID + ":" + e.node }

func runSearchChunk(e *env, in map[string]any) (outcome, error) {
	if e.tools.Search == nil {
		return outcome{}, ErrNoSearch
	}
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	job := chunkJob{ItemID: itemID, Part: e.node, Chunker: "window", Kind: search.KindContent,
		Options: search.ChunkOptions{MaxChars: search.DefaultMaxChars, Overlap: search.DefaultOverlap, MaxChunks: search.DefaultMaxChunks}}
	if err = e.choice(in, "chunker", &job.Chunker, search.ChunkerKeys()); err != nil {
		return outcome{}, err
	}
	if err = e.choice(in, "kind", &job.Kind, []string{search.KindContent, search.KindSummary, search.KindExtract}); err != nil {
		return outcome{}, err
	}
	if err = e.whole(in, "max_chars", &job.Options.MaxChars, 100, 20000); err != nil {
		return outcome{}, err
	}
	if err = e.whole(in, "overlap", &job.Options.Overlap, 0, 5000); err != nil {
		return outcome{}, err
	}
	if job.Options.Overlap == 0 {
		job.Options.Overlap = -1
	}
	if err = e.whole(in, "max_chunks", &job.Options.MaxChunks, 1, 2000); err != nil {
		return outcome{}, err
	}
	if job.BlobID, err = e.optionalText(in, "blob_id"); err != nil {
		return outcome{}, err
	}
	// The author must read the file version.
	b, err := e.t.GetBlob(e.ctx, e.author, itemID, job.BlobID)
	if err != nil {
		return outcome{}, err
	}
	job.BlobID = b.ID
	textID, err := e.optionalText(in, "text_id")
	if err != nil {
		return outcome{}, err
	}
	if raw, ok := in["text"]; ok {
		if job.Text, err = e.scope.text(raw); err != nil {
			return outcome{}, err
		}
	}
	switch {
	case textID != "" && job.Text != "":
		return outcome{}, errors.New("give text_id or text, not both")
	case textID != "":
		text, err := e.t.BlobText(e.ctx, textID)
		if err != nil {
			return outcome{}, err
		}
		if text.Sha256 != b.Sha256 {
			return outcome{}, errors.New("text_id is the text of another file")
		}
		job.ObjectKey, job.ObjectSize, job.Language = text.ObjectKey, text.ObjectSize, text.Language
	case job.Text != "":
		if len(job.Text) > maxChunkSource {
			return outcome{}, fmt.Errorf("text is larger than %d bytes; read it with document.read_text and pass text_id", maxChunkSource)
		}
	default:
		return outcome{}, errors.New("give text_id or text")
	}
	if job.Generation, err = e.optionalText(in, "generation"); err != nil {
		return outcome{}, err
	}
	if job.Generation == "" {
		job.Generation = e.generation()
	} else if !strings.HasPrefix(job.Generation, e.runID+":") {
		return outcome{}, errors.New("generation must be one this run started")
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw, Output: map[string]any{"item_id": itemID, "blob_id": b.ID, "generation": job.Generation}}, nil
}

func workSearchChunk(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job chunkJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.Search == nil {
		return nil, ErrNoSearch
	}
	pages := []search.Page{{Text: job.Text}}
	if job.ObjectKey != "" {
		data, err := w.read(job.ObjectKey)
		if err != nil {
			return nil, err
		}
		var stored textObject
		if err = json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("the stored text is unreadable: %w", err)
		}
		pages = pagesOf(stored.Pages)
	}
	chunks, truncated, err := search.Split(job.Chunker, pages, job.Kind, job.Options)
	if err != nil {
		return nil, err
	}
	for i := range chunks {
		chunks[i].Part, chunks[i].Language = job.Part, job.Language
	}
	if err = w.tools.Search.StageContent(w.ctx, job.ItemID, job.BlobID, job.Generation, chunks); err != nil {
		return nil, err
	}
	return json.Marshal(chunkResult{Chunks: len(chunks), Truncated: truncated})
}

func pagesOf(pages []doctext.Page) []search.Page {
	out := make([]search.Page, len(pages))
	for i, p := range pages {
		out[i] = search.Page{Number: p.Number, Text: p.Text}
	}
	return out
}

func commitSearchChunk(_ *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job chunkJob
	var res chunkResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := json.Unmarshal(rawResult, &res); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"generation": job.Generation, "chunks": res.Chunks, "truncated": res.Truncated, "item_id": job.ItemID, "blob_id": job.BlobID}}, nil
}

// publishJob is search.publish's work.
type publishJob struct {
	ItemID     string `json:"item_id"`
	BlobID     string `json:"blob_id"`
	Generation string `json:"generation"`
}

func runSearchPublish(e *env, in map[string]any) (outcome, error) {
	if e.tools.Search == nil {
		return outcome{}, ErrNoSearch
	}
	itemID, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	generation, err := e.optionalText(in, "generation")
	if err != nil {
		return outcome{}, err
	}
	blobID, err := e.optionalText(in, "blob_id")
	if err != nil {
		return outcome{}, err
	}
	out := map[string]any{"published": false, "generation": generation, "item_id": itemID, "blob_id": blobID}
	stale := func(reason string) (outcome, error) {
		out["reason"] = reason
		return outcome{Port: portStale, Output: out}, nil
	}
	if generation == "" {
		return outcome{}, errors.New("generation is required")
	}
	// Fixed checks: a run publishes only its own generations, for a file
	// one of the item's surfaces still has.
	if !strings.HasPrefix(generation, e.runID+":") {
		return stale("The generation was not started by this run.")
	}
	b, err := e.t.GetBlob(e.ctx, e.author, itemID, blobID)
	if errors.Is(err, dms.ErrNotFound) {
		return stale("The item or its file is gone.")
	}
	if err != nil {
		return outcome{}, err
	}
	files, err := e.t.ItemFiles(e.ctx, itemID)
	if err != nil {
		return outcome{}, err
	}
	if !slices.Contains(files, b.ID) {
		return stale("A newer file replaced the one this run read.")
	}
	out["blob_id"] = b.ID
	raw, err := json.Marshal(publishJob{ItemID: itemID, BlobID: b.ID, Generation: generation})
	if err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw, Output: out}, nil
}

func workSearchPublish(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job publishJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.Search == nil {
		return nil, ErrNoSearch
	}
	return json.RawMessage(`{}`), w.tools.Search.PublishContent(w.ctx, job.ItemID, job.BlobID, job.Generation)
}

func commitSearchPublish(_ *env, rawJob, _ json.RawMessage) (outcome, error) {
	var job publishJob
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"published": true, "generation": job.Generation, "item_id": job.ItemID, "blob_id": job.BlobID}}, nil
}
