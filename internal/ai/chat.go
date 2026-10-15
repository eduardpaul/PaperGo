package ai

import (
	"context"
	"crypto/sha256"
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

// Completion asks the chat model for one answer.
type Completion struct {
	// Instructions say what to do (the system message).
	Instructions string
	// Input is what to do it with (the user message).
	Input string
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
	body := map[string]any{"model": c.model, "temperature": 0, "messages": []map[string]string{
		{"role": "system", "content": in.Instructions},
		{"role": "user", "content": in.Input},
	}}
	if in.Schema != nil {
		body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "schema": in.Schema}}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Answer{}, err
	}
	var out Answer
	err = c.endpoint.call(ctx, payload, func(raw []byte) error {
		var answer struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Choices) == 0 {
			return errors.New("the answer is not a chat completion")
		}
		choice := answer.Choices[0]
		if choice.FinishReason == "length" {
			return errors.New("the answer was cut off at the model's length limit")
		}
		text := strings.TrimSpace(choice.Message.Content)
		if in.Schema == nil {
			if text == "" {
				return errors.New("the answer is empty")
			}
			out.Text = text
			return nil
		}
		if err := json.Unmarshal([]byte(unfence(text)), &out.JSON); err != nil || out.JSON == nil {
			return errors.New("the answer is not a JSON object")
		}
		return nil
	})
	return out, err
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
