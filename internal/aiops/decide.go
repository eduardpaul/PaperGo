package aiops

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/operations"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/doctext"
)

// DecisionSource is what a decision is about: text or JSON data given
// directly in a workspace, or the text read from an item's file version,
// optionally with document context.
type DecisionSource struct {
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Input       string         `json:"input,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
	ItemID      string         `json:"item_id,omitempty"`
	TextID      string         `json:"text_id,omitempty"`
	BlobID      string         `json:"blob_id,omitempty"`
	Context     map[string]any `json:"context,omitempty"`
}

// DecisionRequest asks choice, noul or ordinal score questions.
type DecisionRequest struct {
	DecisionSource
	Questions []decision.Question `json:"questions"`
}

// TaxonomyDecisionRequest picks active terms of an organization term set.
// multiple compares each term independently; single makes one joint
// choice.
type TaxonomyDecisionRequest struct {
	DecisionSource
	TermSetID string `json:"term_set_id"`
	// Selection is multiple (default) or single.
	Selection string `json:"selection,omitempty"`
	// Threshold, above 0.5 and at most 1 (default 0.8), includes a term.
	Threshold            float64                `json:"threshold,omitempty"`
	Question             string                 `json:"question,omitempty"`
	OtherText            string                 `json:"other_text,omitempty"`
	RootTermID           string                 `json:"root_term_id,omitempty"`
	TermIDs              []string               `json:"term_ids,omitempty"`
	LeafOnly             bool                   `json:"leaf_only,omitempty"`
	DescriptionOverrides []dms.DecisionOverride `json:"description_overrides,omitempty"`
}

// decisionSource is the item file version a document decision read.
type decisionSource struct {
	ItemID      string `json:"item_id"`
	BlobID      string `json:"blob_id"`
	TextID      string `json:"text_id"`
	ItemVersion int    `json:"item_version"`
}

// decisionJob is a decision operation's payload.
type decisionJob struct {
	Namespace  string                  `json:"namespace"`
	Workspace  string                  `json:"workspace"`
	Model      string                  `json:"model"`
	Key        string                  `json:"key"`
	Serializer string                  `json:"serializer"`
	Input      string                  `json:"input,omitempty"`
	Context    map[string]any          `json:"context,omitempty"`
	HasContext bool                    `json:"has_context,omitempty"`
	ObjectKey  string                  `json:"object_key,omitempty"`
	ObjectHash string                  `json:"object_hash,omitempty"`
	Source     *decisionSource         `json:"source,omitempty"`
	Questions  []decision.Question     `json:"questions"`
	Candidates []dms.DecisionCandidate `json:"candidates,omitempty"`
	Guard      *dms.TaxonomyGuard      `json:"guard,omitempty"`
	Selection  string                  `json:"selection,omitempty"`
	Strategy   string                  `json:"strategy"`
	Threshold  float64                 `json:"threshold,omitempty"`
}

type decisionResult struct {
	Probabilities [][]float64 `json:"probabilities"`
	Tokens        []int       `json:"tokens,omitempty"`
	InputHash     string      `json:"input_hash"`
	Cached        bool        `json:"cached"`
}

func (r decisionResult) results() []decision.Result {
	out := make([]decision.Result, len(r.Probabilities))
	for i, p := range r.Probabilities {
		out[i].Probabilities = p
	}
	return out
}

func decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}

// maxDecision bounds a decision's payload and result.
const maxDecision = 64 << 10

func bounded(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err == nil && len(raw) > maxDecision {
		err = dms.Invalid("the decision exceeds 64 KiB")
	}
	return raw, err
}

// source fills the job's input from the request: exactly one of input,
// data or text_id. It returns the operation's container.
func (a *Assistant) source(ctx context.Context, s *dms.Service, subject string, in DecisionSource, job *decisionJob) (string, error) {
	count := 0
	for _, given := range []bool{in.Input != "", in.Data != nil, in.TextID != ""} {
		if given {
			count++
		}
	}
	if count != 1 {
		return "", dms.Invalid("a decision needs exactly one of input, data or text_id")
	}
	if in.TextID == "" && (in.Context != nil || in.BlobID != "" || in.ItemID != "") {
		return "", dms.Invalid("context, item_id and blob_id need text_id")
	}
	if in.TextID == "" {
		if in.WorkspaceID == "" {
			return "", dms.Invalid("give workspace_id")
		}
		if _, err := s.Authorize(ctx, subject, in.WorkspaceID, "read"); err != nil {
			return "", err
		}
		job.Workspace = in.WorkspaceID
	}
	switch {
	case in.Input != "":
		if strings.TrimSpace(in.Input) == "" || len(in.Input) > 16*1024 {
			return "", dms.Invalid("input needs 1 to 16384 bytes of text")
		}
		job.Input, job.Serializer = in.Input, decision.TextSerialization
		return in.WorkspaceID, nil
	case in.Data != nil:
		if len(in.Data) == 0 {
			return "", dms.Invalid("data must be a populated JSON object")
		}
		raw, err := decision.Payload(in.Data)
		if err != nil {
			return "", dms.Invalid(err.Error())
		}
		job.Input, job.Serializer = string(raw), decision.JSONSerialization
		return in.WorkspaceID, nil
	}
	if in.ItemID == "" || in.BlobID == "" {
		return "", dms.Invalid("text_id needs item_id and blob_id")
	}
	r, err := s.Authorize(ctx, subject, in.ItemID, "read")
	if err != nil {
		return "", err
	}
	b, err := s.CurrentFile(ctx, subject, in.ItemID)
	if err != nil {
		return "", err
	}
	if b.ID != in.BlobID {
		return "", dms.ErrPreconditionFailed
	}
	row, err := s.DAL.Blobs().Text(ctx, in.TextID)
	if err != nil || row.Sha256 != b.Sha256 {
		return "", dms.Invalid("text_id is not a text of the file")
	}
	if row.Chars == 0 {
		return "", dms.Invalid("the file's text is empty")
	}
	job.Workspace = r.WorkspaceID
	job.ObjectKey, job.ObjectHash = row.ObjectKey, row.ObjectSha256
	job.Source = &decisionSource{ItemID: in.ItemID, BlobID: in.BlobID, TextID: in.TextID, ItemVersion: r.Version}
	job.Serializer = decision.TextSerialization
	if in.Context != nil {
		if _, err = decision.Payload(in.Context); err != nil {
			return "", dms.Invalid(err.Error())
		}
		job.Context, job.HasContext, job.Serializer = in.Context, true, decision.DocumentContextSerialization
	}
	return in.ItemID, nil
}

// StartDecision accepts a decision as subject.
func (a *Assistant) StartDecision(ctx context.Context, s *dms.Service, subject string, in DecisionRequest) (*dms.OperationView, error) {
	if a.Decisions == nil {
		return nil, dms.Invalid(ErrNoDecisions.Error())
	}
	job := decisionJob{Namespace: KindDecide, Model: a.Decisions.ID(), Strategy: decision.Rendering, Questions: in.Questions}
	container, err := a.source(ctx, s, subject, in.DecisionSource, &job)
	if err != nil {
		return nil, err
	}
	return a.startDecision(ctx, s, subject, KindDecide, container, job)
}

// StartTaxonomyDecision accepts a taxonomy decision as subject, who needs
// to read the term set.
func (a *Assistant) StartTaxonomyDecision(ctx context.Context, s *dms.Service, subject string, in TaxonomyDecisionRequest) (*dms.OperationView, error) {
	if a.Decisions == nil {
		return nil, dms.Invalid(ErrNoDecisions.Error())
	}
	job := decisionJob{Namespace: KindTaxonomy, Model: a.Decisions.ID(), Strategy: decision.Rendering, Selection: in.Selection, Threshold: in.Threshold}
	container, err := a.source(ctx, s, subject, in.DecisionSource, &job)
	if err != nil {
		return nil, err
	}
	if job.Selection == "" {
		job.Selection = "multiple"
	}
	if job.Selection != "multiple" && job.Selection != "single" {
		return nil, dms.Invalid("selection must be multiple or single")
	}
	if job.Threshold == 0 {
		job.Threshold = .8
	}
	if !(job.Threshold > .5 && job.Threshold <= 1) {
		return nil, dms.Invalid("threshold must be greater than 0.5 and at most 1")
	}
	if in.RootTermID != "" && len(in.TermIDs) > 0 {
		return nil, dms.Invalid("root_term_id and term_ids are mutually exclusive")
	}
	limit := 50
	if job.Selection == "single" {
		limit = 19
	}
	snapshot, err := s.DecisionCandidates(ctx, subject, in.TermSetID, dms.DecisionScope{RootTermID: in.RootTermID, TermIDs: in.TermIDs, LeafOnly: in.LeafOnly}, in.DescriptionOverrides, limit)
	if err != nil {
		return nil, err
	}
	job.Candidates, job.Guard = snapshot.Candidates, &snapshot.Guard
	question, other := in.Question, in.OtherText
	if question == "" {
		question = "Which category best describes this document?"
	}
	if other == "" {
		other = "Other document"
	}
	if job.Selection == "multiple" {
		job.Strategy = "independent-category-v1"
		for i, c := range job.Candidates {
			job.Questions = append(job.Questions, decision.Question{Key: fmt.Sprintf("candidate_%d", i), Type: "choice", Instructions: question, Options: []decision.Option{{Key: "unmatched", Text: other}, {Key: c.ID, Text: c.Text}}})
		}
	} else {
		job.Strategy = "single-category-v1"
		q := decision.Question{Key: "category", Type: "choice", Instructions: question}
		for _, c := range job.Candidates {
			q.Options = append(q.Options, decision.Option{Key: c.ID, Text: c.Text})
		}
		q.Options = append(q.Options, decision.Option{Key: "unmatched", Text: other})
		job.Questions = []decision.Question{q}
	}
	return a.startDecision(ctx, s, subject, KindTaxonomy, container, job)
}

func questionLimit(job decisionJob) int {
	if job.Namespace == KindTaxonomy {
		return 50
	}
	return 32
}

func (a *Assistant) startDecision(ctx context.Context, s *dms.Service, subject, kind, container string, job decisionJob) (*dms.OperationView, error) {
	if _, err := decision.Requests(job.Questions, "validation", questionLimit(job)); err != nil {
		return nil, dms.Invalid(err.Error())
	}
	definitions, _ := decision.Canonical(job.Questions)
	context, _ := decision.Canonical(job.Context)
	fingerprint := ""
	if job.Guard != nil {
		fingerprint = job.Guard.Fingerprint
	}
	source := hashText(job.Input)
	if job.Source != nil {
		source = job.ObjectHash + ":" + job.Source.TextID
	}
	job.Key = ai.CacheKey(job.Model, job.Serializer, job.Strategy, source, string(context), string(definitions), fingerprint)
	payload, err := bounded(job)
	if err != nil {
		return nil, err
	}
	// The largest result this decision can have must fit too.
	worst := decisionResult{InputHash: strings.Repeat("f", 64)}
	for _, q := range job.Questions {
		p := make([]float64, len(q.Options))
		for i := range p {
			p[i] = 1.2345678901234567e-100
		}
		worst.Probabilities = append(worst.Probabilities, p)
	}
	if _, err = bounded(decisionOutput(job, worst)); err != nil {
		return nil, err
	}
	var out *dms.OperationView
	err = s.Write(ctx, func(t *dms.Service) error {
		var err error
		if out, err = t.EnqueueOperation(ctx, subject, container, kind, payload); err != nil || out.Replayed || job.Source == nil {
			return err
		}
		return t.LeaseOperationBlobs(ctx, out.ID, []string{job.Source.BlobID})
	})
	return out, err
}

func (a *Assistant) validateDecision(_ context.Context, _ *dms.Service, _, _ string, payload json.RawMessage) error {
	var job decisionJob
	if err := decode(payload, &job); err != nil || job.Key == "" {
		return dms.Invalid("invalid decision payload")
	}
	return nil
}

// decide answers the questions with the local engine outside the writer,
// unless the answer is cached, and ends the operation once the item's file
// and the term set are still those it decided about.
func (a *Assistant) decide(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var job decisionJob
	if err := decode(op.Payload, &job); err != nil {
		return err
	}
	if a.Decisions == nil {
		return ErrNoDecisions
	}
	if a.Decisions.ID() != job.Model {
		return errors.New("the decision model changed before the decision ran")
	}
	if err := a.cacheReady(); err != nil {
		return err
	}
	requests, err := decision.Requests(job.Questions, "validation", questionLimit(job))
	if err != nil {
		return err
	}
	find := func() (decisionResult, bool, error) {
		var r decisionResult
		data, err := a.Results.Get(ctx, job.Namespace, job.Workspace, job.Key)
		if err != nil || data == nil {
			return r, false, err
		}
		if err = decode(data, &r); err != nil {
			return r, false, err
		}
		if err = checkResult(job, requests, r); err != nil {
			return r, false, err
		}
		r.Cached = true
		return r, true, nil
	}
	started := time.Now()
	result, found, err := find()
	if err != nil {
		return err
	}
	if !found {
		if result, err = a.infer(ctx, job); err != nil {
			slog.Warn("decision failed", "kind", job.Namespace, "model", job.Model, "error", err)
			return err
		}
	}
	slog.Info("decision complete", "kind", job.Namespace, "model", job.Model, "questions", len(job.Questions), "candidates", len(job.Candidates), "tokens", result.Tokens, "cached", result.Cached, "ms", time.Since(started).Milliseconds())
	raw, err := bounded(decisionOutput(job, result))
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		if job.Source != nil {
			if err := t.CheckItemGuards(ctx, op.CreatedBy, job.Source.ItemID, nil, job.Source.BlobID, nil); err != nil {
				return err
			}
		}
		if job.Guard != nil {
			if err := t.CheckTaxonomyGuard(ctx, op.CreatedBy, *job.Guard); err != nil {
				return err
			}
		}
		completed := time.Now().UTC()
		op.Status, op.Result, op.CompletedAt = operations.StatusSucceeded, raw, &completed
		return t.SaveOperation(ctx, op)
	})
}

// infer runs the engine and caches its answer; a concurrent answer cached
// first wins.
func (a *Assistant) infer(ctx context.Context, job decisionJob) (decisionResult, error) {
	state := job.Input
	if job.Source != nil {
		data, err := a.readObject(ctx, job.ObjectKey)
		if err != nil {
			return decisionResult{}, err
		}
		if hashText(string(data)) != job.ObjectHash {
			return decisionResult{}, errors.New("the stored text changed")
		}
		var stored struct {
			Pages []doctext.Page `json:"pages"`
		}
		if err = decode(data, &stored); err != nil {
			return decisionResult{}, err
		}
		texts := make([]string, len(stored.Pages))
		for i, p := range stored.Pages {
			texts[i] = p.Text
		}
		state = strings.Join(texts, "\n\n")
		if strings.TrimSpace(state) == "" {
			return decisionResult{}, errors.New("the document text is empty")
		}
		if job.HasContext {
			b, err := decision.Canonical(map[string]any{"document_text": state, "context": job.Context})
			if err != nil {
				return decisionResult{}, err
			}
			state = string(b)
		}
	}
	requests, err := decision.Requests(job.Questions, state, questionLimit(job))
	if err != nil {
		return decisionResult{}, err
	}
	results, err := a.Decisions.Decide(ctx, requests)
	if err != nil {
		var row *decision.RowError
		if errors.As(err, &row) && row.Index >= 0 && row.Index < len(job.Questions) {
			key := job.Questions[row.Index].Key
			if job.Selection == "multiple" {
				key = job.Candidates[row.Index].ID
			}
			return decisionResult{}, fmt.Errorf("decision question or term %s: %w", key, err)
		}
		return decisionResult{}, err
	}
	if err = decision.CheckResults(requests, results); err != nil {
		return decisionResult{}, err
	}
	result := decisionResult{InputHash: hashText(state), Probabilities: make([][]float64, len(results)), Tokens: make([]int, len(results))}
	for i, r := range results {
		result.Probabilities[i], result.Tokens[i] = r.Probabilities, r.Tokens
	}
	payload, err := bounded(result)
	if err != nil {
		return decisionResult{}, err
	}
	winning, err := a.Results.PutIfAbsent(ctx, job.Namespace, job.Workspace, job.Key, payload, a.ResultTTL)
	if err != nil {
		return decisionResult{}, err
	}
	if err = decode(winning, &result); err != nil {
		return decisionResult{}, err
	}
	return result, checkResult(job, requests, result)
}

func checkResult(job decisionJob, requests []decision.Request, r decisionResult) error {
	if err := decision.CheckResults(requests, r.results()); err != nil {
		return err
	}
	if digest, err := hex.DecodeString(r.InputHash); err != nil || len(digest) != 32 {
		return errors.New("the decision result has an invalid input digest")
	}
	if job.Source == nil && r.InputHash != hashText(job.Input) {
		return errors.New("the decision result's input digest does not match")
	}
	return nil
}

// decisionOutput is a decision operation's result: answers by question
// key, or for a taxonomy decision the accepted term IDs, every candidate's
// score and the guard to update the item with.
func decisionOutput(job decisionJob, r decisionResult) map[string]any {
	out := map[string]any{"model": job.Model, "input_hash": r.InputHash, "cached": r.Cached}
	if job.Source != nil {
		out["source"] = job.Source
	}
	if job.Guard == nil {
		answers, _ := decision.Answers(job.Questions, r.results())
		out["answers"] = answers
		return out
	}
	out["term_set_id"] = job.Guard.TermSetID
	out["selection"] = job.Selection
	out["strategy"] = job.Strategy
	out["taxonomy_guard"] = job.Guard
	ids := []string{}
	scores := []map[string]any{}
	winner := 0
	if job.Selection == "single" {
		for j, p := range r.Probabilities[0] {
			if p > r.Probabilities[0][winner] {
				winner = j
			}
		}
		out["other_probability"] = r.Probabilities[0][len(job.Candidates)]
	}
	for i, c := range job.Candidates {
		p := 0.0
		if job.Selection == "multiple" {
			p = r.Probabilities[i][1]
		} else {
			p = r.Probabilities[0][i]
		}
		scores = append(scores, map[string]any{"term_id": c.ID, "probability": p})
		if p >= job.Threshold && (job.Selection == "multiple" || winner == i) {
			ids = append(ids, c.ID)
		}
	}
	out["term_ids"] = ids
	out["scores"] = scores
	return out
}
