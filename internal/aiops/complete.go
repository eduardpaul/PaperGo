package aiops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resultcache"
	"papergo/internal/dms"
	"papergo/internal/doctext"
	"papergo/internal/forms"
)

// CompletionRequest asks the chat model to do something with text or an
// image. The input is text given directly, the text_id of a read of an
// item's file, or an image file version of an item, which can accompany
// text.
type CompletionRequest struct {
	Prompt string `json:"prompt"`
	Input  string `json:"input,omitempty"`
	// WorkspaceID is where a request on text given directly is made; a
	// request on an item's file is made on the item.
	WorkspaceID string `json:"workspace_id,omitempty"`
	ItemID      string `json:"item_id,omitempty"`
	// TextID is a text read from the item's file version BlobID (default
	// its current file).
	TextID string `json:"text_id,omitempty"`
	BlobID string `json:"blob_id,omitempty"`
	// ImageBlobID is an image file version of the item the model sees.
	ImageBlobID string `json:"image_blob_id,omitempty"`
	// MaxChars bounds the input sent, 100 to 200000 (default 20000).
	MaxChars int `json:"max_chars,omitempty"`
	// OutputSchema is a form schema of an object: the answer is JSON
	// matching it, else text.
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	// Execution is immediate or batch; default the deployment's.
	Execution string `json:"execution,omitempty"`
	// Queue is the batch queue, default shared.
	Queue string `json:"queue,omitempty"`
}

// Answer is a finished completion's result.
type Answer struct {
	Text   string         `json:"text,omitempty"`
	JSON   map[string]any `json:"json,omitempty"`
	Cached bool           `json:"cached"`
	Model  string         `json:"model"`
}

// cachedAnswer is what the answer cache keeps.
type cachedAnswer struct {
	Text string         `json:"text,omitempty"`
	JSON map[string]any `json:"json,omitempty"`
}

func (c cachedAnswer) answer(model string, cached bool) Answer {
	return Answer{Text: c.Text, JSON: c.JSON, Cached: cached, Model: model}
}

type imageRef struct {
	ObjectKey   string `json:"object_key"`
	ContentType string `json:"content_type"`
}

// completion is a completion operation's payload: the canonical request
// and how it is sent. Key identifies the answer in the cache.
type completion struct {
	Key        string         `json:"key"`
	ModelKey   string         `json:"model_key"`
	Model      string         `json:"model,omitempty"`
	Connection string         `json:"connection,omitempty"`
	Queue      string         `json:"queue,omitempty"`
	Prompt     string         `json:"prompt"`
	Input      string         `json:"input,omitempty"`
	ObjectKey  string         `json:"object_key,omitempty"`
	MaxChars   int            `json:"max_chars"`
	Schema     map[string]any `json:"schema,omitempty"`
	Images     []imageRef     `json:"images,omitempty"`
	ItemID     string         `json:"item_id,omitempty"`
	Blobs      []string       `json:"blobs,omitempty"`
}

