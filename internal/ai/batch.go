package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// MetadataKey is the provider batch metadata entry that identifies one
// PaperGo submission. Reconciliation looks it up before creating again.
const MetadataKey = "papergo_batch"

// ErrUncertain means a batch create did not confirm whether the provider
// stored it. The caller must look the submission up by metadata before
// creating it again.
var ErrUncertain = errors.New("the batch submission did not confirm whether it was created")

// BatchConfig configures the OpenAI-compatible batch endpoint (AI_BATCH_*).
// It is independent of the chat endpoint, model and credential.
type BatchConfig struct {
	Provider string
	Endpoint string
	Model    string
	APIKey   string
}

func (c BatchConfig) Enabled() bool { return c.Provider != "" && c.Provider != ProviderNone }

func (c BatchConfig) Validate() error {
	switch c.Provider {
	case "", ProviderNone:
		return nil
	case ProviderOpenAI:
	default:
		return errors.New("the provider must be none or openai")
	}
	if strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 {
		return errors.New("a batch model needs a name of at most 200 characters")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the endpoint must be an http or https URL without credentials or a query")
	}
	return nil
}

// Batch is one provider batch.
type Batch struct {
	ID           string
	Status       string
	OutputFileID string
	ErrorFileID  string
}

// Terminal reports a batch that will not produce more output.
func (b Batch) Terminal() bool {
	switch b.Status {
	case "completed", "failed", "expired", "cancelled":
		return true
	default:
		return false
	}
}

// Batcher submits independent chat requests and reads them back. Create does
// not retry: an ambiguous failure is ErrUncertain.
type Batcher interface {
	// Key identifies the model and endpoint the way a chat model key does,
	// so identical immediate and batch requests share a cached answer.
	Key() string
	// Connection is the batch API base. Requests are grouped by it and the
	// effective model.
	Connection() string
	// Model is the provider model or deployment name sent in each line.
	Model() string
	Upload(ctx context.Context, jsonl []byte) (string, error)
	Create(ctx context.Context, inputFileID, tag string) (string, error)
	FindByTag(ctx context.Context, tag string) (string, bool, error)
	Get(ctx context.Context, batchID string) (Batch, error)
	Download(ctx context.Context, fileID string) ([]byte, error)
}

type openAIBatch struct {
	model  string
	base   string
	apiKey string
	client *http.Client
}

// NewBatch returns the configured batch client, or nil when none is.
func NewBatch(c BatchConfig, client *http.Client) (Batcher, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{}
	}
	return &openAIBatch{model: c.Model, base: BatchBase(c.Endpoint), apiKey: c.APIKey, client: client}, nil
}

// BatchBase is the files and batches root. An endpoint that already ends at
// /openai/v1 is that root. A chat-completions URL loses that suffix.
func BatchBase(endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	return strings.TrimRight(base, "/")
}

func (b *openAIBatch) Key() string {
	return ProviderOpenAI + ":" + b.model + ":" + CacheKey(b.base+"/chat/completions")
}

func (b *openAIBatch) Connection() string { return b.base }

func (b *openAIBatch) Model() string { return b.model }

