package workflowai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/activitystore"
	waitstore "papergo/internal/dal/waits"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key:         "ai.batch.coordinate",
		Kind:        "action",
		Description: "Admits the installation's parked AI requests for one named queue: configurable groups, a daily partial flush, and provider polling. Originating workflows keep their own authors. The output is counts only.",
		InputSchema: inputSchema(`
			"batch_size": {"type":"integer","title":"Batch size","minimum":1,"maximum":100,"default":20},
			"queue": {"type": "string", "title": "Queue", "minLength": 1, "maxLength": 64, "description": "Named queue. Empty uses shared."},
			"policy": {"type": "string", "title": "Admission", "description": "threshold submits full groups. schedule submits on the flush, including a partial group. both does both."},
			"flush_cron": {"type": "string", "title": "Flush schedule", "description": "When a partial group is sent. Default 01:00 every day."},
			"time_zone": {"type": "string", "title": "Time zone", "description": "Time zone of the flush. Default Europe/Madrid."}`),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"queue": {"type": "string"}, "submitted": {"type": "integer"}, "polled": {"type": "integer"},
			"completed": {"type": "integer"}, "released": {"type": "integer"}, "busy": {"type": "boolean"}}}`),
		Prepare: prepareCoordinate, Work: workCoordinate, Commit: commitCoordinate,
	})
}

type stateData struct {
	NextFlush time.Time `json:"next_flush,omitempty"`
	Cron      string    `json:"cron,omitempty"`
	TimeZone  string    `json:"time_zone,omitempty"`
	Drain     bool      `json:"drain,omitempty"`
}

type flightData struct {
	Tries      int       `json:"tries,omitempty"`
	Status     string    `json:"status"`
	BatchID    string    `json:"batch_id,omitempty"`
	Hashes     []string  `json:"hashes,omitempty"`
	NextPoll   time.Time `json:"next_poll,omitempty"`
	Queue      string    `json:"queue"`
	Connection string    `json:"connection"`
	Model      string    `json:"model"`
}

// groupSubmit is one provider batch. Tries is the attempt already recorded
// on these requests, so a retry does not reuse the metadata of a batch that
// already finished.
type groupSubmit struct {
	Tag    string   `json:"tag"`
	Queue  string   `json:"queue"`
	Hashes []string `json:"hashes"`
	Tries  int      `json:"tries"`
}

type coordinateJob struct {
	Queue      string        `json:"queue"`
	ResultKey  string        `json:"result_key"`
	StateToken string        `json:"state_token"`
	Connection string        `json:"connection"`
	Model      string        `json:"model"`
	Groups     []groupSubmit `json:"groups,omitempty"`
	Reconcile  []string      `json:"reconcile,omitempty"`
	Poll       []pollTarget  `json:"poll,omitempty"`
	Completed  int           `json:"completed,omitempty"`
}

type lineResult struct {
	ResponseKey string `json:"response_key,omitempty"`
	Hash        string `json:"hash"`
	OK          bool   `json:"ok,omitempty"`
	Terminal    bool   `json:"terminal,omitempty"`
	Error       string `json:"error,omitempty"`
}

type groupResult struct {
	Tag     string       `json:"tag"`
	Hashes  []string     `json:"hashes,omitempty"`
	BatchID string       `json:"batch_id,omitempty"`
	Status  string       `json:"status"`
	Phase   string       `json:"phase,omitempty"`
	Lines   []lineResult `json:"lines,omitempty"`
	Error   string       `json:"error,omitempty"`
}

func prepareCoordinate(step workflow.Step, in map[string]any) (workflow.Prepared, error) {
	if err := step.DMS().RequireScope(step.Context(), step.Author(), "organization.manage"); err != nil {
		return workflow.Prepared{}, err
	}
	tools := step.Tools()
	if runtime(tools).Batch == nil {
		return workflow.Prepared{}, ErrNoBatch
	}
	queue, err := queueName(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	policy := policyBoth
	if err = step.Choice(in, "policy", &policy, workflow.AdmissionPolicies()); err != nil {
		return workflow.Prepared{}, err
	}
	cronExpr, err := step.OptionalText(in, "flush_cron")
	if err != nil {
		return workflow.Prepared{}, err
	}
	if cronExpr == "" {
		cronExpr = "0 1 * * *"
	}
	zone, err := step.OptionalText(in, "time_zone")
	if err != nil {
		return workflow.Prepared{}, err
	}
	if zone == "" {
		zone = "Europe/Madrid"
	}
	now := tools.Clock()
	store := tools.Records.WithTx(step.DMS().SQLTx())
	if err = store.PutImmutable(step.Context(), activitystore.Record{Namespace: kindState, Scope: answerScope, Key: queue, OwnerID: step.WorkflowID(), Payload: json.RawMessage(`{}`)}); err != nil && !errors.Is(err, activitystore.ErrConflict) {
		return workflow.Prepared{}, err
	}
	token, claimed, err := store.Claim(step.Context(), kindState, answerScope, queue, step.RunID(), time.Hour)
	if err != nil {
		return workflow.Prepared{}, err
	}
	if !claimed {
		return workflow.Prepared{Output: map[string]any{"queue": queue, "busy": true}}, nil
	}
	record, err := store.Get(step.Context(), kindState, answerScope, queue)
	if err != nil {
		return workflow.Prepared{}, err
	}
	state, err := decodeState(record.Payload)
	if err != nil {
		return workflow.Prepared{}, err
	}
	if state.Cron != cronExpr || state.TimeZone != zone {
		state.NextFlush = time.Time{}
		state.Drain = false
	}
	batchSize := 20
	if err = step.Whole(in, "batch_size", &batchSize, 1, 100); err != nil {
		return workflow.Prepared{}, err
	}
	requests, err := listRequests(step, queue)
	if err != nil {
		return workflow.Prepared{}, err
	}
	flights, err := listFlights(step, queue)
	if err != nil {
		return workflow.Prepared{}, err
	}
	plan, err := admit(now, queue, policy, runtime(tools).Batch.Connection(), runtime(tools).Batch.Key(), cronExpr, zone, requests, flights, flushState{NextFlush: state.NextFlush, Drain: state.Drain}, batchSize)
	if err != nil {
		return workflow.Prepared{}, err
	}
	completed := 0
	for _, key := range plan.Complete {
		wait, err := workflow.GetWait(step.Context(), step.DMS(), kindRequest, key)
		if err != nil {
			return workflow.Prepared{}, err
		}
		var data requestData
		if err = json.Unmarshal(wait.Data, &data); err != nil {
			return workflow.Prepared{}, err
		}
		answer, found, err := findAnswerStep(step.Context(), step.DMS().SQLTx(), tools, data.Activity, data.Hash)
		if err != nil || !found {
			if err == nil {
				err = errors.New("the cached answer disappeared")
			}
			return workflow.Prepared{}, err
		}
		payload, err := json.Marshal(answer.output(data.Model, true))
		if err != nil {
			return workflow.Prepared{}, err
		}
		if err = workflow.CompleteWait(step.Context(), step.DMS(), kindRequest, key, payload); err != nil && !errors.Is(err, dms.ErrConflict) {
			return workflow.Prepared{}, err
		}
		completed++
	}
	if err = resolveJoins(step, tools, queue); err != nil {
		return workflow.Prepared{}, err
	}
	drain := state.Drain || plan.FlushSet && !state.NextFlush.IsZero() && !state.NextFlush.After(now)
	if len(plan.Groups) > 1 {
		plan.Groups = plan.Groups[:1]
	} else {
		drain = false
	}
	if len(plan.Poll) > 1 {
		plan.Poll = plan.Poll[:1]
	}
	if len(plan.Reconcile) > 1 {
		plan.Reconcile = plan.Reconcile[:1]
	}
	next := state.NextFlush
	if plan.FlushSet {
		next = plan.NextFlush
	}
	rawState, err := json.Marshal(stateData{NextFlush: next, Cron: cronExpr, TimeZone: zone, Drain: drain})
	if err != nil {
		return workflow.Prepared{}, err
	}
	if err = store.Put(step.Context(), activitystore.Record{Namespace: kindState, Scope: answerScope, Key: queue, OwnerID: step.WorkflowID(), Payload: rawState}); err != nil {
		return workflow.Prepared{}, err
	}
	tries := map[string]int{}
	for _, req := range requests {
		if req.Attempts > tries[req.Hash] {
			tries[req.Hash] = req.Attempts
		}
	}
	groups := make([]groupSubmit, 0, len(plan.Groups))
	for _, hashes := range plan.Groups {
		group := groupSubmit{Hashes: hashes, Queue: queue}
		for _, hash := range hashes {
			if tries[hash] > group.Tries {
				group.Tries = tries[hash]
			}
		}
		groups = append(groups, group)
		tag := submissionTag(runtime(tools).Batch.Connection(), runtime(tools).Batch.Model(), step.ExecutionID(), group.Tries, hashes)
		groups[len(groups)-1].Tag = tag
		if err = saveFlight(step, coordinateJob{Queue: queue, Connection: runtime(tools).Batch.Connection(), Model: runtime(tools).Batch.Key()}, tag, flightData{Tries: group.Tries, Status: "prepared", Hashes: hashes, Queue: queue, Connection: runtime(tools).Batch.Connection(), Model: runtime(tools).Batch.Key()}); err != nil {
			return workflow.Prepared{}, err
		}
		for _, hash := range hashes {
			if err = store.Reference(step.Context(), activitystore.ID(kindFlight, flightScope(queue, runtime(tools).Batch.Connection(), runtime(tools).Batch.Key()), tag), activitystore.ID(requestCache, answerScope, hash)); err != nil {
				return workflow.Prepared{}, err
			}
		}

	}
	until := time.Now().UTC().Add(24 * time.Hour)
	resultID := activitystore.ID("ai.coordinate.result", queue, step.ExecutionID())
	if err = store.Put(step.Context(), activitystore.Record{Namespace: "ai.coordinate.result", Scope: queue, Key: step.ExecutionID(), Payload: json.RawMessage(`{}`), ExpiresAt: &until}); err != nil {
		return workflow.Prepared{}, err
	}
	if err = store.Reference(step.Context(), step.RunID(), resultID); err != nil {
		return workflow.Prepared{}, err
	}
	job, err := json.Marshal(coordinateJob{
		Queue: queue, ResultKey: step.ExecutionID(), StateToken: token, Connection: runtime(tools).Batch.Connection(), Model: runtime(tools).Batch.Key(),
		Groups: groups, Reconcile: plan.Reconcile, Poll: plan.Poll, Completed: completed,
	})
	if err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Job: job, Output: map[string]any{"queue": queue, "completed": completed}}, nil
}

func listRequests(step workflow.Step, queue string) ([]queuedRequest, error) {
	waits, err := listKind(step, kindRequest, queue)
	if err != nil {
		return nil, err
	}
	sort.Slice(waits, func(i, j int) bool {
		if waits[i].CreatedAt.Equal(waits[j].CreatedAt) {
			return waits[i].ID < waits[j].ID
		}
		return waits[i].CreatedAt.Before(waits[j].CreatedAt)
	})
	now := step.Tools().Clock()
	var out []queuedRequest
	for _, wait := range waits {
		var data requestData
		if err = json.Unmarshal(wait.Data, &data); err != nil {
			return nil, err
		}
		if wait.Deadline != nil && !wait.Deadline.After(now) {
			if err = workflow.FailWait(step.Context(), step.DMS(), kindRequest, wait.Key, "the wait reached its deadline"); err != nil && !errors.Is(err, dms.ErrConflict) {
				return nil, err
			}
			continue
		}
		if err = authorizeRequest(step, wait); err != nil {
			if !errors.Is(err, dms.ErrForbidden) && !errors.Is(err, dms.ErrNotFound) {
				return nil, err
			}
			if err = workflow.FailWait(step.Context(), step.DMS(), kindRequest, wait.Key, "the request author can no longer read its source"); err != nil {
				return nil, err
			}
			continue
		}
		cached := false
		if _, cached, err = findAnswerStep(step.Context(), step.DMS().SQLTx(), step.Tools(), data.Activity, data.Hash); err != nil {
			return nil, err
		}
		out = append(out, queuedRequest{Key: wait.Key, Hash: data.Hash, Queue: data.Queue, Connection: data.Connection, Model: data.Model, Cached: cached, Attempts: data.Attempts})
	}
	return out, nil
}

// Dispatch preserves each originating version author's authority, even when
// an installation coordinator has broader permissions.
func authorizeRequest(step workflow.Step, wait workflow.Wait) error {
	t, ctx := step.DMS(), step.Context()
	authority, err := t.DAL.Waits().Authority(ctx, wait.ID)
	if err != nil {
		return err
	}
	if _, err = t.Authorize(ctx, authority.Author, authority.WorkspaceID, "read"); err != nil {
		return err
	}
	for _, source := range authority.Sources {
		if _, err = t.GetBlob(ctx, authority.Author, source.ItemID, source.BlobID); err != nil {
			return err
		}
	}
	if authority.ItemID != "" {
		_, err = t.GetSurface(ctx, authority.Author, authority.ItemID, "auto")
	}

	return err
}

func flightScope(queue, connection, model string) string {
	return ai.CacheKey(queue, connection, model)
}
func listFlights(step workflow.Step, queue string) ([]openFlight, error) {
	tools := step.Tools()
	scope := flightScope(queue, runtime(tools).Batch.Connection(), runtime(tools).Batch.Key())
	store := tools.Records.WithTx(step.DMS().SQLTx())
	waits, err := listKind(step, kindRequest, queue)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for _, wait := range waits {
		var data requestData
		if err := json.Unmarshal(wait.Data, &data); err != nil {
			return nil, err
		}
		active[data.Hash] = true
	}
	after := ""
	var out []openFlight
	for {
		records, err := store.List(step.Context(), kindFlight, scope, "pending", after, 100)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			var data flightData
			if err = json.Unmarshal(record.Payload, &data); err != nil {
				return nil, err
			}
			live := false
			for _, hash := range data.Hashes {
				live = live || active[hash]
			}
			if !live {
				if err = saveFlight(step, coordinateJob{Queue: queue, Connection: runtime(tools).Batch.Connection(), Model: runtime(tools).Batch.Key()}, record.Key, flightData{Status: "done", BatchID: data.BatchID, Queue: queue, Connection: data.Connection, Model: data.Model}); err != nil {
					return nil, err
				}
				after = record.Key
				continue
			}
			out = append(out, openFlight{Tag: record.Key, Status: data.Status, BatchID: data.BatchID, Hashes: data.Hashes, NextPoll: data.NextPoll})
			after = record.Key
		}
	}
	return out, nil
}

func listKind(step workflow.Step, kind, queue string) ([]workflow.Wait, error) {
	var out []workflow.Wait
	after := ""
	for {
		listed, err := workflow.ListOpenWaits(step.Context(), step.DMS(), workflow.WaitQuery{Kind: kind, Queue: queue, After: after, Limit: 100})
		if err != nil {
			return nil, err
		}
		out = append(out, listed.Data...)
		if listed.NextCursor == "" {
			break
		}
		after = listed.NextCursor
	}
	return out, nil
}

func decodeState(raw json.RawMessage) (stateData, error) {
	var state stateData
	if len(raw) == 0 {
		return state, nil
	}
	err := json.Unmarshal(raw, &state)
	return state, err
}

func workCoordinate(ctx context.Context, tools workflow.Tools, raw json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var job coordinateJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	var groups []groupResult
	for _, tag := range job.Reconcile {
		groups = append(groups, reconcileGroup(ctx, tools, job, tag))
	}
	for _, poll := range job.Poll {
		groups = append(groups, pollGroup(ctx, tools, poll))
	}
	for _, group := range job.Groups {
		groups = append(groups, submitGroup(ctx, tools, job.Connection, group))
	}
	payload, err := json.Marshal(struct {
		Groups []groupResult `json:"groups"`
	}{Groups: groups})
	if err != nil {
		return nil, err
	}
	until := time.Now().UTC().Add(24 * time.Hour)
	if err = tools.Records.Put(ctx, activitystore.Record{Namespace: "ai.coordinate.result", Scope: job.Queue, Key: job.ResultKey, Payload: payload, ExpiresAt: &until}); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"result_key": job.ResultKey})
}

func reconcileGroup(ctx context.Context, tools workflow.Tools, job coordinateJob, tag string) groupResult {
	if runtime(tools).Batch == nil {
		return groupResult{Tag: tag, Status: "error", Error: ErrNoBatch.Error()}
	}
	record, e := tools.Records.Get(ctx, kindFlight, flightScope(job.Queue, job.Connection, job.Model), tag)
	if e != nil {
		return groupResult{Tag: tag, Status: "uncertain", Error: e.Error()}
	}
	if record != nil {
		var data flightData
		if json.Unmarshal(record.Payload, &data) == nil && data.Status == "prepared" {
			return submitGroup(ctx, tools, job.Connection, groupSubmit{Tag: tag, Queue: job.Queue, Hashes: data.Hashes, Tries: data.Tries})
		}
	}
	id, found, err := runtime(tools).Batch.FindByTag(ctx, tag)
	if err != nil {
		return groupResult{Tag: tag, Status: "uncertain", Error: err.Error()}
	}
	if !found {
		return groupResult{Tag: tag, Status: "uncertain", Phase: "reconcile", Error: "the provider has not confirmed the submission"}
	}
	result := finishBatch(ctx, tools, tag, nil, id)
	result.Phase = "reconcile"
	return result
}

func pollGroup(ctx context.Context, tools workflow.Tools, poll pollTarget) groupResult {
	if runtime(tools).Batch == nil {
		return groupResult{Tag: poll.Tag, Status: "error", Phase: "poll", Error: ErrNoBatch.Error()}
	}
	if poll.BatchID == "" {
		return groupResult{Tag: poll.Tag, Status: "in_progress", Phase: "poll", Error: "the submitted batch has no id"}
	}
	result := finishBatch(ctx, tools, poll.Tag, nil, poll.BatchID)
	result.Phase = "poll"
	return result
}

func submitGroup(ctx context.Context, tools workflow.Tools, connection string, group groupSubmit) groupResult {
	hashes := group.Hashes
	if runtime(tools).Batch == nil {
		return groupResult{Hashes: hashes, Status: "error", Error: ErrNoBatch.Error()}
	}
	tag := group.Tag
	if tag == "" {
		return groupResult{Hashes: hashes, Status: "error", Error: "the submission has no durable intent"}
	}
	scope := flightScope(group.Queue, connection, runtime(tools).Batch.Key())
	record, err := tools.Records.Get(ctx, kindFlight, scope, tag)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Error: err.Error()}
	}
	if record == nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Error: "the submission intent is unavailable"}
	}
	var data flightData
	if err = json.Unmarshal(record.Payload, &data); err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Error: err.Error()}
	}
	if data.Status != "prepared" {
		// Only a prepared intent proves Create has never been authorized.
		// All other states require reconciliation, never another create.
		return reconcileGroup(ctx, tools, coordinateJob{Queue: group.Queue, Connection: connection, Model: runtime(tools).Batch.Key()}, tag)
	}
	payload, err := jsonl(ctx, tools, hashes)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "error", Phase: "submit", Error: err.Error()}
	}
	fileID, err := runtime(tools).Batch.Upload(ctx, payload)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "error", Phase: "submit", Error: err.Error()}
	}
	intent, _ := json.Marshal(flightData{Status: "uncertain", Hashes: hashes, Queue: group.Queue, Connection: connection, Model: runtime(tools).Batch.Key()})
	swapped, err := tools.Records.CompareAndSwap(ctx, record.ID, record.Payload, intent)
	if err != nil || !swapped {
		if err == nil {
			err = errors.New("another executor advanced the submission intent")
		}
		return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Error: err.Error()}
	}
	id, err := runtime(tools).Batch.Create(ctx, fileID, tag)
	if err != nil {
		if errors.Is(err, ai.ErrUncertain) {
			return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Phase: "submit", Error: err.Error()}
		}
		return groupResult{Tag: tag, Hashes: hashes, Status: "error", Phase: "submit", Error: err.Error()}
	}
	result := finishBatch(ctx, tools, tag, hashes, id)
	result.Phase = "submit"
	return result
}

func finishBatch(ctx context.Context, tools workflow.Tools, tag string, hashes []string, id string) groupResult {
	batch, err := runtime(tools).Batch.Get(ctx, id)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress", Error: err.Error()}
	}
	if !batch.Terminal() {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress"}
	}
	lines, err := batchLines(ctx, tools, batch, tag)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress", Error: err.Error()}
	}
	return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: batch.Status, Lines: lines}
}

func submissionTag(connection, model, invocation string, tries int, hashes []string) string {
	sorted := append([]string(nil), hashes...)
	sort.Strings(sorted)
	parts := append([]string{model, invocation, strconv.Itoa(tries)}, sorted...)
	return ai.CacheKey(connection, parts...)
}

func jsonl(ctx context.Context, tools workflow.Tools, hashes []string) ([]byte, error) {
	var buf bytes.Buffer
	for _, hash := range hashes {
		body, err := loadRequest(ctx, tools, hash)
		if err != nil {
			return nil, err
		}
		input := body.Input
		if body.ObjectKey != "" {
			data, err := readObject(ctx, tools, body.ObjectKey)
			if err != nil {
				return nil, err
			}
			var stored storedText
			if err = json.Unmarshal(data, &stored); err != nil {
				return nil, fmt.Errorf("the stored text is unreadable: %w", err)
			}
			texts := make([]string, len(stored.Pages))
			for i, page := range stored.Pages {
				texts[i] = page.Text
			}
			limit := body.MaxChars
			if limit <= 0 {
				limit = defaultInputChars
			}
			input = shortText(strings.Join(texts, "\n\n"), limit)
		}
		images, err := loadImages(ctx, tools, body.Images)
		if err != nil {
			return nil, err
		}
		line, err := json.Marshal(map[string]any{
			"custom_id": hash,
			"method":    "POST",
			"url":       "/v1/chat/completions",
			"body":      ai.CompletionBody(body.Model, ai.Completion{Instructions: body.Prompt, Input: input, Schema: body.Schema, Images: images}),
		})
		if err != nil {
			return nil, err
		}
		if buf.Len()+len(line)+1 > 100<<20 {
			return nil, errors.New("batch input exceeds the 100 MiB action limit; reduce batch_size or source size")
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func batchLines(ctx context.Context, tools workflow.Tools, batch ai.Batch, tag string) ([]lineResult, error) {
	var lines []lineResult
	if batch.OutputFileID != "" {
		raw, err := runtime(tools).Batch.Download(ctx, batch.OutputFileID)
		if err != nil {
			return nil, err
		}
		parsed, err := parseBatchFile(ctx, tools, raw, tag)
		if err != nil {
			return nil, err
		}
		lines = append(lines, parsed...)
	}
	if batch.ErrorFileID != "" {
		raw, err := runtime(tools).Batch.Download(ctx, batch.ErrorFileID)
		if err != nil {
			return nil, err
		}
		parsed, err := parseBatchFile(ctx, tools, raw, tag)
		if err != nil {
			return nil, err
		}
		lines = append(lines, parsed...)
	}
	return lines, nil
}

func parseBatchFile(ctx context.Context, tools workflow.Tools, raw []byte, tag string) ([]lineResult, error) {
	var lines []lineResult
	for _, row := range bytes.Split(raw, []byte("\n")) {
		row = bytes.TrimSpace(row)
		if len(row) == 0 {
			continue
		}
		var line struct {
			CustomID string `json:"custom_id"`
			Response struct {
				StatusCode int             `json:"status_code"`
				Body       json.RawMessage `json:"body"`
			} `json:"response"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(row, &line); err != nil || line.CustomID == "" {
			continue
		}
		if line.Error != nil && line.Error.Message != "" && line.Response.StatusCode == 0 {
			lines = append(lines, lineResult{Hash: line.CustomID, Error: shortText(line.Error.Message, 300)})
			continue
		}
		if line.Response.StatusCode != 200 {
			message := "the provider rejected the request"
			if line.Error != nil && line.Error.Message != "" {
				message = line.Error.Message
			}
			lines = append(lines, lineResult{Hash: line.CustomID, Error: shortText(message, 300)})
			continue
		}
		body, err := loadRequest(ctx, tools, line.CustomID)
		if err != nil {
			lines = append(lines, lineResult{Hash: line.CustomID, Terminal: true, Error: err.Error()})
			continue
		}
		answer, err := ai.ParseCompletion(line.Response.Body, body.Schema != nil)
		if err != nil {
			// An empty or unreadable line can be sent again. A schema or size
			// rejection is decided below and is terminal.
			lines = append(lines, lineResult{Hash: line.CustomID, Error: err.Error()})
			continue
		}
		if body.Activity == "search.enrich" {
			answer.Text = shortText(answer.Text, maxEnrichmentChars)
		}
		accepted, e := acceptAnswer(ctx, tools, body.Activity, line.CustomID, body.ModelKey, body.Schema, answer)
		err = e
		if err != nil {
			terminal := strings.Contains(err.Error(), "output_schema") || strings.Contains(err.Error(), "workflow output limit")
			lines = append(lines, lineResult{Hash: line.CustomID, Terminal: terminal, Error: err.Error()})
			continue
		}
		responseKey := ai.CacheKey(tag, line.CustomID)
		if err = saveResponse(ctx, tools, line.CustomID, responseKey, accepted); err != nil {
			return nil, err
		}
		lines = append(lines, lineResult{Hash: line.CustomID, ResponseKey: responseKey, OK: true})
	}
	return lines, nil
}

