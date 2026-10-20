// Package scriptrun performs workflow script effects on PaperGo. A script's
// API requests run in-process through the API as the run's author, in the
// transaction that records them, and its waits are steps linked to the
// operations they wait for.
package scriptrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

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
	err := e.DMS.Write(ctx, func(t *dms.Service) error {
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
		return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
	})
	if errors.Is(err, errRejected) {
		step.Output = script.ResponseOutput(response)
		err = e.DMS.Write(ctx, func(t *dms.Service) error {
			return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
		})
	}
	return response, err
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
func (e *Effects) Wait(ctx context.Context, run script.Run, unsaved []runs.Step, step runs.Step) (bool, error) {
	done := true
	err := e.DMS.Write(ctx, func(t *dms.Service) error {
		done = true
		for _, id := range step.Operations {
			op, err := t.Operation(ctx, run.Author, id)
			if errors.Is(err, dms.ErrNotFound) || errors.Is(err, dms.ErrForbidden) {
				return fmt.Errorf("%w: %s", script.ErrUnknownOperation, id)
			}
			if err != nil {
				return err
			}
			if op.Status == operations.StatusPending {
				done = false
			}
		}
		if done {
			step.Status = runs.StepCompleted
		}
		return t.DAL.Runs().RecordSteps(ctx, append(unsaved, step))
	})
	return done, err
}