func (b *openAIBatch) Upload(ctx context.Context, jsonl []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("purpose", "batch"); err != nil {
		return "", err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="batch.jsonl"`)
	header.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(jsonl); err != nil {
		return "", err
	}
	if err = w.Close(); err != nil {
		return "", err
	}
	raw, status, err := b.do(ctx, http.MethodPost, b.base+"/files", &body, w.FormDataContentType())
	if err != nil {
		return "", err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
	}
	var out struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return "", errors.New("the uploaded batch file has no id")
	}
	return out.ID, nil
}

func (b *openAIBatch) Create(ctx context.Context, inputFileID, tag string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"input_file_id":     inputFileID,
		"endpoint":          "/v1/chat/completions",
		"completion_window": "24h",
		"metadata":          map[string]string{MetadataKey: tag},
	})
	if err != nil {
		return "", err
	}
	raw, status, err := b.do(ctx, http.MethodPost, b.base+"/batches", bytes.NewReader(payload), "application/json")
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUncertain, err.Error())
	}
	if status >= 500 || status == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: HTTP %d", ErrUncertain, status)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
	}
	parsed, err := decodeBatch(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUncertain, err.Error())
	}
	return parsed.ID, nil
}

func (b *openAIBatch) FindByTag(ctx context.Context, tag string) (string, bool, error) {
	after := ""
	for page := 0; ; page++ {
		if page >= 100 {
			return "", false, fmt.Errorf("%w: provider batch listing exceeds reconciliation budget", ErrUncertain)
		}
		u := b.base + "/batches?limit=100"
		if after != "" {
			u += "&after=" + url.QueryEscape(after)
		}
		raw, status, err := b.do(ctx, http.MethodGet, u, nil, "")
		if err != nil {
			return "", false, err
		}
		if status != http.StatusOK {
			return "", false, &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
		}
		var list struct {
			Data    []json.RawMessage `json:"data"`
			HasMore bool              `json:"has_more"`
		}
		if err = json.Unmarshal(raw, &list); err != nil {
			return "", false, err
		}
		var last string
		for _, item := range list.Data {
			parsed, err := decodeBatch(item)
			if err != nil {
				return "", false, err
			}
			last = parsed.ID
			found, ok, err := b.tagMatches(ctx, item, parsed.ID, tag)
			if err != nil || ok {
				return found, ok, err
			}
		}
		if !list.HasMore {
			return "", false, nil
		}
		if last == "" || last == after {
			return "", false, fmt.Errorf("%w: provider listing cannot advance", ErrUncertain)
		}
		after = last
	}
}

func (b *openAIBatch) tagMatches(ctx context.Context, item json.RawMessage, id, tag string) (string, bool, error) {
	var meta struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(item, &meta); err != nil {
		return "", false, err
	}
	if meta.Metadata == nil {
		raw, status, err := b.do(ctx, http.MethodGet, b.base+"/batches/"+url.PathEscape(id), nil, "")
		if err != nil || status != http.StatusOK {
			if err == nil {
				err = &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
			}
			return "", false, err
		}
		if err = json.Unmarshal(raw, &meta); err != nil {
			return "", false, err
		}
	}
	if text, _ := meta.Metadata[MetadataKey].(string); text == tag {
		return id, true, nil
	}
	return "", false, nil
}

func (b *openAIBatch) Get(ctx context.Context, batchID string) (Batch, error) {
	raw, status, err := b.do(ctx, http.MethodGet, b.base+"/batches/"+url.PathEscape(batchID), nil, "")
	if err != nil {
		return Batch{}, err
	}
	if status != http.StatusOK {
		return Batch{}, &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
	}
	return decodeBatch(raw)
}

func (b *openAIBatch) Download(ctx context.Context, fileID string) ([]byte, error) {
	raw, status, err := b.do(ctx, http.MethodGet, b.base+"/files/"+url.PathEscape(fileID)+"/content", nil, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &Error{Model: "batch model", Status: status, Message: errorMessage(raw)}
	}
	return raw, nil
}

func decodeBatch(raw []byte) (Batch, error) {
	var out struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		OutputFileID string `json:"output_file_id"`
		ErrorFileID  string `json:"error_file_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		return Batch{}, errors.New("the provider batch is unreadable")
	}
	return Batch{ID: out.ID, Status: out.Status, OutputFileID: out.OutputFileID, ErrorFileID: out.ErrorFileID}, nil
}

func (b *openAIBatch) do(ctx context.Context, method, endpoint string, body io.Reader, contentType string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(raw) > maxResponse {
		return nil, resp.StatusCode, errors.New("batch provider response exceeds the size limit")
	}
	return raw, resp.StatusCode, nil
}