func commitCoordinate(step workflow.Step, rawJob, rawResult json.RawMessage) (workflow.Prepared, error) {
	var job coordinateJob
	var result struct {
		Groups []groupResult `json:"groups"`
	}
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return workflow.Prepared{}, err
	}
	store := step.Tools().Records.WithTx(step.DMS().SQLTx())
	if err := store.CheckLease(step.Context(), kindState, answerScope, job.Queue, step.RunID(), job.StateToken); err != nil {
		return workflow.Prepared{}, err
	}
	record, err := store.Get(step.Context(), "ai.coordinate.result", job.Queue, job.ResultKey)
	if err != nil {
		return workflow.Prepared{}, err
	}
	if record == nil {
		return workflow.Prepared{}, errors.New("coordinator work result is unavailable")
	}
	if err = json.Unmarshal(record.Payload, &result); err != nil {
		return workflow.Prepared{}, err
	}
	if err = store.Unreference(step.Context(), step.RunID(), record.ID); err != nil {
		return workflow.Prepared{}, err
	}
	counts := map[string]int{"submitted": 0, "polled": 0, "completed": job.Completed, "released": 0}
	for _, group := range result.Groups {
		if err := applyGroup(step, job, group, counts); err != nil {
			return workflow.Prepared{}, err
		}
	}
	if err := resolveJoins(step, step.Tools(), job.Queue); err != nil {
		return workflow.Prepared{}, err
	}
	if err := store.ReleaseLease(step.Context(), kindState, answerScope, job.Queue, step.RunID(), job.StateToken); err != nil && !notFound(err) {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Output: map[string]any{
		"queue": job.Queue, "submitted": counts["submitted"], "polled": counts["polled"],
		"completed": counts["completed"], "released": counts["released"],
	}}, nil
}