// StartCompletion accepts a completion as subject, who needs read access
// to the item's file versions or to the workspace.
func (a *Assistant) StartCompletion(ctx context.Context, s *dms.Service, subject string, in CompletionRequest) (*dms.OperationView, error) {
	execution := in.Execution
	if execution == "" {
		execution = a.DefaultExecution
	}
	if execution != "batch" {
		execution = "immediate"
	}
	if in.Execution != "" && in.Execution != "immediate" && in.Execution != "batch" {
		return nil, dms.Invalid("execution must be immediate or batch")
	}
	c := completion{Prompt: in.Prompt, Input: in.Input, MaxChars: in.MaxChars, ItemID: in.ItemID}
	kind := KindComplete
	if execution == "batch" {
		if a.Batch == nil {
			return nil, dms.Invalid(ErrNoBatch.Error())
		}
		kind, c.ModelKey, c.Model, c.Connection = KindBatch, a.Batch.Key(), a.Batch.Model(), a.Batch.Connection()
		c.Queue = in.Queue
		if c.Queue == "" {
			c.Queue = defaultQueue
		}
		if err := checkQueue(c.Queue); err != nil {
			return nil, err
		}
	} else {
		if a.Chat == nil {
			return nil, dms.Invalid(ErrNoChat.Error())
		}
		c.ModelKey = a.Chat.Key()
		if in.Queue != "" {
			return nil, dms.Invalid("queue is for batch completions")
		}
	}
	if c.Prompt == "" || len([]rune(c.Prompt)) > maxPromptChars {
		return nil, dms.Invalid(fmt.Sprintf("prompt needs 1 to %d characters", maxPromptChars))
	}
	if c.MaxChars == 0 {
		c.MaxChars = defaultInputChars
	}
	if c.MaxChars < 100 || c.MaxChars > maxInputChars {
		return nil, dms.Invalid(fmt.Sprintf("max_chars must be from 100 to %d", maxInputChars))
	}
	if in.OutputSchema != nil {
		if err := forms.Validate(in.OutputSchema, "output_schema"); err != nil {
			return nil, dms.Invalid(err.Error())
		}
		if in.OutputSchema["type"] != "object" {
			return nil, dms.Invalid("output_schema must describe an object")
		}
		c.Schema = in.OutputSchema
	}
	container := in.ItemID
	if (in.TextID != "" || in.ImageBlobID != "") && in.ItemID == "" {
		return nil, dms.Invalid("text_id and image_blob_id need item_id")
	}
	if in.ItemID == "" {
		if in.WorkspaceID == "" {
			return nil, dms.Invalid("give item_id or workspace_id")
		}
		if _, err := s.Authorize(ctx, subject, in.WorkspaceID, "read"); err != nil {
			return nil, err
		}
		container = in.WorkspaceID
	}
	var source string
	switch {
	case in.TextID != "" && c.Input != "":
		return nil, dms.Invalid("give input or text_id, not both")
	case in.TextID != "":
		b, err := s.GetBlob(ctx, subject, in.ItemID, in.BlobID)
		if err != nil {
			return nil, err
		}
		text, err := s.DAL.Blobs().Text(ctx, in.TextID)
		if err != nil || text.Sha256 != b.Sha256 {
			return nil, dms.Invalid("text_id is not a text of the file")
		}
		c.ObjectKey, source = text.ObjectKey, "text_id:"+in.TextID
		c.Blobs = append(c.Blobs, b.ID)
	default:
		if strings.TrimSpace(c.Input) == "" && in.ImageBlobID == "" {
			return nil, dms.Invalid("give input, text_id or image_blob_id")
		}
		c.Input = shortText(c.Input, c.MaxChars)
		source = "image"
		if c.Input != "" {
			source = "input:" + hashText(c.Input)
		}
	}
	if in.ImageBlobID != "" {
		b, err := s.GetBlob(ctx, subject, in.ItemID, in.ImageBlobID)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(string(b.ContentType), "image/") {
			return nil, dms.Invalid("image_blob_id must be an image file")
		}
		c.Images = append(c.Images, imageRef{ObjectKey: b.ObjectKey, ContentType: string(b.ContentType)})
		c.Blobs = append(c.Blobs, b.ID)
	}
	schema, err := json.Marshal(c.Schema)
	if err != nil {
		return nil, err
	}
	parts := []string{"ai.complete", c.Prompt, source, string(schema), strconv.Itoa(c.MaxChars)}
	for _, image := range c.Images {
		parts = append(parts, image.ObjectKey, image.ContentType)
	}
	c.Key = ai.CacheKey(c.ModelKey, parts...)
	var cached cachedAnswer
	var found bool
	if kind == KindBatch {
		cached, found, err = a.findAnswer(ctx, s, c.Key)
		if err != nil {
			return nil, err
		}
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, container, kind, payload); err != nil || out.Replayed {
			return err
		}
		if kind == KindBatch && found {
			// Cache state changes execution, not the operation's identity.
			// Complete a cache hit in this acceptance transaction so it does
			// not wait for the batch worker's admission policy.
			now := time.Now()
			claimed, err := t.DAL.Operations().Claim(ctx, operations.ClaimRequest{
				Ready:  operations.Ready{Kinds: []string{kind}, IDs: []string{out.ID}, Now: now, Limit: 1},
				Holder: "ai.cache", TTL: time.Minute,
			})
			if err != nil {
				return err
			}
			if len(claimed) != 1 {
				return operations.ErrClaimLost
			}
			claimed[0].StartedAt = &now
			if err = succeed(ctx, t, &claimed[0], cached.answer(c.ModelKey, true)); err != nil {
				return err
			}
			out, err = t.Operation(ctx, subject, out.ID)
			return err
		}
		if len(c.Blobs) == 0 {
			return nil
		}
		return t.LeaseOperationBlobs(ctx, out.ID, c.Blobs)
	})
	return out, err
}

func checkQueue(queue string) error {
	if len(queue) > 64 {
		return dms.Invalid("queue needs 1 to 64 characters")
	}
	for _, r := range queue {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return dms.Invalid("queue uses letters, digits, hyphens and underscores")
		}
	}
	return nil
}

