package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/forms"
)

var ErrNoDecisions = errors.New("decision actions require AI_DECISION_MODELS_PATH and ONNXRUNTIME_LIB")

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

const decisionSourceProps = itemIDProp + `,
 "input":{"type":"string","title":"Input text"},
 "data":{"type":"object","title":"JSON data","additionalProperties":true},
 "text_id":{"type":"string","title":"Extracted text ID"},
 "blob_id":{"type":"string","title":"Source file version"},
 "context":{"type":"object","title":"Document context","additionalProperties":true}`
const questionsProp = `"questions":{"type":"array","title":"Questions","minItems":1,"maxItems":32,"x-decision-questions":true,"items":{"type":"object","properties":{
 "key":{"type":"string","title":"Key","minLength":1,"maxLength":64},
 "type":{"type":"string","title":"Type","enum":["choice","noul","score"],"default":"choice","description":"choice selects an option; noul returns the true probability; score is the expected zero-based ordinal level."},
 "instructions":{"type":"string","title":"Instructions","minLength":1},
 "options":{"type":"array","title":"Options","minItems":2,"maxItems":20,"items":{"type":"object","properties":{"key":{"type":"string","title":"Key","minLength":1,"maxLength":64},"text":{"type":"string","title":"Model text","minLength":1}},"required":["key","text"]}}
 },"required":["key","instructions","options"]}}`
const taxonomyScopeProps = `"root_term_id":{"type":"string","title":"Subtree root","x-papergo":{"kind":"terms"},"x-term-set-sibling":"term_set_id"},
 "term_ids":{"type":"array","title":"Candidate subset","minItems":1,"maxItems":50,"uniqueItems":true,"items":{"type":"string"},"x-papergo":{"kind":"terms"},"x-term-set-sibling":"term_set_id"},
 "leaf_only":{"type":"boolean","title":"Leaves only","default":false}`
const taxonomyGuardSchema = `{"type":"object","properties":{"term_set_id":{"type":"string"},"selection_scope":{"type":"object","properties":{"root_term_id":{"type":"string"},"term_ids":{"type":"array","items":{"type":"string"}},"leaf_only":{"type":"boolean"}}},"fingerprint":{"type":"string"}},"required":["term_set_id","selection_scope","fingerprint"]}`
const decisionSourceSchema = `{"type":"object","properties":{"item_id":{"type":"string"},"blob_id":{"type":"string"},"text_id":{"type":"string"},"item_version":{"type":"integer"}}}`
const decisionCommonOutput = `"source":` + decisionSourceSchema + `,"model":{"type":"string"},"input_hash":{"type":"string"},"cached":{"type":"boolean"}`
const answerSchema = `{"type":"object","properties":{"type":{"type":"string"},"selected":{"type":"string"},"probability":{"type":"number"},"probabilities":{"type":"object","additionalProperties":{"type":"number"}},"noul":{"type":"number"},"score":{"type":"number"}}}`

func init() {
	register(&Activity{Key: "ai.decide", Kind: "action", Description: "Answers ordered choice, noul or ordinal score questions about text, JSON data or a document. Returns probability scores; workflows control routing and review.", Ports: []string{"done"}, InputSchema: inputs(decisionSourceProps+","+questionsProp, "questions"), OutputSchema: forms.MustParse(`{"type":"object","properties":{"answers":{"type":"object","additionalProperties":` + answerSchema + `},` + decisionCommonOutput + `}}`), run: runDecision, work: workDecision, commit: commitDecision})
	register(&Activity{Key: "taxonomy.decide", Kind: "action", Ports: []string{"done", "none"}, Description: "Evaluates active terms of an organization term set and returns managed IDs, scores and a taxonomy guard. multiple compares each term independently; single uses one joint choice. No accepted terms follows none; invalid configuration or inference follows error.", InputSchema: inputs(decisionSourceProps+`,
 "term_set_id":{"type":"string","title":"Term set","x-papergo":{"kind":"terms","select":"sets"}},
 "selection":{"type":"string","title":"Selection","enum":["multiple","single"],"default":"multiple"},
 "threshold":{"type":"number","title":"Inclusion threshold","minimum":0.5,"maximum":1,"default":0.8,"description":"Must be greater than 0.5. Experimental probability score; not calibrated confidence."},
 "question":{"type":"string","title":"Question","default":"Which category best describes this document?"},
 "other_text":{"type":"string","title":"Unmatched option","default":"Other document"},`+taxonomyScopeProps+`,
 "description_overrides":{"type":"array","title":"Description overrides","x-term-set-sibling":"term_set_id","maxItems":50,"items":{"type":"object","properties":{"term_id":{"type":"string","title":"Term","x-papergo":{"kind":"terms"}},"text":{"type":"string","title":"Exact option text"}},"required":["term_id","text"]}}`, "term_set_id"), OutputSchema: forms.MustParse(`{"type":"object","properties":{"term_set_id":{"type":"string"},"selection":{"type":"string"},"strategy":{"type":"string"},"term_ids":{"type":"array","items":{"type":"string"}},"scores":{"type":"array","items":{"type":"object","properties":{"term_id":{"type":"string"},"probability":{"type":"number"}}}},"other_probability":{"type":"number"},"taxonomy_guard":` + taxonomyGuardSchema + `,` + decisionCommonOutput + `}}`), run: runTaxonomyDecision, work: workDecision, commit: commitDecision})
}