func applyGroup(step workflow.Step, job coordinateJob, group groupResult, counts map[string]int) error {
	hashes := group.Hashes
	if len(hashes) == 0 && group.Tag != "" {
		store := step.Tools().Records.WithTx(step.DMS().SQLTx())
		if flight, err := store.Get(step.Context(), kindFlight, flightScope(job.Queue, job.Connection, job.Model), group.Tag); err == nil && flight != nil {
			var data flightData
			if json.Unmarshal(flight.Payload, &data) == nil {
				hashes = data.Hashes
			}
		}
	}
	switch group.Status {
	case "uncertain":
		next := time.Time{}
		if group.Phase == "reconcile" {
			next = step.Tools().Clock().Add(pollEvery)
		}
		return saveFlight(step, job, group.Tag, flightData{Status: "uncertain", NextPoll: next, Hashes: hashes, Queue: job.Queue, Connection: job.Connection, Model: job.Model})
	case "error":
		n, err := releaseHashes(step, hashes, lineResult{Error: group.Error})
		counts["released"] += n
		if err != nil {
			return err
		}
		return saveFlight(step, job, group.Tag, flightData{Status: "done", Queue: job.Queue, Connection: job.Connection, Model: job.Model})
	case "in_progress":
		if group.Phase == "submit" {
			counts["submitted"]++
		}
		return saveFlight(step, job, group.Tag, flightData{
			Status: "submitted", BatchID: group.BatchID, Hashes: hashes, NextPoll: step.Tools().Clock().Add(pollEvery),
			Queue: job.Queue, Connection: job.Connection, Model: job.Model,
		})
	default:
		if group.Phase == "submit" {
			counts["submitted"]++
		}
		counts["polled"]++
		byHash := map[string]lineResult{}
		for _, line := range group.Lines {
			if prev, ok := byHash[line.Hash]; ok && prev.OK {
				continue
			}
			byHash[line.Hash] = line
		}
		for _, hash := range hashes {
			line, ok := byHash[hash]
			if !ok {
				line = lineResult{Hash: hash, Error: "the provider batch omitted the request"}
			}
			done, released, err := applyLine(step, hash, line)
			if err != nil {
				return err
			}
			counts["completed"] += done
			counts["released"] += released
		}
		return saveFlight(step, job, group.Tag, flightData{
			Status: "done", BatchID: group.BatchID, Queue: job.Queue, Connection: job.Connection, Model: job.Model,
		})
	}
}

