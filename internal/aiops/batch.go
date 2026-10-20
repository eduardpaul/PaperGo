package aiops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
)

// Batch completions: the ai.batch worker admits completions in groups per
// queue, by its admission policy. A group is sent as one provider batch,
// whose members park until Poll finds the batch finished and ends them
// with its answers. A request the provider failed returns to the queue and
// is sent again, within its attempts; a request whose answer does not
// match its schema fails.

const (
	flightNamespace = "ai.batch.flight"
	pollEvery       = 5 * time.Minute
	// unconfirmed is how long a submission the provider never confirmed is
	// looked for before its members are sent again.
	unconfirmed = time.Hour
	// maxBatchBytes bounds a provider batch's input file.
	maxBatchBytes = 100 << 20
)

// Flight states.
const (
	flightPrepared  = "prepared"
	flightUncertain = "uncertain"
	flightSubmitted = "submitted"
)

// flight is a provider batch in flight, kept in activity records under its
// submission tag.
type flight struct {
	Status    string    `json:"status"`
	BatchID   string    `json:"batch_id,omitempty"`
	Members   []string  `json:"members"`
	ModelKey  string    `json:"model_key"`
	Since     time.Time `json:"since"`
	NextPoll  time.Time `json:"next_poll"`
	Execution string    `json:"execution"`
}

func batchAdmission(payload json.RawMessage) (string, string, error) {
	var c completion
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", "", err
	}
	return c.Queue, c.Key, nil
}

// submit sends a group of batch completions as one provider batch. Cached
// answers end their members at once.
func (a *Assistant) submit(ctx context.Context, s *dms.Service, ops []*operations.Operation) error {
	if a.Batch == nil {
		return ErrNoBatch
	}
	requests := map[string]completion{}
	var send []*operations.Operation
	var keys []string
	for _, op := range ops {
		var c completion
		if err := json.Unmarshal(op.Payload, &c); err != nil {
			return err
		}
		if c.ModelKey != a.Batch.Key() {
			if err := fail(ctx, s, op, "the batch model changed before the request was sent"); err != nil {
				return err
			}
			continue
		}
		cached, found, err := a.findAnswer(ctx, c.Key)
		if err != nil {
			return err
		}
		if found {
			if err = succeed(ctx, s, op, cached.answer(c.ModelKey, true)); err != nil {
				return err
			}
			continue
		}
		if _, seen := requests[c.Key]; !seen {
			keys = append(keys, c.Key)
		}
		requests[c.Key] = c
		send = append(send, op)
	}
	if len(send) == 0 {
		return nil
	}
	tag := submissionTag(a.Batch, send)
	f := flight{Status: flightPrepared, ModelKey: a.Batch.Key(), Since: time.Now().UTC(), Execution: KindBatch + ":" + tag}
	for _, op := range send {
		f.Members = append(f.Members, op.ID)
	}
	records := a.Records
	if err := putFlight(ctx, records, tag, f); err != nil {
		return err
	}
	lines, err := a.jsonl(ctx, keys, requests)
	if err != nil {
		return a.abandon(ctx, tag, err)
	}
	fileID, err := a.Batch.Upload(ctx, lines)
	if err != nil {
		return a.abandon(ctx, tag, dms.Retry(err, pollEvery))
	}
	f.Status = flightUncertain
	if err = putFlight(ctx, records, tag, f); err != nil {
		return err
	}
	if f.BatchID, err = a.Batch.Create(ctx, fileID, tag); err != nil && !errors.Is(err, ai.ErrUncertain) {
		return a.abandon(ctx, tag, dms.Retry(err, pollEvery))
	}
	if err == nil {
		f.Status = flightSubmitted
	}
	f.NextPoll = time.Now().UTC().Add(pollEvery)
	return s.Write(ctx, func(t *dms.Service) error {
		if err := t.ParkOperations(ctx, send, f.Execution); err != nil {
			return err
		}
		return putFlight(ctx, records.WithTx(t.SQLTx()), tag, f)
	})
}

// abandon forgets a submission that never reached the provider and returns
// err, which gives its members back.
func (a *Assistant) abandon(ctx context.Context, tag string, err error) error {
	return errors.Join(err, doneFlight(ctx, a.Records, tag))
}

// submissionTag names a provider batch by its members and their attempts,
// so a retry sends a new batch.
func submissionTag(b ai.Batcher, ops []*operations.Operation) string {
	parts := make([]string, len(ops))
	for i, op := range ops {
		parts[i] = op.ID + ":" + strconv.Itoa(op.Attempts)
	}
	sort.Strings(parts)
	return ai.CacheKey(b.Connection(), append([]string{b.Key()}, parts...)...)
}