type decisionSource struct {
	ItemID      string `json:"item_id"`
	BlobID      string `json:"blob_id"`
	TextID      string `json:"text_id"`
	ItemVersion int    `json:"item_version"`
}
type decisionJob struct {
	Namespace  string                  `json:"namespace"`
	Workspace  string                  `json:"workspace"`
	Model      string                  `json:"model"`
	Key        string                  `json:"key"`
	Serializer string                  `json:"serializer"`
	Input      string                  `json:"input,omitempty"`
	Context    map[string]any          `json:"context"`
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

func decisionDecode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}
func decisionConvert(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return decisionDecode(b, out)
}
func literalString(in map[string]any, key string) (string, error) {
	v, ok := in[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text", key)
	}
	return s, nil
}

func prepareDecisionSource(e *env, in map[string]any, job *decisionJob) error {
	_, input := in["input"]
	_, data := in["data"]
	_, text := in["text_id"]
	count := 0
	for _, yes := range []bool{input, data, text} {
		if yes {
			count++
		}
	}
	if count != 1 {
		return errors.New("decision needs exactly one of input, data or text_id")
	}
	ctx, hasContext := in["context"]
	if !text && (hasContext || in["blob_id"] != nil) {
		return errors.New("context and blob_id require text_id")
	}
	if hasContext {
		var ok bool
		job.Context, ok = ctx.(map[string]any)
		if !ok {
			return errors.New("context must be an object")
		}
		job.HasContext = true
	}
	if input {
		v, err := literalString(in, "input")
		if err != nil {
			return err
		}
		if strings.TrimSpace(v) == "" {
			return errors.New("input must not be empty")
		}
		if len(v) > 16*1024 {
			return errors.New("input exceeds 16 KiB")
		}
		job.Input = v
		job.Serializer = decision.TextSerialization
		return nil
	}
	if data {
		v, ok := in["data"].(map[string]any)
		if !ok || len(v) == 0 {
			return errors.New("data must be a populated JSON object")
		}
		raw, err := decision.Payload(v)
		if err != nil {
			return err
		}
		job.Input = string(raw)
		job.Serializer = decision.JSONSerialization
		return nil
	}
	textID, err := literalString(in, "text_id")
	if err != nil {
		return err
	}
	blobID, err := literalString(in, "blob_id")
	if err != nil {
		return err
	}
	itemID, err := literalString(in, "item_id")
	if err != nil {
		return err
	}
	if itemID == "" {
		itemID = e.itemID
	}
	if textID == "" || blobID == "" || itemID == "" {
		return errors.New("text_id requires nonempty blob_id and item_id (or a run item)")
	}
	r, err := e.t.Authorize(e.ctx, e.author, itemID, "read")
	if err != nil {
		return err
	}
	b, err := e.t.CurrentFile(e.ctx, e.author, itemID)
	if err != nil {
		return err
	}
	if b.ID != blobID {
		return dms.ErrPreconditionFailed
	}
	row, err := e.t.BlobText(e.ctx, textID)
	if err != nil {
		return err
	}
	if row.Sha256 != b.Sha256 {
		return errors.New("text_id belongs to another file")
	}
	if row.Chars == 0 {
		return errors.New("stored document text is empty")
	}
	job.ObjectKey = row.ObjectKey
	job.ObjectHash = row.ObjectSha256
	job.Source = &decisionSource{itemID, blobID, textID, r.Version}
	job.Serializer = decision.TextSerialization
	if hasContext {
		if _, err = decision.Payload(job.Context); err != nil {
			return err
		}
		job.Serializer = decision.DocumentContextSerialization
	}
	return nil
}

