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
	// attempts is how often a request is tried when the provider is busy
	// or failing.
	attempts = 4
	// maxRetryAfter is the longest Retry-After waited for; a longer one
	// fails the call.
	maxRetryAfter = 30 * time.Second
	maxResponse   = 256 << 20
)

// Error is a failed call to a provider.
type Error struct {
	// Model says which model failed: "embedding model" or "chat model".
	Model   string
	Status  int
	Message string
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
	sleep  func(context.Context, time.Duration) error
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
	return endpoint{what: what, url: url, apiKey: apiKey, client: client, now: time.Now, sleep: sleep}
}

// call posts payload and decodes the answer, retrying when the provider is
// busy or failing; an answer decode refuses is not tried again.
func (p endpoint) call(ctx context.Context, payload []byte, decode func([]byte) error) error {
	var last error
	for attempt := range attempts {
		raw, wait, err := p.post(ctx, payload)
		if err == nil {
			if err = decode(raw); err != nil {
				return &Error{Model: p.what, Status: http.StatusOK, Message: err.Error()}
			}
			return nil
		}
		last = err
		if wait < 0 || attempt == attempts-1 {
			break
		}
		if wait == 0 {
			wait = time.Duration(500<<attempt) * time.Millisecond
		}
		if err := p.sleep(ctx, wait); err != nil {
			return err
		}
	}
	return last
}

// post sends one request. wait is how long to wait before trying again: 0
// for the default backoff, negative when trying again cannot help.
func (p endpoint) post(ctx context.Context, payload []byte) (_ []byte, wait time.Duration, _ error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled) {
			return nil, -1, err
		}
		return nil, 0, &Error{Model: p.what, Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, 0, &Error{Model: p.what, Message: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		failure := &Error{Model: p.what, Status: resp.StatusCode, Message: errorMessage(raw)}
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return nil, -1, failure
		}
		wait, ok := retryAfter(resp.Header.Get("Retry-After"), p.now())
		if !ok {
			return nil, -1, failure
		}
		return nil, wait, failure
	}
	return raw, 0, nil
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

// retryAfter reads a Retry-After header (seconds or an HTTP date): 0 when
// absent, and false when it asks to wait longer than is worth waiting.
func retryAfter(header string, now time.Time) (time.Duration, bool) {
	if header == "" {
		return 0, true
	}
	var wait time.Duration
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil {
		wait = time.Duration(max(seconds, 0)) * time.Second
	} else if at, err := http.ParseTime(header); err == nil {
		wait = max(at.Sub(now), 0)
	} else {
		return 0, true
	}
	if wait > maxRetryAfter {
		return 0, false
	}
	// At least a moment, so 0 is not read as "use the backoff".
	return max(wait, time.Millisecond), true
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
