package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// ProviderAnthropic is Anthropic's Message Batches API.
const ProviderAnthropic = "anthropic"

// DefaultAnthropicEndpoint is the Claude API.
const DefaultAnthropicEndpoint = "https://api.anthropic.com"

const (
	// DefaultMaxTokens bounds an anthropic batch answer unless configured.
	DefaultMaxTokens = 16_000
	// maxStandardTokens is the largest answer without extended output.
	maxStandardTokens = 128_000
	// MaxExtendedTokens is the largest answer extended output allows.
	MaxExtendedTokens = 300_000
	// maxAnthropicBatchBytes keeps a batch under the API's 256 MB bound.
	maxAnthropicBatchBytes = 250 << 20
	// maxAnthropicRequests is the API's bound on requests in a batch.
	maxAnthropicRequests = 100_000
	// findWindow is how long before its preparation a submission's batch
	// may have been created, for clock skew between PaperGo and the API.
	findWindow = 10 * time.Minute
)

// anthropicBatch sends completions to Anthropic's Message Batches API.
// Requests travel inline with the batch, so there is no upload.
type anthropicBatch struct {
	model     string
	base      string
	maxTokens int64
	client    anthropic.Client
	// ended remembers the request keys of the ended batches Find has read,
	// by batch: those results never change and can be large, and Find looks
	// at the same batches on every poll.
	ended sync.Map
}

func newAnthropicBatch(c BatchConfig, client *http.Client) *anthropicBatch {
	base := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if base == "" {
		base = DefaultAnthropicEndpoint
	}
	maxTokens := int64(c.MaxTokens)
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	// PaperGo's settings are the only source: the SDK's environment
	// defaults (ANTHROPIC_BASE_URL, ANTHROPIC_API_KEY and the rest) would
	// change where batches go and who pays for them.
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithBaseURL(base), option.WithHTTPClient(client), option.WithRequestTimeout(RequestTimeout)}
	if c.APIKey != "" {
		opts = append(opts, option.WithAPIKey(c.APIKey))
	}
	return &anthropicBatch{model: c.Model, base: base, maxTokens: maxTokens, client: anthropic.NewClient(opts...)}
}

func (b *anthropicBatch) Key() string {
	return ProviderAnthropic + ":" + b.model + ":" + CacheKey(b.base+"/v1/messages")
}

func (b *anthropicBatch) Connection() string { return b.base }

func (b *anthropicBatch) Model() string { return b.model }

// Prepare checks the requests; they are sent with the batch.
func (b *anthropicBatch) Prepare(_ context.Context, requests []BatchRequest) (BatchInput, error) {
	if len(requests) > maxAnthropicRequests {
		return BatchInput{}, fmt.Errorf("a batch holds at most %d requests; lower the worker's batch size", maxAnthropicRequests)
	}
	size := 0
	for _, r := range requests {
		params, err := json.Marshal(b.params(r.Completion))
		if err != nil {
			return BatchInput{}, err
		}
		if size += len(params) + len(r.Key); size > maxAnthropicBatchBytes {
			return BatchInput{}, errors.New("the batch exceeds 250 MiB; lower the worker's batch size")
		}
	}
	return BatchInput{requests: requests}, nil
}

// params is one Messages request: the instructions as the system prompt,
// the images before the text, and a JSON schema as its output format.
func (b *anthropicBatch) params(c Completion) anthropic.MessageBatchNewParamsRequestParams {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(c.Images))
	for _, image := range c.Images {
		blocks = append(blocks, anthropic.NewImageBlockBase64(image.MediaType, base64.StdEncoding.EncodeToString(image.Bytes)))
	}
	if c.Input != "" || len(blocks) == 0 {
		blocks = append(blocks, anthropic.NewTextBlock(c.Input))
	}
	p := anthropic.MessageBatchNewParamsRequestParams{
		Model:     anthropic.Model(b.model),
		MaxTokens: b.maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)},
	}
	if c.Instructions != "" {
		p.System = []anthropic.TextBlockParam{{Text: c.Instructions}}
	}
	if c.Schema != nil {
		p.OutputConfig = anthropic.OutputConfigParam{Format: anthropic.JSONOutputFormatParam{Schema: StructuredSchema(c.Schema)}}
	}
	return p
}

// Create sends the batch. Answers longer than the standard bound ask for
// extended output.
func (b *anthropicBatch) Create(ctx context.Context, in BatchInput, _ string) (string, error) {
	if len(in.requests) == 0 {
		return "", errors.New("the batch has no requests")
	}
	requests := make([]anthropic.MessageBatchNewParamsRequest, len(in.requests))
	for i, r := range in.requests {
		requests[i] = anthropic.MessageBatchNewParamsRequest{CustomID: r.Key, Params: b.params(r.Completion)}
	}
	var opts []option.RequestOption
	if b.maxTokens > maxStandardTokens {
		opts = append(opts, option.WithHeaderAdd("anthropic-beta", string(anthropic.AnthropicBetaOutput300k2026_03_24)))
	}
	// One attempt: a retried create could submit the batch twice.
	opts = append(opts, option.WithMaxRetries(0))
	batch, err := b.client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: requests}, opts...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusTooManyRequests && apiErr.StatusCode != http.StatusRequestTimeout {
			return "", &Error{Model: "batch model", Status: apiErr.StatusCode, Message: shortMessage(apiErr.Error())}
		}
		return "", fmt.Errorf("%w: %s", ErrUncertain, err.Error())
	}
	if batch.ID == "" {
		return "", fmt.Errorf("%w: the created batch has no id", ErrUncertain)
	}
	return batch.ID, nil
}