// validateCompletion runs in the accepting transaction: the file versions
// the request reads must still be the item's.
func (a *Assistant) validateCompletion(ctx context.Context, t *dms.Service, subject, containerID string, payload json.RawMessage) error {
	var c completion
	if err := json.Unmarshal(payload, &c); err != nil || c.Key == "" {
		return dms.Invalid("invalid completion payload")
	}
	for _, blobID := range c.Blobs {
		if _, err := t.GetBlob(ctx, subject, c.ItemID, blobID); err != nil {
			return err
		}
	}
	return nil
}

// complete calls the chat model outside the writer, unless the answer is
// cached.
func (a *Assistant) complete(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var c completion
	if err := json.Unmarshal(op.Payload, &c); err != nil {
		return err
	}
	if cached, found, err := a.findAnswer(ctx, s, c.Key); err != nil || found {
		if err != nil {
			return err
		}
		return succeed(ctx, s, op, cached.answer(c.ModelKey, true))
	}
	if a.Chat == nil {
		return ErrNoChat
	}
	if a.Chat.Key() != c.ModelKey {
		return errors.New("the chat model changed before the request ran")
	}
	input, err := a.input(ctx, c)
	if err != nil {
		return err
	}
	images, err := a.images(ctx, c.Images)
	if err != nil {
		return err
	}
	answer, err := a.Chat.Complete(ctx, ai.Completion{Instructions: c.Prompt, Input: input, Schema: c.Schema, Images: images})
	if err != nil {
		return err
	}
	cached, err := a.acceptAnswer(ctx, c, answer)
	if err != nil {
		return err
	}
	return succeed(ctx, s, op, cached.answer(c.ModelKey, false))
}

// input is the text a completion sends: given directly, or read from the
// stored text of a file.
func (a *Assistant) input(ctx context.Context, c completion) (string, error) {
	if c.ObjectKey == "" {
		return c.Input, nil
	}
	data, err := a.readObject(ctx, c.ObjectKey)
	if err != nil {
		return "", err
	}
	var stored struct {
		Pages []doctext.Page `json:"pages"`
	}
	if err = json.Unmarshal(data, &stored); err != nil {
		return "", fmt.Errorf("the stored text is unreadable: %w", err)
	}
	texts := make([]string, len(stored.Pages))
	for i, page := range stored.Pages {
		texts[i] = page.Text
	}
	return shortText(strings.Join(texts, "\n\n"), c.MaxChars), nil
}

func (a *Assistant) images(ctx context.Context, refs []imageRef) ([]ai.Image, error) {
	out := make([]ai.Image, len(refs))
	for i, ref := range refs {
		data, err := a.readObject(ctx, ref.ObjectKey)
		if err != nil {
			return nil, err
		}
		out[i] = ai.Image{MediaType: ref.ContentType, Bytes: data}
	}
	return out, nil
}

func (a *Assistant) findAnswer(ctx context.Context, s *dms.Service, key string) (cachedAnswer, bool, error) {
	var answer cachedAnswer
	if err := a.cacheReady(); err != nil {
		return answer, false, err
	}
	cache := a.Results
	if _, database := cache.(*resultcache.Store); database && s.SQLTx() != nil {
		cache = resultcache.NewTx(s.SQLTx())
	}
	raw, err := cache.Get(ctx, KindComplete, answerScope, key)
	if err != nil || raw == nil {
		return answer, false, err
	}
	return answer, true, json.Unmarshal(raw, &answer)
}

// errSchema marks an answer that does not match the output schema, which
// asking again would not change.
var errSchema = errors.New("the model's answer does not match output_schema")

// acceptAnswer checks an answer against the request's schema and caches
// it; a concurrent answer cached first wins.
func (a *Assistant) acceptAnswer(ctx context.Context, c completion, answer ai.Answer) (cachedAnswer, error) {
	if err := a.cacheReady(); err != nil {
		return cachedAnswer{}, err
	}
	out := cachedAnswer{Text: answer.Text}
	if c.Schema != nil {
		values, err := forms.Values(c.Schema, answer.JSON, "answer")
		if err != nil {
			return cachedAnswer{}, fmt.Errorf("%w: %v", errSchema, err)
		}
		out = cachedAnswer{JSON: values}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return cachedAnswer{}, err
	}
	if len(raw) > maxAnswer {
		return cachedAnswer{}, fmt.Errorf("%w: the answer is larger than %d bytes", errSchema, maxAnswer)
	}
	if raw, err = a.Results.PutIfAbsent(ctx, KindComplete, answerScope, c.Key, raw, a.ResultTTL); err != nil {
		return cachedAnswer{}, err
	}
	out = cachedAnswer{}
	return out, json.Unmarshal(raw, &out)
}
