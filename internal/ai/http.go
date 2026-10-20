package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Limits of calls to an OpenAI-compatible endpoint.
const (
	// RequestTimeout bounds one request.
	RequestTimeout = 2 * time.Minute
	// retryDefault is how long to wait before trying a busy or failing
	// provider again when it does not say.
	retryDefault = 30 * time.Second
	// maxRetryAfter bounds the wait a provider's Retry-After asks for.
	maxRetryAfter = time.Hour
	maxResponse   = 256 << 20
)

// Error is a failed call to a provider.
type Error struct {
	// Model says which model failed: "embedding model" or "chat model".
	Model   string
	Status  int
	Message string
	// Retry says the call may succeed later: the provider was busy,
	// failing or unreachable. After is how long it asked to wait.
	Retry bool
	After time.Duration
}

// RetryAfter reports whether err is a provider failure worth trying again
// and how long to wait first: what the provider asked for, else 30
// seconds. A call makes one attempt; the operation that made it defers
// itself for the next one, so no worker waits.
func RetryAfter(err error) (time.Duration, bool) {
	var e *Error
	if !errors.As(err, &e) || !e.Retry {
		return 0, false
	}
	if e.After > 0 {
		return e.After, true
	}
	return retryDefault, true
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Model + ": " + e.Message
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Model, e.Status, e.Message)
}

// endpoint is one route of an OpenAI-compatible API.
type endpoint struct {
	what   string
	url    string
	apiKey string
	client *http.Client
	now    func() time.Time
}

// newEndpoint is route (such as /embeddings) under base, unless base
// already ends with it.
func newEndpoint(what, base, route, apiKey string, client *http.Client) endpoint {
	if client == nil {
		client = &http.Client{}
	}
	url := strings.TrimRight(base, "/")
	if !strings.HasSuffix(url, route) {
		url += route
	}
	return endpoint{what: what, url: url, apiKey: apiKey, client: client, now: time.Now}
}

// call posts payload once and decodes the answer. A busy or failing
// provider returns an Error with Retry set; an answer decode refuses is
// not worth trying again.
func (p endpoint) call(ctx context.Context, payload []byte, decode func([]byte) error) error {
	raw, err := p.post(ctx, payload)
	if err != nil {
		return err
	}
	if err = decode(raw); err != nil {
		return &Error{Model: p.what, Status: http.StatusOK, Message: err.Error()}
	}
	return nil
}

// post sends one request.
func (p endpoint) post(ctx context.Context, payload []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
			return nil, err
		}
		return nil, &Error{Model: p.what, Message: err.Error(), Retry: true}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, &Error{Model: p.what, Message: err.Error(), Retry: true}
	}
	if resp.StatusCode != http.StatusOK {
		failure := &Error{Model: p.what, Status: resp.StatusCode, Message: errorMessage(raw)}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			failure.Retry, failure.After = true, retryAfter(resp.Header.Get("Retry-After"), p.now())
		}
		return nil, failure
	}
	return raw, nil
}

func errorMessage(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error.Message != "" {
		raw = []byte(body.Error.Message)
	}
	message := strings.TrimSpace(string(raw))
	if r := []rune(message); len(r) > 300 {
		message = string(r[:300])
	}
	return message
}

// retryAfter reads a Retry-After header (seconds or an HTTP date), up to an
// hour: 0 when absent or unreadable.
func retryAfter(header string, now time.Time) time.Duration {
	var wait time.Duration
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil {
		wait = time.Duration(max(seconds, 0)) * time.Second
	} else if at, err := http.ParseTime(header); err == nil {
		wait = max(at.Sub(now), 0)
	}
	return min(wait, maxRetryAfter)
}