// Find looks for the submission among the batches created since it was
// prepared. Message batches carry no metadata, so a batch is the
// submission when it ended holding exactly its request keys. Only a batch
// of the submission's size can be it; while one of those is still running,
// Find cannot tell.
func (b *anthropicBatch) Find(ctx context.Context, s BatchSubmission) (string, bool, error) {
	want := slices.Clone(s.Keys)
	slices.Sort(want)
	want = slices.Compact(want)
	undetermined := false
	pages := b.client.Messages.Batches.ListAutoPaging(ctx, anthropic.MessageBatchListParams{Limit: anthropic.Int(100)})
	for seen := 0; pages.Next(); seen++ {
		batch := pages.Current()
		if seen >= 10_000 {
			return "", false, fmt.Errorf("%w: provider batch listing exceeds reconciliation budget", ErrUncertain)
		}
		// Batches are listed newest first.
		if batch.CreatedAt.Before(s.Since.Add(-findWindow)) {
			break
		}
		counts := batch.RequestCounts
		if counts.Processing+counts.Succeeded+counts.Errored+counts.Canceled+counts.Expired != int64(len(want)) {
			continue
		}
		if batch.ProcessingStatus != anthropic.MessageBatchProcessingStatusEnded {
			undetermined = true
			continue
		}
		keys, err := b.resultKeys(ctx, batch.ID)
		if err != nil {
			return "", false, err
		}
		if slices.Equal(keys, want) {
			return batch.ID, true, nil
		}
	}
	if err := pages.Err(); err != nil {
		return "", false, err
	}
	if undetermined {
		return "", false, ErrUndetermined
	}
	return "", false, nil
}

// resultKeys are the sorted request keys of an ended batch's results.
func (b *anthropicBatch) resultKeys(ctx context.Context, id string) ([]string, error) {
	if keys, ok := b.ended.Load(id); ok {
		return keys.([]string), nil
	}
	stream := b.client.Messages.Batches.ResultsStreaming(ctx, id, anthropic.MessageBatchResultsParams{})
	defer stream.Close()
	var keys []string
	for stream.Next() {
		keys = append(keys, stream.Current().CustomID)
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	b.ended.Store(id, keys)
	return keys, nil
}

func (b *anthropicBatch) Get(ctx context.Context, id string) (Batch, error) {
	batch, err := b.client.Messages.Batches.Get(ctx, id, anthropic.MessageBatchGetParams{})
	if err != nil {
		return Batch{}, err
	}
	return Batch{ID: batch.ID, Ended: batch.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded}, nil
}

// Results streams an ended batch's results: each succeeded message is read
// as an answer; errored, canceled and expired requests are failures.
func (b *anthropicBatch) Results(ctx context.Context, batch Batch, structured map[string]bool) ([]BatchResult, error) {
	stream := b.client.Messages.Batches.ResultsStreaming(ctx, batch.ID, anthropic.MessageBatchResultsParams{})
	defer stream.Close()
	var out []BatchResult
	for stream.Next() {
		r := stream.Current()
		result := BatchResult{Key: r.CustomID}
		switch v := r.Result.AsAny().(type) {
		case anthropic.MessageBatchSucceededResult:
			result.Answer, result.Err = anthropicAnswer(v.Message, structured[r.CustomID])
		case anthropic.MessageBatchErroredResult:
			result.Err = errors.New(shortMessage("the provider rejected the request: " + v.Error.Error.Message))
		case anthropic.MessageBatchCanceledResult:
			result.Err = errors.New("the provider canceled the request")
		case anthropic.MessageBatchExpiredResult:
			result.Err = errors.New("the batch expired before the request was answered")
		default:
			result.Err = fmt.Errorf("the provider returned a %q result", r.Result.Type)
		}
		out = append(out, result)
	}
	return out, stream.Err()
}

// anthropicAnswer reads a message's text, as JSON when structured.
func anthropicAnswer(m anthropic.Message, structured bool) (Answer, error) {
	switch m.StopReason {
	case anthropic.StopReasonMaxTokens:
		return Answer{}, fmt.Errorf("%w: it was cut off at the model's length limit", ErrNoAnswer)
	case anthropic.StopReasonRefusal:
		return Answer{}, fmt.Errorf("%w: it declined to answer", ErrNoAnswer)
	}
	var text strings.Builder
	for _, block := range m.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	answer := strings.TrimSpace(text.String())
	if !structured {
		if answer == "" {
			return Answer{}, errors.New("the answer is empty")
		}
		return Answer{Text: answer}, nil
	}
	var out Answer
	if err := json.Unmarshal([]byte(unfence(answer)), &out.JSON); err != nil || out.JSON == nil {
		return Answer{}, errors.New("the answer is not a JSON object")
	}
	return out, nil
}

// unsupportedSchema lists the JSON schema keywords structured outputs do
// not take. PaperGo checks the answer against the whole schema itself.
var unsupportedSchema = []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "default"}

// StructuredSchema is schema as structured outputs take it: without the
// constraints they do not support, and with every object closed to other
// properties. It copies; schema is unchanged.
func StructuredSchema(schema map[string]any) map[string]any {
	out, _ := structured(schema).(map[string]any)
	return out
}

func structured(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t)+1)
		for k, value := range t {
			if slices.Contains(unsupportedSchema, k) {
				continue
			}
			switch k {
			case "properties", "$defs", "definitions":
				// Names here are properties, not keywords.
				props, ok := value.(map[string]any)
				if !ok {
					out[k] = value
					continue
				}
				copied := make(map[string]any, len(props))
				for name, p := range props {
					copied[name] = structured(p)
				}
				out[k] = copied
			default:
				out[k] = structured(value)
			}
		}
		if out["type"] == "object" || out["properties"] != nil {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, value := range t {
			out[i] = structured(value)
		}
		return out
	default:
		return v
	}
}