func (a *Assistant) jsonl(ctx context.Context, keys []string, requests map[string]completion) ([]byte, error) {
	var buf bytes.Buffer
	for _, key := range keys {
		c := requests[key]
		input, err := a.input(ctx, c)
		if err != nil {
			return nil, err
		}
		images, err := a.images(ctx, c.Images)
		if err != nil {
			return nil, err
		}
		line, err := json.Marshal(map[string]any{
			"custom_id": key, "method": "POST", "url": "/v1/chat/completions",
			"body": ai.CompletionBody(c.Model, ai.Completion{Instructions: c.Prompt, Input: input, Schema: c.Schema, Images: images}),
		})
		if err != nil {
			return nil, err
		}
		if buf.Len()+len(line)+1 > maxBatchBytes {
			return nil, errors.New("the batch input exceeds 100 MiB; lower the worker's batch size")
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func putFlight(ctx context.Context, records *activitystore.Store, tag string, f flight) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return records.Put(ctx, activitystore.Record{Namespace: flightNamespace, Scope: answerScope, Key: tag, Payload: raw})
}

// doneFlight lets a flight's record expire.
func doneFlight(ctx context.Context, records *activitystore.Store, tag string) error {
	now := time.Now().UTC()
	return records.Put(ctx, activitystore.Record{Namespace: flightNamespace, Scope: answerScope, Key: tag, Payload: json.RawMessage(`{}`), State: "completed", ExpiresAt: &now})
}

// Poll looks at the provider batches in flight that are due: finished
// batches end their members, an unconfirmed submission is looked for by
// its tag, and members of a batch that never reached the provider return
// to the queue. It is a maintenance task.
func (a *Assistant) Poll(ctx context.Context, now time.Time) error {
	if a.Batch == nil || a.Records == nil {
		return nil
	}
	after := ""
	for {
		page, err := a.Records.List(ctx, flightNamespace, answerScope, "pending", after, 100)
		if err != nil || len(page) == 0 {
			return err
		}
		for _, r := range page {
			after = r.Key
			var f flight
			if err = json.Unmarshal(r.Payload, &f); err != nil {
				return err
			}
			if f.Status == flightPrepared || f.NextPoll.After(now) {
				continue
			}
			if err = a.poll(ctx, r.Key, f, now); err != nil {
				return fmt.Errorf("poll AI batch %s: %w", r.Key, err)
			}
		}
	}
}

func (a *Assistant) poll(ctx context.Context, tag string, f flight, now time.Time) error {
	if f.Status == flightUncertain {
		id, found, err := a.Batch.FindByTag(ctx, tag)
		if err != nil {
			return err
		}
		if !found {
			if now.Sub(f.Since) > unconfirmed {
				return a.release(ctx, tag, f, f.Members)
			}
			f.NextPoll = now.Add(pollEvery)
			return putFlight(ctx, a.Records, tag, f)
		}
		f.Status, f.BatchID = flightSubmitted, id
	}
	batch, err := a.Batch.Get(ctx, f.BatchID)
	if err != nil || !batch.Terminal() {
		f.NextPoll = now.Add(pollEvery)
		return errors.Join(err, putFlight(ctx, a.Records, tag, f))
	}
	members := map[string]*operations.Operation{}
	requests := map[string]completion{}
	for _, id := range f.Members {
		op, err := a.member(ctx, id)
		if err != nil {
			return err
		}
		if op == nil || op.Status != operations.StatusPending {
			continue
		}
		var c completion
		if err = json.Unmarshal(op.Payload, &c); err != nil {
			return err
		}
		members[id], requests[c.Key] = op, c
	}
	lines, err := a.lines(ctx, batch, requests)
	if err != nil {
		return err
	}
	var retry []string
	for _, id := range f.Members {
		op := members[id]
		if op == nil {
			continue
		}
		var c completion
		if err = json.Unmarshal(op.Payload, &c); err != nil {
			return err
		}
		l, ok := lines[c.Key]
		status, message := operations.StatusSucceeded, ""
		var result json.RawMessage
		switch {
		case !ok || l.err != nil && !errors.Is(l.err, errSchema):
			retry = append(retry, id)
			continue
		case l.err != nil:
			status, message = operations.StatusFailed, l.err.Error()
		default:
			if result, err = json.Marshal(l.answer.answer(c.ModelKey, false)); err != nil {
				return err
			}
		}
		if err = a.svc.Write(ctx, func(t *dms.Service) error {
			return endMember(ctx, t, op, f.Execution, status, result, message)
		}); err != nil {
			return err
		}
	}
	return a.release(ctx, tag, f, retry)
}

// release returns members of a flight to the queue and ends the flight.
func (a *Assistant) release(ctx context.Context, tag string, f flight, ids []string) error {
	return a.svc.Write(ctx, func(t *dms.Service) error {
		if len(ids) > 0 {
			if err := t.UnparkOperations(ctx, f.Execution, ids, time.Now()); err != nil {
				return err
			}
		}
		return doneFlight(ctx, a.Records.WithTx(t.SQLTx()), tag)
	})
}

// member returns a flight member, or nil when it is gone.
func (a *Assistant) member(ctx context.Context, id string) (*operations.Operation, error) {
	ops, err := a.svc.DAL.Operations().List(ctx, operations.Filter{IDs: []string{id}})
	if err != nil || len(ops) == 0 {
		return nil, err
	}
	return &ops[0], nil
}

// endMember ends a parked member; one that ended or left the batch
// meanwhile is left alone.
func endMember(ctx context.Context, t *dms.Service, op *operations.Operation, execution string, status operations.Status, result json.RawMessage, message string) error {
	op.Status, op.Result, op.Error = status, result, message
	err := t.FinishParkedOperation(ctx, op, execution)
	if errors.Is(err, operations.ErrClaimLost) {
		return nil
	}
	return err
}

// line is the outcome of one request of a finished provider batch.
type line struct {
	answer cachedAnswer
	err    error
}

// lines reads a finished batch's answers by request key, caching those
// that match their request.
func (a *Assistant) lines(ctx context.Context, batch ai.Batch, requests map[string]completion) (map[string]line, error) {
	out := map[string]line{}
	for _, file := range []string{batch.OutputFileID, batch.ErrorFileID} {
		if file == "" {
			continue
		}
		raw, err := a.Batch.Download(ctx, file)
		if err != nil {
			return nil, err
		}
		for _, row := range bytes.Split(raw, []byte("\n")) {
			key, l := a.parseLine(ctx, bytes.TrimSpace(row), requests)
			if key == "" {
				continue
			}
			if prev, ok := out[key]; ok && prev.err == nil {
				continue
			}
			out[key] = l
		}
	}
	return out, nil
}

func (a *Assistant) parseLine(ctx context.Context, row []byte, requests map[string]completion) (string, line) {
	var parsed struct {
		CustomID string `json:"custom_id"`
		Response struct {
			StatusCode int             `json:"status_code"`
			Body       json.RawMessage `json:"body"`
		} `json:"response"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(row) == 0 || json.Unmarshal(row, &parsed) != nil || parsed.CustomID == "" {
		return "", line{}
	}
	if parsed.Response.StatusCode != 200 {
		message := "the provider rejected the request"
		if parsed.Error != nil && parsed.Error.Message != "" {
			message = parsed.Error.Message
		}
		return parsed.CustomID, line{err: errors.New(shortText(message, 300))}
	}
	c, ok := requests[parsed.CustomID]
	if !ok {
		return "", line{}
	}
	answer, err := ai.ParseCompletion(parsed.Response.Body, c.Schema != nil)
	if err != nil {
		return parsed.CustomID, line{err: err}
	}
	cached, err := a.acceptAnswer(ctx, c, answer)
	return parsed.CustomID, line{answer: cached, err: err}
}

// fail ends a claimed operation with message.
func fail(ctx context.Context, s *dms.Service, op *operations.Operation, message string) error {
	return s.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		op.Status, op.Error, op.CompletedAt = operations.StatusFailed, message, &completed
		return t.SaveOperation(ctx, op)
	})
}

// Collect deletes the provider batch records past their expiry and the
// cached answers idle past ResultTTL. It is a maintenance task.
func (a *Assistant) Collect(ctx context.Context, now time.Time) error {
	if a.Records != nil {
		if err := a.Records.Purge(ctx, now); err != nil {
			return err
		}
	}
	if results, ok := a.Results.(interface {
		PurgeExpired(context.Context, time.Time) (int64, error)
	}); ok {
		if _, err := results.PurgeExpired(ctx, now); err != nil {
			return err
		}
	}
	return nil
}