func applyLine(step workflow.Step, hash string, line lineResult) (completed, released int, err error) {
	waits, err := waitsForHash(step, hash)
	if err != nil {
		return 0, 0, err
	}
	for _, wait := range waits {
		var data requestData
		if err = json.Unmarshal(wait.Data, &data); err != nil {
			return completed, released, err
		}
		if line.OK {
			answer, err := responseStep(step, line.ResponseKey)
			if err != nil {
				return completed, released, err
			}
			payload, err := json.Marshal(answer.output(data.Model, false))
			if err != nil {
				return completed, released, err
			}
			if err = workflow.CompleteWait(step.Context(), step.DMS(), kindRequest, wait.Key, payload); err != nil && !errors.Is(err, dms.ErrConflict) {
				return completed, released, err
			}
			completed++
			continue
		}
		message := line.Error
		if message == "" {
			message = "the provider request failed"
		}
		next := data.Attempts + 1
		if line.Terminal || next >= maxAttempts {
			if err = workflow.FailWait(step.Context(), step.DMS(), kindRequest, wait.Key, shortText(message, 300)); err != nil && !errors.Is(err, dms.ErrConflict) {
				return completed, released, err
			}
			completed++
			continue
		}
		data.Attempts = next
		raw, err := json.Marshal(data)
		if err != nil {
			return completed, released, err
		}
		if err = workflow.UpdateWaitData(step.Context(), step.DMS(), workflow.WaitUpdate{Kind: kindRequest, Key: wait.Key, Holder: wait.RunID, Data: raw}); err != nil {
			return completed, released, err
		}

		released++
	}
	return completed, released, nil
}