func runDecision(e *env, in map[string]any) (outcome, error) { return prepareDecision(e, in, false) }
func runTaxonomyDecision(e *env, in map[string]any) (outcome, error) {
	return prepareDecision(e, in, true)
}
func prepareDecision(e *env, in map[string]any, taxonomy bool) (outcome, error) {
	if e.tools.Decisions == nil {
		return outcome{}, ErrNoDecisions
	}
	if _, stored := in["text_id"]; !stored {
		// Inline snapshots have no item binding; do not resolve an irrelevant
		// item token (including in an item-free manual run).
		copied := make(map[string]any, len(in))
		for key, value := range in {
			if key != "item_id" {
				copied[key] = value
			}
		}
		in = copied
	}
	resolved, err := e.scope.resolve(in)
	if err != nil {
		return outcome{}, err
	}
	in = resolved.(map[string]any)
	schema := activities["ai.decide"].InputSchema
	if taxonomy {
		schema = activities["taxonomy.decide"].InputSchema
	}
	in, err = forms.Values(schema, in, "decision")
	if err != nil {
		return outcome{}, err
	}
	job := decisionJob{Namespace: "ai.decide", Workspace: e.workspaceID, Model: e.tools.Decisions.ID(), Strategy: decision.Rendering}
	if err = prepareDecisionSource(e, in, &job); err != nil {
		return outcome{}, err
	}
	if taxonomy {
		job.Namespace = "taxonomy.decide"
		job.Selection = "multiple"
		job.Threshold = .8
		if v, ok := in["selection"]; ok {
			job.Selection, _ = v.(string)
		}
		if job.Selection != "multiple" && job.Selection != "single" {
			return outcome{}, errors.New("selection must be multiple or single")
		}
		if v, ok := in["threshold"]; ok {
			var valid bool
			job.Threshold, valid = forms.Number(v)
			if !valid {
				return outcome{}, errors.New("threshold must be a number")
			}
		}
		if !(job.Threshold > .5 && job.Threshold <= 1) {
			return outcome{}, errors.New("threshold must be greater than 0.5 and at most 1")
		}
		setID, err := literalString(in, "term_set_id")
		if err != nil {
			return outcome{}, err
		}
		var scope dms.DecisionScope
		var overrides []dms.DecisionOverride
		if err = decisionConvert(map[string]any{"root_term_id": in["root_term_id"], "term_ids": in["term_ids"], "leaf_only": in["leaf_only"]}, &scope); err != nil {
			return outcome{}, err
		}
		if v, ok := in["description_overrides"]; ok {
			if err = decisionConvert(v, &overrides); err != nil {
				return outcome{}, err
			}
		}
		limit := 50
		if job.Selection == "single" {
			limit = 19
		}
		snapshot, err := e.t.DecisionCandidates(e.ctx, e.author, setID, scope, overrides, limit)
		if err != nil {
			return outcome{}, err
		}
		job.Candidates = snapshot.Candidates
		job.Guard = &snapshot.Guard
		question, other := "Which category best describes this document?", "Other document"
		if _, ok := in["question"]; ok {
			question, err = literalString(in, "question")
			if err != nil {
				return outcome{}, err
			}
		}
		if _, ok := in["other_text"]; ok {
			other, err = literalString(in, "other_text")
			if err != nil {
				return outcome{}, err
			}
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
	} else {
		if err = decisionConvert(in["questions"], &job.Questions); err != nil {
			return outcome{}, err
		}
	}
	if _, err = decision.Requests(job.Questions, "validation", decisionQuestionLimit(job)); err != nil {
		return outcome{}, err
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
	raw, err := boundedDecision(job)
	if err != nil {
		return outcome{}, err
	}
	// Preflight the largest probability representation plus job-dependent output metadata.
	worst := decisionResult{InputHash: strings.Repeat("f", 64)}
	for _, q := range job.Questions {
		p := make([]float64, len(q.Options))
		for i := range p {
			p[i] = 1.2345678901234567e-100
		}
		p[0] = 1
		worst.Probabilities = append(worst.Probabilities, p)
	}
	if _, err = boundedDecision(decisionOutput(job, worst)); err != nil {
		return outcome{}, err
	}
	return outcome{Job: raw}, nil
}

func boundedDecision(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err == nil && len(raw) > MaxOutputBytes {
		err = errors.New("decision exceeds the 64 KiB durable envelope")
	}
	return raw, err
}
func decisionRequests(job decisionJob, state string) ([]decision.Request, error) {
	return decision.Requests(job.Questions, state, decisionQuestionLimit(job))
}
func (r decisionResult) results() []decision.Result {
	out := make([]decision.Result, len(r.Probabilities))
	for i, p := range r.Probabilities {
		out[i].Probabilities = p
	}
	return out
}

func workDecision(w *workEnv, raw json.RawMessage) (json.RawMessage, error) {
	var job decisionJob
	if err := decisionDecode(raw, &job); err != nil {
		return nil, err
	}
	if w.tools.Decisions == nil {
		return nil, ErrNoDecisions
	}
	if w.tools.Decisions.ID() != job.Model {
		return nil, errors.New("decision model identity changed")
	}
	if w.tools.Results == nil || w.tools.ResultTTL <= 0 {
		return nil, errors.New("decisions require a result cache with positive TTL")
	}
	requests, err := decisionRequests(job, "validation")
	if err != nil {
		return nil, err
	}
	find := func() (decisionResult, bool, error) {
		var r decisionResult
		data, err := w.tools.Results.Get(w.ctx, job.Namespace, job.Workspace, job.Key)
		if err != nil || data == nil {
			return r, false, err
		}
		if err = decisionDecode(data, &r); err != nil {
			return r, false, err
		}
		if err = checkDecisionResult(job, requests, r); err != nil {
			return r, false, err
		}
		r.Cached = true
		return r, true, nil
	}
	if r, found, err := find(); err != nil {
		return nil, err
	} else if found {
		slog.Info("workflow decision complete", "action", job.Namespace, "model", job.Model, "questions", len(job.Questions), "candidates", len(job.Candidates), "tokens", r.Tokens, "cached", true, "queue_ms", 0, "inference_ms", 0)
		return boundedDecision(r)
	}
	var result decisionResult
	started := time.Now()
	var queueMS, inferenceMS int64
	err = w.exclusive(func() error {
		queueMS = time.Since(started).Milliseconds()
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if w.tools.Decisions.ID() != job.Model {
			return errors.New("decision model identity changed")
		}
		if r, found, err := find(); err != nil {
			return err
		} else if found {
			result = r
			return nil
		}
		state := job.Input
		if job.Source != nil {
			data, err := w.read(job.ObjectKey)
			if err != nil {
				return err
			}
			if hashText(string(data)) != job.ObjectHash {
				return errors.New("stored text checksum mismatch")
			}
			var stored textObject
			if err = decisionDecode(data, &stored); err != nil {
				return err
			}
			texts := make([]string, len(stored.Pages))
			for i, p := range stored.Pages {
				texts[i] = p.Text
			}
			state = strings.Join(texts, "\n\n")
			if strings.TrimSpace(state) == "" {
				return errors.New("document text is empty")
			}
			if job.HasContext {
				b, err := decision.Canonical(map[string]any{"document_text": state, "context": job.Context})
				if err != nil {
					return err
				}
				state = string(b)
			}
		}
		requests, err = decisionRequests(job, state)
		if err != nil {
			return err
		}
		inferenceStart := time.Now()
		results, err := w.tools.Decisions.Decide(w.ctx, requests)
		inferenceMS = time.Since(inferenceStart).Milliseconds()
		if err != nil {
			var row *decision.RowError
			if errors.As(err, &row) && row.Index >= 0 && row.Index < len(job.Questions) {
				key := job.Questions[row.Index].Key
				if job.Selection == "multiple" {
					key = job.Candidates[row.Index].ID
				}
				return fmt.Errorf("decision question/term %s: %w", key, err)
			}
			return err
		}
		if err = decision.CheckResults(requests, results); err != nil {
			return err
		}
		if err = w.ctx.Err(); err != nil {
			return err
		}
		result = decisionResult{InputHash: hashText(state), Probabilities: make([][]float64, len(results))}
		result.Tokens = make([]int, len(results))
		for i, r := range results {
			result.Probabilities[i] = r.Probabilities
			result.Tokens[i] = r.Tokens
		}
		if _, err = boundedDecision(decisionOutput(job, result)); err != nil {
			return err
		}
		payload, err := boundedDecision(result)
		if err != nil {
			return err
		}
		winning, err := w.tools.Results.PutIfAbsent(w.ctx, job.Namespace, job.Workspace, job.Key, payload, w.tools.ResultTTL)
		if err != nil {
			return err
		}
		if err = decisionDecode(winning, &result); err != nil {
			return err
		}
		return checkDecisionResult(job, requests, result)
	})
	if err != nil {
		slog.Warn("workflow decision failed", "action", job.Namespace, "model", job.Model, "reason", "work_failed", "error", err, "queue_ms", queueMS, "inference_ms", inferenceMS)
		return nil, err
	}
	slog.Info("workflow decision complete", "action", job.Namespace, "model", job.Model, "questions", len(job.Questions), "candidates", len(job.Candidates), "tokens", result.Tokens, "cached", result.Cached, "queue_ms", queueMS, "inference_ms", inferenceMS, "work_ms", time.Since(started).Milliseconds())
	return boundedDecision(result)
}

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

func commitDecision(e *env, rawJob, rawResult json.RawMessage) (outcome, error) {
	var job decisionJob
	var result decisionResult
	if err := decisionDecode(rawJob, &job); err != nil {
		return outcome{}, err
	}
	if err := decisionDecode(rawResult, &result); err != nil {
		return outcome{}, err
	}
	if err := e.ctx.Err(); err != nil {
		return outcome{}, err
	}
	if e.tools.Decisions == nil || e.tools.Decisions.ID() != job.Model {
		return outcome{}, errors.New("decision model identity changed")
	}
	if job.Source != nil {
		if err := e.t.CheckItemGuards(e.ctx, e.author, job.Source.ItemID, nil, job.Source.BlobID, nil); err != nil {
			return outcome{}, err
		}
	}
	if job.Guard != nil {
		if err := e.t.CheckTaxonomyGuard(e.ctx, e.author, *job.Guard); err != nil {
			return outcome{}, err
		}
	}
	req, err := decisionRequests(job, "validation")
	if err != nil {
		return outcome{}, err
	}
	if err = checkDecisionResult(job, req, result); err != nil {
		return outcome{}, err
	}
	out := decisionOutput(job, result)
	if _, err = boundedDecision(outcome{Output: out}); err != nil {
		return outcome{}, err
	}
	port := "done"
	if job.Guard != nil && len(out["term_ids"].([]string)) == 0 {
		port = "none"
	}
	return outcome{Port: port, Output: out}, nil
}

func hasDecisionTokens(v any) bool {
	switch t := v.(type) {
	case string:
		return tokenPattern.MatchString(t)
	case []any:
		for _, e := range t {
			if hasDecisionTokens(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if hasDecisionTokens(e) {
				return true
			}
		}
	}
	return false
}
func validateDecisionStatic(a *Activity, in map[string]any) error {
	if err := checkStaticDecisionValue(a.InputSchema, in, "decision"); err != nil {
		return err
	}
	sources := 0
	for _, k := range []string{"input", "data", "text_id"} {
		if _, ok := in[k]; ok {
			sources++
		}
	}
	if sources != 1 {
		return errors.New("decision needs exactly one of input, data or text_id")
	}
	if _, ok := in["text_id"]; ok {
		if in["blob_id"] == nil {
			return errors.New("text_id requires blob_id")
		}
	} else if in["context"] != nil || in["blob_id"] != nil {
		return errors.New("context and blob_id require text_id")
	}
	if !hasDecisionTokens(in) {
		if _, err := forms.Values(a.InputSchema, in, "decision"); err != nil {
			return err
		}
	}
	if v, ok := in["input"]; ok && !hasDecisionTokens(v) {
		text, ok := v.(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 16*1024 {
			return errors.New("input needs 1–16384 bytes of text")
		}
	}
	if v, ok := in["data"]; ok && !hasDecisionTokens(v) {
		obj, ok := v.(map[string]any)
		if !ok || len(obj) == 0 {
			return errors.New("data must be a populated JSON object")
		}
		if _, err := decision.Payload(obj); err != nil {
			return err
		}
	}
	if a.Key == "ai.decide" && !hasDecisionTokens(in["questions"]) {
		var q []decision.Question
		if err := decisionConvert(in["questions"], &q); err != nil {
			return err
		}
		_, err := decision.Requests(q, "validation", 32)
		return err
	}
	if a.Key == "taxonomy.decide" {
		if in["root_term_id"] != nil && in["term_ids"] != nil {
			return errors.New("root_term_id and term_ids are mutually exclusive")
		}
		if v, ok := in["threshold"]; ok && !hasDecisionTokens(v) {
			n, ok := forms.Number(v)
			if !ok || !(n > .5 && n <= 1) {
				return errors.New("threshold must be greater than 0.5 and at most 1")
			}
		}
	}
	return nil
}

func checkStaticTaxonomyDecision(ctx context.Context, t *dms.Service, subject string, in map[string]any) error {
	config := map[string]any{}
	for _, key := range []string{"term_set_id", "root_term_id", "term_ids", "leaf_only", "description_overrides", "selection"} {
		if v, ok := in[key]; ok {
			config[key] = v
		}
	}
	set, _ := config["term_set_id"].(string)
	if !hasDecisionTokens(set) && set != "" {
		if _, err := t.TermSet(ctx, subject, set); err != nil {
			return err
		}
	}
	if hasDecisionTokens(config) {
		return nil
	}
	var scope dms.DecisionScope
	var overrides []dms.DecisionOverride
	if err := decisionConvert(config, &scope); err != nil {
		return err
	}
	if raw, ok := config["description_overrides"]; ok {
		if err := decisionConvert(raw, &overrides); err != nil {
			return err
		}
	}
	limit := 50
	if config["selection"] == "single" {
		limit = 19
	}
	_, err := t.DecisionCandidates(ctx, subject, set, scope, overrides, limit)
	return err
}

func decisionQuestionLimit(job decisionJob) int {
	if job.Namespace == "taxonomy.decide" {
		return 50
	}
	return 32
}

func checkDecisionResult(job decisionJob, requests []decision.Request, r decisionResult) error {
	if err := decision.CheckResults(requests, r.results()); err != nil {
		return err
	}
	if digest, err := hex.DecodeString(r.InputHash); err != nil || len(digest) != 32 {
		return errors.New("decision result has an invalid input digest")
	}
	if job.Source == nil && r.InputHash != hashText(job.Input) {
		return errors.New("decision result input digest mismatch")
	}
	return nil
}

// Validate every statically known subtree even when sibling inputs are tokens.
// Literal maps/array lengths are known; token-returned values wait for execution.
func checkStaticDecisionValue(schema map[string]any, value any, path string) error {
	if !hasDecisionTokens(value) {
		_, err := forms.Values(map[string]any{"type": "object", "properties": map[string]any{"value": schema}}, map[string]any{"value": value}, path)
		return err
	}
	switch v := value.(type) {
	case map[string]any:
		if schema["type"] != "object" {
			return fmt.Errorf("%s must be %v", path, schema["type"])
		}
		props, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				key, _ := r.(string)
				if v[key] == nil {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		for key, child := range v {
			childSchema, ok := props[key].(map[string]any)
			if !ok {
				if schema["additionalProperties"] == true {
					continue
				}
				childSchema, ok = schema["additionalProperties"].(map[string]any)
				if !ok {
					return fmt.Errorf("%s.%s is not an input of this form", path, key)
				}
			}
			if err := checkStaticDecisionValue(childSchema, child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		if schema["type"] != "array" {
			return fmt.Errorf("%s must be %v", path, schema["type"])
		}
		if minimum, ok := forms.WholeNumber(schema["minItems"]); ok && len(v) < minimum {
			return fmt.Errorf("%s needs at least %d entries", path, minimum)
		}
		if maximum, ok := forms.WholeNumber(schema["maxItems"]); ok && len(v) > maximum {
			return fmt.Errorf("%s takes at most %d entries", path, maximum)
		}
		if child, ok := schema["items"].(map[string]any); ok {
			for i, entry := range v {
				if err := checkStaticDecisionValue(child, entry, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
