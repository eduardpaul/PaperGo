// Package scriptrun performs workflow script effects on PaperGo. A script's
// API requests run in-process through the API as the run's author: a
// request that may change something in the transaction that records it, a
// read like an API client's, recorded with the next durable step. Its
// waits are steps linked to the operations they wait for.
package scriptrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"papergo/internal/dal/operations"
	"papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/script"
)

// maxResponse bounds a recorded response body. A larger response is
// recorded as a 413 problem asking for a smaller page.
const maxResponse = 1 << 20

// Effects performs script effects on a DMS service through its API.
type Effects struct {
	DMS *dms.Service
	API *httpapi.API
}

var _ script.Effects = (*Effects)(nil)

// write commits an effect only while this execution still owns the run.
// Checking after fn also fences effects whose claim expired while they ran.
func (e *Effects) write(ctx context.Context, run script.Run, fn func(*dms.Service) error) error {
	return e.DMS.Write(ctx, func(t *dms.Service) error {
		if err := fn(t); err != nil {
			return err
		}
		return t.DAL.Operations().CheckClaim(ctx, run.ID, run.Claim, time.Now())
	})
}

func (e *Effects) Steps(ctx context.Context, run script.Run) ([]runs.Step, error) {
	return e.DMS.DAL.Runs().Steps(ctx, run.ID)
}

// errRejected rolls back the writes of a request the API refused.
var errRejected = errors.New("the API refused the request")

// Request runs req as the run's author. A successful response commits with
// the request's writes and its step. A 4xx response is recorded without the
// request's writes, so a replay sees the same refusal. A 5xx response is not
// recorded: the slice fails and the request runs again when the run resumes.
func (e *Effects) Request(ctx context.Context, run script.Run, unsaved []runs.Step, step runs.Step, req script.Request) (script.Response, error) {
	var response script.Response
	err := e.write(ctx, run, func(t *dms.Service) error {
		var err error
		if response, err = serve(ctx, e.API.Loopback(t, run.Author, run.ID), req); err != nil {
			return err
		}
		if response.Status >= 500 {
			return fmt.Errorf("%s %s: the API answered %d: %s", req.Method, req.URL, response.Status, response.Body)
		}
		if response.Status >= 400 {
			return errRejected
		}
		step.Output = script.ResponseOutput(response)
		// The step shows the operation the request answered with; whether
		// the run owns it is the operation's parent.
		if location := response.Header["Location"]; response.Status == http.StatusAccepted && strings.HasPrefix(location, "/v1/operations/") {
			step.Operations = []string{strings.TrimPrefix(location, "/v1/operations/")}
		}
		return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
	})
	if errors.Is(err, errRejected) {
		step.Output = script.ResponseOutput(response)
		err = e.write(ctx, run, func(t *dms.Service) error {
			return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
		})
	}
	return response, err
}

// Read runs a GET or HEAD as the run's author outside any transaction of
// the run, as an API client's read runs: it changes nothing, so the engine
// records its step with the next durable one. A 5xx response is an error,
// so the slice fails and the read runs again when the run resumes.
func (e *Effects) Read(ctx context.Context, run script.Run, req script.Request) (script.Response, error) {
	response, err := serve(ctx, e.API.Loopback(e.DMS, run.Author, run.ID), req)
	if err == nil && response.Status >= 500 {
		err = fmt.Errorf("%s %s: the API answered %d: %s", req.Method, req.URL, response.Status, response.Body)
	}
	return response, err
}

// Record records value and read steps the script observed, while this
// execution still owns the run.
func (e *Effects) Record(ctx context.Context, run script.Run, unsaved []runs.Step) error {
	if len(unsaved) == 0 {
		return nil
	}
	return e.write(ctx, run, func(t *dms.Service) error { return t.DAL.Runs().RecordSteps(ctx, unsaved) })
}

func serve(ctx context.Context, handler http.Handler, req script.Request) (script.Response, error) {
	r, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return script.Response{}, err
	}
	for name, value := range req.Header {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	response := script.Response{Status: w.Code, Header: map[string]string{}, Body: w.Body.Bytes()}
	for name := range w.Header() {
		response.Header[name] = w.Header().Get(name)
	}
	if len(response.Body) > maxResponse {
		body, _ := json.Marshal(map[string]string{"code": "response_too_large", "detail": fmt.Sprintf("workflow responses are limited to %d bytes; request a smaller page", maxResponse)})
		response = script.Response{Status: http.StatusRequestEntityTooLarge, Header: map[string]string{"Content-Type": "application/problem+json"}, Body: body}
	}
	return response, nil
}

// Wait records a wait for operations the author can read. It is over when
// none of them is pending; otherwise ReleaseWaits completes it once the last
// one ends, and ReconcileWaits if that release was missed.
func (e *Effects) Wait(ctx context.Context, run script.Run, unsaved []runs.Step, step runs.Step) (json.RawMessage, bool, error) {
	done := true
	err := e.write(ctx, run, func(t *dms.Service) error {
		done, step.Status, step.Output = true, runs.StepPending, nil
		ended := make([]*dms.OperationView, len(step.Operations))
		for i, id := range step.Operations {
			// A run waits for operations its author can read, and for those it
			// owns, such as runs it called of a workflow another author wrote.
			op, err := t.Operation(ctx, run.Author, id)
			if errors.Is(err, dms.ErrNotFound) || errors.Is(err, dms.ErrForbidden) {
				op, err = t.OwnedOperation(ctx, run.ID, id)
			}
			if errors.Is(err, dms.ErrNotFound) || errors.Is(err, dms.ErrForbidden) {
				return fmt.Errorf("%w: %s", script.ErrUnknownOperation, id)
			}
			if err != nil {
				return err
			}
			if op.Status == operations.StatusPending {
				done = false
			}
			ended[i] = op
		}
		if done {
			output, err := json.Marshal(ended)
			if err != nil {
				return err
			}
			step.Status, step.Output = runs.StepCompleted, output
		}
		return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
	})
	return step.Output, done, err
}

// Sleep records a sleep step; the run's worker makes it wait until then.
func (e *Effects) Sleep(ctx context.Context, run script.Run, unsaved []runs.Step, step runs.Step) error {
	return e.write(ctx, run, func(t *dms.Service) error {
		return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
	})
}

// Wake completes a pending sleep step whose time came.
func (e *Effects) Wake(ctx context.Context, run script.Run, seq int) error {
	return e.write(ctx, run, func(t *dms.Service) error {
		_, err := t.DAL.Runs().FinishStep(ctx, run.ID, seq, nil, "")
		return err
	})
}