func releaseHashes(step workflow.Step, hashes []string, line lineResult) (int, error) {
	n := 0
	for _, hash := range hashes {
		_, released, err := applyLine(step, hash, line)
		if err != nil {
			return n, err
		}
		n += released
	}
	return n, nil
}

func waitsForHash(step workflow.Step, hash string) ([]workflow.Wait, error) {
	waits, err := listKind(step, kindRequest, "")
	if err != nil {
		return nil, err
	}
	var out []workflow.Wait
	for _, wait := range waits {
		var data requestData
		if json.Unmarshal(wait.Data, &data) != nil || data.Hash != hash {
			continue
		}
		out = append(out, wait)
	}
	return out, nil
}

func saveFlight(step workflow.Step, job coordinateJob, tag string, data flightData) error {
	if tag == "" {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	scope := flightScope(job.Queue, job.Connection, job.Model)
	// Pending intents outlive every consumer deadline, but are still
	// collected if this coordinator is disabled or its provider changes.
	until := step.Tools().Clock().UTC().Add(requestTTL(step.Tools()) + 24*time.Hour)
	record := activitystore.Record{Namespace: kindFlight, Scope: scope, Key: tag, OwnerID: step.WorkflowID(), Payload: raw, ExpiresAt: &until}
	if data.Status == "done" || data.Status == "cleared" {
		record.State = "completed"
		until := time.Now().UTC().Add(step.Tools().ResultTTL)
		record.ExpiresAt = &until
	}
	store := step.Tools().Records.WithTx(step.DMS().SQLTx())
	if err = store.Put(step.Context(), record); err != nil {
		return err
	}
	if record.State == "completed" {
		return store.Release(step.Context(), activitystore.ID(kindFlight, scope, tag))
	}
	return nil
}

func resolveJoins(step workflow.Step, tools workflow.Tools, queue string) error {
	if tools.Search == nil || tools.Results == nil {
		return nil
	}
	joins, err := listKind(step, kindJoin, queue)
	if err != nil {
		return err
	}
	for _, join := range joins {
		var data joinData
		if err = json.Unmarshal(join.Data, &data); err != nil {
			return err
		}
		raw, err := loadManifest(step, data.Manifest)
		if err != nil {
			return err
		}
		if raw == nil {
			if err = workflow.FailWait(step.Context(), step.DMS(), kindJoin, join.Key, "the enrichment request expired"); err != nil && !errors.Is(err, dms.ErrConflict) {
				return err
			}
			continue
		}
		var manifest enrichManifest
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return err
		}
		pending, failed := 0, ""
		cached := 0
		for i, chunk := range manifest.Chunks {
			if chunk.Cached {
				cached++
				continue
			}
			wait, err := workflow.GetWait(step.Context(), step.DMS(), kindRequest, chunk.WaitKey)
			if notFound(err) || err == nil && wait.Status == waitstore.StatusClosed {
				failed = "the enrichment request was closed"
				continue
			}
			if err != nil {
				return err
			}
			if wait.Status == waitstore.StatusOpen {
				pending++
				continue
			}
			var c workflow.Completion
			if err = json.Unmarshal(wait.Completion, &c); err != nil {
				return err
			}
			if c.Error != "" {
				failed = c.Error
				continue
			}
			text, ok := c.Output["text"].(string)
			if !ok {
				failed = "the enrichment completion has no text"
				continue
			}
			manifest.Chunks[i].Answer = text
		}

		if pending > 0 {
			continue
		}
		// Child completions have been copied into the manifest. Their
		// request payloads no longer need to outlive this join.
		for _, chunk := range manifest.Chunks {
			if chunk.WaitKey == "" {
				continue
			}
			if err = workflow.ConsumeWait(step.Context(), step.DMS(), kindRequest, chunk.WaitKey, join.RunID); err != nil && !notFound(err) {
				return err
			}
		}
		if failed != "" {
			if err = workflow.FailWait(step.Context(), step.DMS(), kindJoin, join.Key, failed); err != nil && !errors.Is(err, dms.ErrConflict) {
				return err
			}
			continue
		}
		answersKey := data.Manifest + ":answers"
		answers, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		if err = putManifest(step, answersKey, answers); err != nil {
			return err
		}
		if err = tools.Records.WithTx(step.DMS().SQLTx()).Reference(step.Context(), join.ID, activitystore.ID("ai.enrich.manifest", answerScope, answersKey)); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"manifest": answersKey, "generation": manifest.Generation, "enriched": len(manifest.Chunks), "cached": cached, "model": manifest.Model,
		})
		if err != nil {
			return err
		}
		if err = workflow.CompleteWait(step.Context(), step.DMS(), kindJoin, join.Key, payload); err != nil && !errors.Is(err, dms.ErrConflict) {
			return err
		}
	}
	return nil
}
