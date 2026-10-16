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
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/search"
	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key:         "ai.batch.coordinate",
		Kind:        "action",
		Description: "Admits the installation's parked AI requests for one named queue: full groups of 20, a daily partial flush, and provider polling. Originating workflows keep their own authors. The output is counts only.",
		InputSchema: inputSchema(`
			"queue": {"type": "string", "title": "Queue", "minLength": 1, "maxLength": 64, "description": "Named queue. Empty uses shared."},
			"policy": {"type": "string", "title": "Admission", "enum": ["threshold", "schedule", "both"], "description": "threshold submits full groups. schedule submits on the flush, including a partial group. both does both."},
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
}

type flightData struct {
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
	Hashes []string `json:"hashes"`
	Tries  int      `json:"tries"`
}

type coordinateJob struct {
	Queue      string        `json:"queue"`
	StateToken string        `json:"state_token"`
	Connection string        `json:"connection"`
	Model      string        `json:"model"`
	Groups     []groupSubmit `json:"groups,omitempty"`
	Reconcile  []string      `json:"reconcile,omitempty"`
	Poll       []pollTarget  `json:"poll,omitempty"`
	Completed  int           `json:"completed,omitempty"`
}

type lineResult struct {
	Hash     string `json:"hash"`
	OK       bool   `json:"ok,omitempty"`
	Terminal bool   `json:"terminal,omitempty"`
	Error    string `json:"error,omitempty"`
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
	if tools.Batch == nil {
		return workflow.Prepared{}, ErrNoBatch
	}
	queue, err := queueName(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	policy := policyBoth
	if err = step.Choice(in, "policy", &policy, []string{policyThreshold, policySchedule, policyBoth}); err != nil {
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
	if err = workflow.PutWait(step.Context(), step.DMS(), workflow.Wait{
		Kind: kindState, Key: queue, WorkspaceID: step.WorkspaceID(), RunID: batchOwner(queue),
		Node: step.Node(), ExecutionID: "state", Data: json.RawMessage(`{}`),
	}); err != nil {
		return workflow.Prepared{}, err
	}
	claimed, token, err := workflow.ClaimWaits(step.Context(), step.DMS(), workflow.WaitClaim{
		Kind: kindState, Key: queue, Holder: step.RunID(), Limit: 1,
	})
	if err != nil {
		return workflow.Prepared{}, err
	}
	if len(claimed) == 0 {
		return workflow.Prepared{Output: map[string]any{"queue": queue, "busy": true}}, nil
	}
	state, err := decodeState(claimed[0].Data)
	if err != nil {
		return workflow.Prepared{}, err
	}
	requests, err := listRequests(step)
	if err != nil {
		return workflow.Prepared{}, err
	}
	flights, err := listFlights(step)
	if err != nil {
		return workflow.Prepared{}, err
	}
	plan, err := admit(now, queue, policy, tools.Batch.Connection(), tools.Batch.Key(), cronExpr, zone, requests, flights, flushState{NextFlush: state.NextFlush})
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
		answer, found, err := findAnswerStep(step.Context(), step.DMS().Client, tools, data.Activity, data.Hash)
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
	if err = resolveJoins(step, tools); err != nil {
		return workflow.Prepared{}, err
	}
	if plan.FlushSet {
		raw, err := json.Marshal(stateData{NextFlush: plan.NextFlush, Cron: cronExpr, TimeZone: zone})
		if err != nil {
			return workflow.Prepared{}, err
		}
		if err = workflow.UpdateWaitData(step.Context(), step.DMS(), workflow.WaitUpdate{
			Kind: kindState, Key: queue, Holder: batchOwner(queue), Data: raw,
		}); err != nil {
			return workflow.Prepared{}, err
		}
	}
	tries := map[string]int{}
	for _, req := range requests {
		if req.Attempts > tries[req.Hash] {
			tries[req.Hash] = req.Attempts
		}
	}
	groups := make([]groupSubmit, 0, len(plan.Groups))
	for _, hashes := range plan.Groups {
		group := groupSubmit{Hashes: hashes}
		for _, hash := range hashes {
			if tries[hash] > group.Tries {
				group.Tries = tries[hash]
			}
		}
		groups = append(groups, group)
	}
	job, err := json.Marshal(coordinateJob{
		Queue: queue, StateToken: token, Connection: tools.Batch.Connection(), Model: tools.Batch.Key(),
		Groups: groups, Reconcile: plan.Reconcile, Poll: plan.Poll, Completed: completed,
	})
	if err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Job: job, Output: map[string]any{"queue": queue, "completed": completed}}, nil
}

func listRequests(step workflow.Step) ([]queuedRequest, error) {
	waits, err := listKind(step, kindRequest)
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
			if err = workflow.CompleteWait(step.Context(), step.DMS(), kindRequest, wait.Key, json.RawMessage(`{"error":"the wait reached its deadline"}`)); err != nil && !errors.Is(err, dms.ErrConflict) {
				return nil, err
			}
			continue
		}
		cached := false
		if _, cached, err = findAnswerStep(step.Context(), step.DMS().Client, step.Tools(), data.Activity, data.Hash); err != nil {
			return nil, err
		}
		out = append(out, queuedRequest{Key: wait.Key, Hash: data.Hash, Queue: data.Queue, Connection: data.Connection, Model: data.Model, Cached: cached, Attempts: data.Attempts})
	}
	return out, nil
}

func listFlights(step workflow.Step) ([]openFlight, error) {
	waits, err := listKind(step, kindFlight)
	if err != nil {
		return nil, err
	}
	var out []openFlight
	for _, wait := range waits {
		var data flightData
		if err = json.Unmarshal(wait.Data, &data); err != nil {
			return nil, err
		}
		out = append(out, openFlight{Tag: wait.Key, Status: data.Status, BatchID: data.BatchID, Hashes: data.Hashes, NextPoll: data.NextPoll})
	}
	return out, nil
}

func listKind(step workflow.Step, kind string) ([]workflow.Wait, error) {
	var out []workflow.Wait
	after := ""
	for page := 0; page < 10; page++ {
		listed, err := workflow.ListOpenWaits(step.Context(), step.DMS(), workflow.WaitQuery{Kind: kind, After: after, Limit: 100})
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
	var job coordinateJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	var groups []groupResult
	for _, tag := range job.Reconcile {
		groups = append(groups, reconcileGroup(ctx, tools, tag))
	}
	for _, poll := range job.Poll {
		groups = append(groups, pollGroup(ctx, tools, poll))
	}
	for _, group := range job.Groups {
		groups = append(groups, submitGroup(ctx, tools, job.Connection, group))
	}
	return json.Marshal(struct {
		Groups []groupResult `json:"groups"`
	}{Groups: groups})
}

func reconcileGroup(ctx context.Context, tools workflow.Tools, tag string) groupResult {
	if tools.Batch == nil {
		return groupResult{Tag: tag, Status: "error", Error: ErrNoBatch.Error()}
	}
	id, found, err := tools.Batch.FindByTag(ctx, tag)
	if err != nil {
		return groupResult{Tag: tag, Status: "uncertain", Error: err.Error()}
	}
	if !found {
		return groupResult{Tag: tag, Status: "cleared", Phase: "reconcile"}
	}
	result := finishBatch(ctx, tools, tag, nil, id)
	result.Phase = "reconcile"
	return result
}

func pollGroup(ctx context.Context, tools workflow.Tools, poll pollTarget) groupResult {
	if tools.Batch == nil {
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
	if tools.Batch == nil {
		return groupResult{Hashes: hashes, Status: "error", Error: ErrNoBatch.Error()}
	}
	tag := submissionTag(connection, tools.Batch.Model(), group.Tries, hashes)
	id, found, err := tools.Batch.FindByTag(ctx, tag)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "uncertain", Phase: "submit", Error: err.Error()}
	}
	if found {
		result := finishBatch(ctx, tools, tag, hashes, id)
		result.Phase = "submit"
		return result
	}
	payload, err := jsonl(ctx, tools, hashes)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "error", Phase: "submit", Error: err.Error()}
	}
	fileID, err := tools.Batch.Upload(ctx, payload)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, Status: "error", Phase: "submit", Error: err.Error()}
	}
	id, err = tools.Batch.Create(ctx, fileID, tag)
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
	batch, err := tools.Batch.Get(ctx, id)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress", Error: err.Error()}
	}
	if !batch.Terminal() {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress"}
	}
	lines, err := batchLines(ctx, tools, batch)
	if err != nil {
		return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: "in_progress", Error: err.Error()}
	}
	return groupResult{Tag: tag, Hashes: hashes, BatchID: id, Status: batch.Status, Lines: lines}
}

func submissionTag(connection, model string, tries int, hashes []string) string {
	sorted := append([]string(nil), hashes...)
	sort.Strings(sorted)
	parts := append([]string{model, strconv.Itoa(tries)}, sorted...)
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
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func batchLines(ctx context.Context, tools workflow.Tools, batch ai.Batch) ([]lineResult, error) {
	var lines []lineResult
	if batch.OutputFileID != "" {
		raw, err := tools.Batch.Download(ctx, batch.OutputFileID)
		if err != nil {
			return nil, err
		}
		parsed, err := parseBatchFile(ctx, tools, raw)
		if err != nil {
			return nil, err
		}
		lines = append(lines, parsed...)
	}
	if batch.ErrorFileID != "" {
		raw, err := tools.Batch.Download(ctx, batch.ErrorFileID)
		if err != nil {
			return nil, err
		}
		parsed, err := parseBatchFile(ctx, tools, raw)
		if err != nil {
			return nil, err
		}
		lines = append(lines, parsed...)
	}
	return lines, nil
}

func parseBatchFile(ctx context.Context, tools workflow.Tools, raw []byte) ([]lineResult, error) {
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
		if _, err = acceptAnswer(ctx, tools, body.Activity, line.CustomID, body.ModelKey, body.Schema, answer); err != nil {
			terminal := strings.Contains(err.Error(), "output_schema") || strings.Contains(err.Error(), "workflow output limit")
			lines = append(lines, lineResult{Hash: line.CustomID, Terminal: terminal, Error: err.Error()})
			continue
		}
		lines = append(lines, lineResult{Hash: line.CustomID, OK: true})
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
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return workflow.Prepared{}, err
	}
	counts := map[string]int{"submitted": 0, "polled": 0, "completed": job.Completed, "released": 0}
	for _, group := range result.Groups {
		if err := applyGroup(step, job, group, counts); err != nil {
			return workflow.Prepared{}, err
		}
	}
	if err := resolveJoins(step, step.Tools()); err != nil {
		return workflow.Prepared{}, err
	}
	if err := workflow.ReleaseClaim(step.Context(), step.DMS(), kindState, job.Queue, step.RunID(), job.StateToken); err != nil && !notFound(err) {
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
		if flight, err := workflow.GetWait(step.Context(), step.DMS(), kindFlight, group.Tag); err == nil {
			var data flightData
			if json.Unmarshal(flight.Data, &data) == nil {
				hashes = data.Hashes
			}
		}
	}
	switch group.Status {
	case "uncertain":
		return saveFlight(step, job, group.Tag, flightData{Status: "uncertain", Hashes: hashes, Queue: job.Queue, Connection: job.Connection, Model: job.Model})
	case "cleared":
		return saveFlight(step, job, group.Tag, flightData{Status: "cleared", Queue: job.Queue, Connection: job.Connection, Model: job.Model})
	case "error":
		n, err := releaseHashes(step, hashes, lineResult{Error: group.Error})
		counts["released"] += n
		return err
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
			answer, found, err := findAnswerStep(step.Context(), step.DMS().Client, step.Tools(), data.Activity, hash)
			if err != nil {
				return completed, released, err
			}
			if !found {
				line = lineResult{Terminal: true, Error: "the provider answer was not cached"}
			} else {
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
		}
		message := line.Error
		if message == "" {
			message = "the provider request failed"
		}
		next := data.Attempts + 1
		if line.Terminal || next >= maxAttempts {
			payload, err := json.Marshal(map[string]string{"error": shortText(message, 300)})
			if err != nil {
				return completed, released, err
			}
			if err = workflow.CompleteWait(step.Context(), step.DMS(), kindRequest, wait.Key, payload); err != nil && !errors.Is(err, dms.ErrConflict) {
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
		holder := step.RunID()
		token := waitToken(wait)
		if wait.ClaimRunID != nil && token != "" {
			holder = *wait.ClaimRunID
		} else {
			holder = wait.RunID
			token = ""
		}
		if err = workflow.UpdateWaitData(step.Context(), step.DMS(), workflow.WaitUpdate{
			Kind: kindRequest, Key: wait.Key, Holder: holder, ClaimToken: token, Data: raw,
		}); err != nil && !notFound(err) {
			return completed, released, err
		}
		if token != "" && wait.ClaimRunID != nil {
			if err = workflow.ReleaseClaim(step.Context(), step.DMS(), kindRequest, wait.Key, *wait.ClaimRunID, token); err != nil && !notFound(err) {
				return completed, released, err
			}
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
	waits, err := listKind(step, kindRequest)
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
	if err = workflow.PutWait(step.Context(), step.DMS(), workflow.Wait{
		Kind: kindFlight, Key: tag, WorkspaceID: step.WorkspaceID(), RunID: batchOwner(job.Queue),
		Node: step.Node(), ExecutionID: tag, Data: raw,
	}); err != nil {
		return err
	}
	return workflow.UpdateWaitData(step.Context(), step.DMS(), workflow.WaitUpdate{
		Kind: kindFlight, Key: tag, Holder: batchOwner(job.Queue), Data: raw,
	})
}

func resolveJoins(step workflow.Step, tools workflow.Tools) error {
	if tools.Search == nil || tools.Results == nil {
		return nil
	}
	joins, err := listKind(step, kindJoin)
	if err != nil {
		return err
	}
	for _, join := range joins {
		var data joinData
		if err = json.Unmarshal(join.Data, &data); err != nil {
			return err
		}
		raw, err := stepCacheGet(step.Context(), step.DMS().Client, "search.enrich", data.Manifest)
		if err != nil {
			return err
		}
		if raw == nil {
			if err = workflow.CompleteWait(step.Context(), step.DMS(), kindJoin, join.Key, json.RawMessage(`{"error":"the enrichment request expired"}`)); err != nil && !errors.Is(err, dms.ErrConflict) {
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
		var enrichments []search.Enrichment
		for _, chunk := range manifest.Chunks {
			if chunk.Cached {
				cached++
			}
			answer, found, err := findAnswerStep(step.Context(), step.DMS().Client, tools, "search.enrich", chunk.Hash)
			if err != nil {
				return err
			}
			if found {
				enrichments = append(enrichments, search.Enrichment{Part: chunk.Part, Ordinal: chunk.Ordinal, Text: answer.Text})
				continue
			}
			if chunk.WaitKey == "" {
				pending++
				continue
			}
			wait, err := workflow.GetWait(step.Context(), step.DMS(), kindRequest, chunk.WaitKey)
			if notFound(err) || err == nil && wait.Status == "closed" {
				failed = "the enrichment request was closed"
				continue
			}
			if err != nil {
				return err
			}
			if wait.Status == "completed" {
				var payload map[string]any
				if json.Unmarshal(wait.Completion, &payload) == nil {
					if message, _ := payload["error"].(string); message != "" && len(payload) == 1 {
						failed = message
						continue
					}
				}
				failed = "the enrichment answer was not cached"
				continue
			}
			pending++
		}
		if pending > 0 {
			continue
		}
		if failed != "" {
			payload, err := json.Marshal(map[string]string{"error": shortText(failed, 300)})
			if err != nil {
				return err
			}
			if err = workflow.CompleteWait(step.Context(), step.DMS(), kindJoin, join.Key, payload); err != nil && !errors.Is(err, dms.ErrConflict) {
				return err
			}
			continue
		}
		if err = tools.Search.SetEnrichments(step.Context(), manifest.Generation, enrichments); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"generation": manifest.Generation, "enriched": len(enrichments), "cached": cached, "model": manifest.Model,
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
