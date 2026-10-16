package ai

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ChatConfig configures the chat model AI workflow steps use (AI_CHAT_*).
type ChatConfig struct {
	// Provider is none (AI steps off) or openai (any OpenAI-compatible
	// /v1/chat/completions endpoint).
	Provider string
	Endpoint string
	Model    string
	APIKey   string
}

// Enabled reports whether a chat model is configured.
func (c ChatConfig) Enabled() bool { return c.Provider != "" && c.Provider != ProviderNone }

// Validate checks a configuration.
func (c ChatConfig) Validate() error {
	switch c.Provider {
	case "", ProviderNone:
		return nil
	case ProviderOpenAI:
	default:
		return errors.New("the provider must be none or openai")
	}
	if strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 {
		return errors.New("a chat model needs a name of at most 200 characters")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the endpoint must be an http or https URL without credentials or a query")
	}
	return nil
}

// Image is one picture sent with a completion. Bytes are the file contents.
type Image struct {
	MediaType string
	Bytes     []byte
}

// Completion asks the chat model for one answer.
type Completion struct {
	// Instructions say what to do (the system message).
	Instructions string
	// Input is what to do it with (the user message).
	Input string
	// Images are pictures the model sees with the input. None keeps the
	// user message as text, which is what a deployment without vision sends.
	Images []Image
	// Schema, when set, asks for a JSON object answer matching this JSON
	// schema.
	Schema map[string]any
}

// Answer is the chat model's answer: JSON when a schema was given, else
// Text.
type Answer struct {
	Text string
	JSON map[string]any
}

// Chat is a chat model.
type Chat interface {
	// Key identifies the model, so cached answers of another model are not
	// reused.
	Key() string
	Complete(ctx context.Context, c Completion) (Answer, error)
}

// NewChat returns the configured chat model, or nil when none is.
func NewChat(c ChatConfig, client *http.Client) (Chat, error) {
	if !c.Enabled() {
		return nil, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &openAIChat{model: c.Model, endpoint: newEndpoint("chat model", c.Endpoint, "/chat/completions", c.APIKey, client)}, nil
}

type openAIChat struct {
	model    string
	endpoint endpoint
}

func (c *openAIChat) Key() string {
	return ProviderOpenAI + ":" + c.model + ":" + CacheKey(c.endpoint.url)
}

// Complete asks for the answer at temperature 0, so equal requests get
// equal answers as far as the model allows.
func (c *openAIChat) Complete(ctx context.Context, in Completion) (Answer, error) {
	payload, err := json.Marshal(CompletionBody(c.model, in))
	if err != nil {
		return Answer{}, err
	}
	var out Answer
	err = c.endpoint.call(ctx, payload, func(raw []byte) error {
		out, err = ParseCompletion(raw, in.Schema != nil)
		return err
	})
	return out, err
}

// CompletionBody is the JSON body of one chat completion. Without images the
// user message stays text. Images use data URLs so the request carries no
// separate file handle.
func CompletionBody(model string, in Completion) map[string]any {
	var user any = in.Input
	if len(in.Images) > 0 {
		parts := make([]any, 0, 1+len(in.Images))
		if in.Input != "" {
			parts = append(parts, map[string]any{"type": "text", "text": in.Input})
		}
		for _, image := range in.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{
				"url": "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Bytes),
			}})
		}
		user = parts
	}
	body := map[string]any{"model": model, "temperature": 0, "messages": []any{
		map[string]any{"role": "system", "content": in.Instructions},
		map[string]any{"role": "user", "content": user},
	}}
	if in.Schema != nil {
		body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "schema": in.Schema}}
	}
	return body
}

// ParseCompletion reads one chat-completion response. structured asks for a
// JSON object; otherwise the answer is text.
func ParseCompletion(raw []byte, structured bool) (Answer, error) {
	var answer struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Choices) == 0 {
		return Answer{}, errors.New("the answer is not a chat completion")
	}
	choice := answer.Choices[0]
	if choice.FinishReason == "length" {
		return Answer{}, errors.New("the answer was cut off at the model's length limit")
	}
	text := strings.TrimSpace(choice.Message.Content)
	if !structured {
		if text == "" {
			return Answer{}, errors.New("the answer is empty")
		}
		return Answer{Text: text}, nil
	}
	var out Answer
	if err := json.Unmarshal([]byte(unfence(text)), &out.JSON); err != nil || out.JSON == nil {
		return Answer{}, errors.New("the answer is not a JSON object")
	}
	return out, nil
}

// unfence strips a Markdown code fence some models put around JSON.
func unfence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
}

// CacheKey is the key of a cached answer: the SHA-256 of the model and the
// parts of the request, each delimited, as hex.
func CacheKey(model string, parts ...string) string {
	h := sha256.New()
	for _, p := range append([]string{model}, parts...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
