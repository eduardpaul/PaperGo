package aiops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
		cached, found, err := a.findAnswer(ctx, s, c.Key)
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
	batchRequests, err := a.batchRequests(ctx, keys, requests)
	if err != nil {
		return a.abandon(ctx, tag, err)
	}
	input, err := a.Batch.Prepare(ctx, batchRequests)
	if err != nil {
		return a.abandon(ctx, tag, dms.Retry(err, pollEvery))
	}
	// From here the provider may hold the batch, so its members belong to
	// the flight before it is created: a crash leaves them parked for Poll
	// to reconcile by the tag, never claimable for a second batch.
	f.Status, f.NextPoll = flightUncertain, time.Now().UTC().Add(pollEvery)
	if err = s.Write(ctx, func(t *dms.Service) error {
		if err := t.ParkOperations(ctx, send, operations.Parking{ExecutionID: f.Execution, CountAttempt: true}); err != nil {
			return err
		}
		return putFlight(ctx, records.WithTx(t.SQLTx()), tag, f)
	}); err != nil {
		return err
	}
	f.BatchID, err = a.Batch.Create(ctx, input, tag)
	switch {
	case errors.Is(err, ai.ErrUncertain):
		return nil
	case err != nil:
		// The provider refused the batch: its members try again later.
		return a.release(ctx, tag, f, f.Members, time.Now().Add(pollEvery), err.Error())
	}
	f.Status = flightSubmitted
	return putFlight(ctx, records, tag, f)
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

// batchRequests are the completions of a batch, by request key, with
// their input text and images read.
func (a *Assistant) batchRequests(ctx context.Context, keys []string, requests map[string]completion) ([]ai.BatchRequest, error) {
	out := make([]ai.BatchRequest, 0, len(keys))
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
		out = append(out, ai.BatchRequest{Key: key, Completion: ai.Completion{Instructions: c.Prompt, Input: input, Schema: c.Schema, Images: images}})
	}
	return out, nil
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
	if a.Records == nil {
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
			if f.Status == flightPrepared {
				// A submission that stopped before its batch could exist
				// parked nothing; its record goes.
				if now.Sub(f.Since) > unconfirmed {
					if err = doneFlight(ctx, a.Records, r.Key); err != nil {
						return err
					}
				}
				continue
			}
			if f.NextPoll.After(now) {
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
		keys, err := a.memberKeys(ctx, f)
		if err != nil {
			return err
		}
		id, found, err := a.Batch.Find(ctx, ai.BatchSubmission{Tag: tag, Keys: keys, Since: f.Since})
		if errors.Is(err, ai.ErrUndetermined) {
			// A batch that could be this one is still running.
			f.NextPoll = now.Add(pollEvery)
			return putFlight(ctx, a.Records, tag, f)
		}
		if err != nil {
			return err
		}
		if !found {
			if now.Sub(f.Since) > unconfirmed {
				return a.release(ctx, tag, f, f.Members, time.Now(), "the provider never confirmed the batch")
			}
			f.NextPoll = now.Add(pollEvery)
			return putFlight(ctx, a.Records, tag, f)
		}
		f.Status, f.BatchID = flightSubmitted, id
	}
	batch, err := a.Batch.Get(ctx, f.BatchID)
	if err != nil || !batch.Ended {
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
	return a.release(ctx, tag, f, retry, time.Now(), "the provider batch did not answer the request")
}

// release returns members of a flight to the queue, ready at at with
// cause as their error, and ends the flight.
func (a *Assistant) release(ctx context.Context, tag string, f flight, ids []string, at time.Time, cause string) error {
	return a.svc.Write(ctx, func(t *dms.Service) error {
		if len(ids) > 0 {
			if err := t.UnparkOperations(ctx, f.Execution, ids, at, cause); err != nil {
				return err
			}
		}
		return doneFlight(ctx, a.Records.WithTx(t.SQLTx()), tag)
	})
}

// memberKeys are the request keys of a flight's members, as its batch
// holds them: one for requests that share a key.
func (a *Assistant) memberKeys(ctx context.Context, f flight) ([]string, error) {
	var keys []string
	for _, id := range f.Members {
		op, err := a.member(ctx, id)
		if err != nil {
			return nil, err
		}
		if op == nil {
			continue
		}
		var c completion
		if err = json.Unmarshal(op.Payload, &c); err != nil {
			return nil, err
		}
		keys = append(keys, c.Key)
	}
	sort.Strings(keys)
	return slices.Compact(keys), nil
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
	structured := map[string]bool{}
	for key, c := range requests {
		structured[key] = c.Schema != nil
	}
	results, err := a.Batch.Results(ctx, batch, structured)
	if err != nil {
		return nil, err
	}
	out := map[string]line{}
	for _, r := range results {
		c, ok := requests[r.Key]
		if !ok {
			continue
		}
		if prev, ok := out[r.Key]; ok && prev.err == nil {
			continue
		}
		if r.Err != nil {
			out[r.Key] = line{err: r.Err}
			continue
		}
		cached, err := a.acceptAnswer(ctx, c, r.Answer)
		out[r.Key] = line{answer: cached, err: err}
	}
	return out, nil
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
