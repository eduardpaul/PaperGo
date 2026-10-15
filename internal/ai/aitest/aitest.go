// Package aitest is a fake OpenAI-compatible server for tests. Embeddings:
// each word of a text adds a fixed direction derived from its hash, so texts
// sharing words are similar and the vectors are the same on every run. Chat
// completions answer from the input deterministically (Answer).
package aitest

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"
)

// Dimensions is the length of the fake vectors.
const Dimensions = 64

// Server is a fake embeddings endpoint.
type Server struct {
	*httptest.Server
	// Requests counts embedding requests; Inputs counts the texts embedded;
	// Chats counts chat completions.
	Requests, Inputs, Chats atomic.Int64
	mu                      sync.Mutex
	failures                int
	status                  int
}

// New starts a fake server, closed when the test ends. Its URL is the
// endpoint (…/v1).
func New(t testing.TB) *Server {
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Fail makes the next n requests answer with status.
func (s *Server) Fail(n, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures, s.status = n, status
}

// Endpoint is the base URL to configure.
func (s *Server) Endpoint() string { return s.URL + "/v1" }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	chat := r.URL.Path == "/v1/chat/completions"
	if r.Method != http.MethodPost || !chat && r.URL.Path != "/v1/embeddings" {
		http.NotFound(w, r)
		return
	}
	if chat {
		s.Chats.Add(1)
	} else {
		s.Requests.Add(1)
	}
	s.mu.Lock()
	if s.failures > 0 {
		s.failures--
		status := s.status
		s.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"error":{"message":"unavailable"}}`, status)
		return
	}
	s.mu.Unlock()
	if chat {
		s.chat(w, r)
		return
	}
	var body struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || len(body.Input) == 0 {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	s.Inputs.Add(int64(len(body.Input)))
	type datum struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	data := make([]datum, len(body.Input))
	for i, text := range body.Input {
		data[len(data)-1-i] = datum{i, Vector(text)} // out of order, as providers may answer
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": body.Model})
}

// Vector is the fake embedding of a text: not normalized, as providers do
// not promise it.
func Vector(text string) []float32 {
	v := make([]float32, Dimensions)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		h := fnv.New64a()
		h.Write([]byte(w))
		sum := h.Sum64()
		// Three dimensions per word, with signs, so different words are
		// almost orthogonal.
		for k := range 3 {
			bits := sum >> (k * 16)
			sign := float32(1)
			if bits&0x8000 != 0 {
				sign = -1
			}
			v[int(bits%Dimensions)] += 2 * sign
		}
	}
	// A small constant part, so no text has a zero vector.
	v[0] += 0.01
	return v
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		ResponseFormat *struct {
			JSONSchema struct {
				Schema map[string]any `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model == "" || len(body.Messages) != 2 {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	var schema map[string]any
	if body.ResponseFormat != nil {
		schema = body.ResponseFormat.JSONSchema.Schema
	}
	content := Answer(body.Messages[0].Content, body.Messages[1].Content, schema)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "chat.completion", "model": body.Model,
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": content}}}})
}

// Answer is the fake chat answer: with a schema, a JSON object with a value
// for each property (strings name the property and the input's first word,
// numbers are 42, booleans true); else the instructions' first word and the
// input's first twelve words.
func Answer(instructions, input string, schema map[string]any) string {
	words := strings.Fields(input)
	first := ""
	if len(words) > 0 {
		first = words[0]
	}
	if schema != nil {
		out := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		for key, raw := range props {
			prop, _ := raw.(map[string]any)
			switch prop["type"] {
			case "number", "integer":
				out[key] = 42
			case "boolean":
				out[key] = true
			default:
				out[key] = key + " " + first
			}
		}
		raw, _ := json.Marshal(out)
		return "```json\n" + string(raw) + "\n```"
	}
	verb := "Answer"
	if f := strings.Fields(instructions); len(f) > 0 {
		verb = strings.Trim(f[0], ":,.")
	}
	return verb + ": " + strings.Join(words[:min(len(words), 12)], " ")
}
