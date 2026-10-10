package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"papergo/ent"
	"papergo/internal/dms"
)

// Activity is one kind of node. Flow activities steer the run; actions change
// or read content. New features add activities here (and built-in workflows
// that use them) instead of reacting to changes in hidden code.
type Activity struct {
	Key         string   `json:"key"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Ports       []string `json:"ports"`
	// InputSchema describes the node's inputs and OutputSchema its output
	// ({step:node...}), as JSON Schema forms an editor can render. Inputs
	// may be tokens, so their types are checked when the node runs.
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	// Terminal activities end the run and have no next nodes.
	Terminal bool `json:"terminal,omitempty"`
	run      func(*env, map[string]any) (outcome, error)
}

// Validate checks a node's inputs when a workflow is saved: required inputs
// are present and every input is known.
func (a *Activity) Validate(inputs map[string]any) error {
	props, _ := a.InputSchema["properties"].(map[string]any)
	required, _ := a.InputSchema["required"].([]any)
	for _, r := range required {
		if name, _ := r.(string); inputs[name] == nil {
			return fmt.Errorf("input %s is required", name)
		}
	}
	for name := range inputs {
		if _, ok := props[name]; !ok {
			return fmt.Errorf("activity %s has no input %s", a.Key, name)
		}
	}
	if a.Key == "if" {
		_, filter := inputs["filter"]
		_, left := inputs["op"]
		if filter == left {
			return errors.New("if needs either filter, or left, op and right")
		}
		if left {
			if op, ok := inputs["op"].(string); !ok || !compareOps[op] {
				return errors.New("op must be one of eq, ne, gt, ge, lt, le, contains, empty, not_empty")
			}
		}
	}
	if a.Key == "event.raise" {
		if name, ok := inputs["event"].(string); !ok || !keyPattern.MatchString(name) || name == "completed" || name == "failed" {
			return errors.New("event must be a lowercase key other than completed and failed")
		}
	}
	if a.Key == "delay" {
		_, d := inputs["duration"]
		_, u := inputs["until"]
		if d == u {
			return errors.New("delay needs either duration or until")
		}
		if s, ok := inputs["duration"].(string); ok && !strings.Contains(s, "{") {
			if v, err := time.ParseDuration(s); err != nil || v < 0 {
				return errors.New("duration must be a Go duration such as 90m or 48h")
			}
		}
	}
	return nil
}

// outcome is what a node produced; it is the recorded result of its step.
type outcome struct {
	Port   string         `json:"port,omitempty"`
	Output any            `json:"output,omitempty"`
	Vars   map[string]any `json:"vars,omitempty"`
	// Sleep is how long the run waits before following Port.
	Sleep float64 `json:"sleep,omitempty"`
	// End completes the run; Fail fails it with this message.
	End  bool   `json:"end,omitempty"`
	Fail string `json:"fail,omitempty"`
}

// env is what an activity can use while its node runs: the DMS service bound
// to the node's transaction, acting as the workflow version's author.
type env struct {
	ctx    context.Context
	t      *dms.Service
	author string
	itemID string
	scope  *scope
	raise  func(typ string, data map[string]any)
}

var compareOps = map[string]bool{"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true, "contains": true, "empty": true, "not_empty": true}

var activities = map[string]*Activity{}

// Activities returns the catalog, sorted by key.
func Activities() []*Activity {
	out := make([]*Activity, 0, len(activities))
	for _, a := range activities {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func register(a *Activity) {
	if a.Ports == nil {
		a.Ports = []string{}
	}
	if a.InputSchema == nil {
		a.InputSchema = mustSchema(`{"type": "object", "properties": {}}`)
	}
	activities[a.Key] = a
}

// inputs builds an activity's input schema from property schemas.
func inputs(properties string, required ...string) map[string]any {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return mustSchema(`{"type": "object", "properties": {` + properties + `}, "required": ` + string(req) + `}`)
}

const (
	itemIDProp  = `"item_id": {"type": "string", "title": "Item", "description": "The item; defaults to the run's item.", "x-papergo": {"kind": "item"}}`
	itemOutput  = `{"type": "object", "properties": {"id": {"type": "string"}, "name": {"type": "string"}, "tags": {"type": "array", "items": {"type": "string"}}, "values": {"type": "object"}, "collection_id": {"type": "string"}, "content_type_id": {"type": "string"}, "version": {"type": "integer"}, "published": {"type": "boolean"}, "created_by": {"type": "string"}, "updated_by": {"type": "string"}, "created_at": {"type": "string", "format": "date-time"}, "updated_at": {"type": "string", "format": "date-time"}}}`
	filterProp  = `{"type": "object", "title": "Filter", "description": "A query filter, as in collection queries."}`
	collectProp = `"collection_id": {"type": "string", "title": "List or library", "x-papergo": {"kind": "collection"}}`
)

func init() {
	register(&Activity{Key: "if", Kind: "flow", Ports: []string{"true", "false"}, Description: "Follows true or false. With filter, tests the run's item with the query filter language; otherwise compares left and right.",
		InputSchema:  inputs(`"filter": ` + filterProp + `, "left": {"type": "string", "title": "Left"}, "op": {"type": "string", "title": "Comparison", "enum": ["eq", "ne", "gt", "ge", "lt", "le", "contains", "empty", "not_empty"]}, "right": {"type": "string", "title": "Right"}`),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"result": {"type": "boolean"}}}`), run: runIf})
	register(&Activity{Key: "set_variable", Kind: "flow", Description: "Sets a run variable.",
		InputSchema:  inputs(`"name": {"type": "string", "title": "Variable"}, "value": {"type": "string", "title": "Value", "description": "A single token keeps its type."}`, "name"),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"name": {"type": "string"}, "value": {"type": "string"}}}`), run: runSetVariable})
	register(&Activity{Key: "delay", Kind: "flow", Description: "Waits durably for a duration or until a time.",
		InputSchema:  inputs(`"duration": {"type": "string", "title": "Duration", "description": "Such as 30m or 72h."}, "until": {"type": "string", "title": "Until", "description": "A date or RFC 3339 time."}`),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"until": {"type": "string", "format": "date-time"}}}`), run: runDelay})
	register(&Activity{Key: "event.raise", Kind: "flow", Description: "Raises wf.{workflow key}.{event} for other workflows, with the run's item.",
		InputSchema:  inputs(`"event": {"type": "string", "title": "Event"}, "data": {"type": "object", "title": "Data"}`, "event"),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"event": {"type": "string"}}}`), run: runRaise})
	register(&Activity{Key: "end", Kind: "flow", Terminal: true, Description: "Completes the run.", run: func(*env, map[string]any) (outcome, error) { return outcome{End: true}, nil }})
	register(&Activity{Key: "fail", Kind: "flow", Terminal: true, Description: "Fails the run with a message.",
		InputSchema: inputs(`"message": {"type": "string", "title": "Message"}`, "message"), run: runFail})
	register(&Activity{Key: "item.get", Kind: "action", Description: "Reads an item; its fields become the output.",
		InputSchema: inputs(itemIDProp), OutputSchema: mustSchema(itemOutput), run: runItemGet})
	register(&Activity{Key: "item.update", Kind: "action", Description: "Changes an item's name, tags or field values. Values are merged; null removes a value.",
		InputSchema:  inputs(itemIDProp + `, "name": {"type": "string", "title": "Name"}, "tags": {"type": "array", "title": "Tags", "items": {"type": "string"}}, "values": {"type": "object", "title": "Field values"}`),
		OutputSchema: mustSchema(itemOutput), run: runItemUpdate})
	register(&Activity{Key: "item.create", Kind: "action", Description: "Creates an item; the output has its item_id.",
		InputSchema:  inputs(collectProp+`, "parent_id": {"type": "string", "title": "Folder", "description": "Defaults to the collection."}, "content_type_id": {"type": "string", "title": "Content type"}, "name": {"type": "string", "title": "Name"}, "tags": {"type": "array", "title": "Tags", "items": {"type": "string"}}, "values": {"type": "object", "title": "Field values"}`, "collection_id", "name"),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"item_id": {"type": "string"}, "item": ` + itemOutput + `}}`), run: runItemCreate})
	register(&Activity{Key: "items.query", Kind: "action", Description: "Queries a collection's items (head surface); the output has items and count.",
		InputSchema:  inputs(collectProp+`, "filter": `+filterProp+`, "sort": {"type": "object", "title": "Sort"}, "limit": {"type": "integer", "title": "Limit", "minimum": 1, "maximum": 100, "default": 50}`, "collection_id"),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"items": {"type": "array", "items": ` + itemOutput + `}, "count": {"type": "integer"}}}`), run: runItemsQuery})
	register(&Activity{Key: "item.has_terms", Kind: "flow", Ports: []string{"matched", "unmatched"}, Description: "Follows matched when the item's term and keywords fields hold the terms (or descendants, or terms merged into them): any of them, or all with match all.",
		InputSchema:  inputs(itemIDProp+`, "terms": {"type": "array", "title": "Terms", "items": {"type": "string"}, "minItems": 1, "maxItems": 20, "x-papergo": {"kind": "terms"}}, "match": {"type": "string", "title": "Match", "enum": ["any", "all"], "default": "any"}`, "terms"),
		OutputSchema: mustSchema(`{"type": "object", "properties": {"matched": {"type": "boolean"}, "terms": {"type": "array", "items": {"type": "string"}, "description": "The given terms the item holds."}}}`), run: runItemHasTerms})
	register(&Activity{Key: "item.publish", Kind: "action", Description: "Publishes the item's head revision; nothing happens when it is already published.",
		InputSchema: inputs(itemIDProp), OutputSchema: mustSchema(`{"type": "object", "properties": {"published": {"type": "boolean"}, "publication_id": {"type": "string"}}}`), run: runItemPublish})
	register(&Activity{Key: "item.unpublish", Kind: "action", Description: "Unpublishes the item; nothing happens when it is not published.",
		InputSchema: inputs(itemIDProp), OutputSchema: mustSchema(`{"type": "object", "properties": {"unpublished": {"type": "boolean"}}}`), run: runItemUnpublish})
	register(&Activity{Key: "item.delete", Kind: "action", Description: "Deletes the item.",
		InputSchema: inputs(itemIDProp), OutputSchema: mustSchema(`{"type": "object", "properties": {"deleted": {"type": "boolean"}}}`), run: runItemDelete})
}

// target resolves the item an action works on.
func (e *env) target(inputs map[string]any) (string, error) {
	if v, ok := inputs["item_id"]; ok {
		id, err := e.scope.text(v)
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", errors.New("item_id is empty")
		}
		return id, nil
	}
	if e.itemID == "" {
		return "", errors.New("the run has no item; give item_id")
	}
	return e.itemID, nil
}

func (e *env) load(id string) (*ent.Resource, error) {
	r, err := e.t.GetSurface(e.ctx, e.author, id, "auto")
	if err != nil {
		return nil, err
	}
	if r.Kind != "item" {
		return nil, fmt.Errorf("%s is not an item", id)
	}
	return r, nil
}

// snapshot is the token and output view of an item.
func snapshot(r *ent.Resource) map[string]any {
	out := map[string]any{"id": r.ID, "name": r.Name, "tags": anyList(r.Tags), "values": jsonish(r.Values), "version": r.Version, "created_by": r.CreatedBy, "updated_by": r.UpdatedBy, "created_at": r.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": r.UpdatedAt.UTC().Format(time.RFC3339Nano), "published": r.PublishedRevisionID != nil}
	if r.ContainerID != nil {
		out["collection_id"] = *r.ContainerID
	}
	if r.ContentTypeID != nil {
		out["content_type_id"] = *r.ContentTypeID
	}
	return out
}

func anyList(tags []string) []any {
	out := make([]any, len(tags))
	for i, t := range tags {
		out[i] = t
	}
	return out
}

// jsonish gives values the shapes they have after a JSON round trip, so a
// first run and a replay see the same types.
func jsonish(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func runIf(e *env, in map[string]any) (outcome, error) {
	var holds bool
	if raw, ok := in["filter"]; ok {
		id, err := e.target(nil)
		if err != nil {
			return outcome{}, err
		}
		r, err := e.load(id)
		if err != nil {
			return outcome{}, err
		}
		filter, err := e.filter(raw)
		if err != nil {
			return outcome{}, err
		}
		holds, err = matches(e.ctx, e.t, e.author, *r.ContainerID, id, filter)
		if err != nil {
			return outcome{}, err
		}
	} else {
		left, err := e.scope.resolve(in["left"])
		if err != nil {
			return outcome{}, err
		}
		right, err := e.scope.resolve(in["right"])
		if err != nil {
			return outcome{}, err
		}
		holds = compare(left, in["op"].(string), right)
	}
	return outcome{Port: strconv.FormatBool(holds), Output: map[string]any{"result": holds}}, nil
}

func runItemHasTerms(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.load(id)
	if err != nil {
		return outcome{}, err
	}
	resolved, err := e.scope.resolve(in["terms"])
	if err != nil {
		return outcome{}, err
	}
	list, _ := resolved.([]any)
	terms := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok || s == "" {
			return outcome{}, errors.New("terms must be term IDs")
		}
		terms = append(terms, s)
	}
	if len(terms) == 0 {
		return outcome{}, errors.New("terms is empty")
	}
	match := "any"
	if v, ok := in["match"]; ok {
		if match, err = e.scope.text(v); err != nil {
			return outcome{}, err
		}
		if match != "any" && match != "all" {
			return outcome{}, errors.New("match is any or all")
		}
	}
	values, err := e.t.ItemTerms(e.ctx, *r.ContainerID, r.Values)
	if err != nil {
		return outcome{}, err
	}
	held, err := e.t.MatchTerms(e.ctx, terms, values)
	if err != nil {
		return outcome{}, err
	}
	found := []any{}
	for _, t := range terms {
		if held[t] {
			found = append(found, t)
		}
	}
	matched := len(found) > 0
	if match == "all" {
		matched = len(found) == len(terms)
	}
	port := "unmatched"
	if matched {
		port = "matched"
	}
	return outcome{Port: port, Output: map[string]any{"matched": matched, "terms": found}}, nil
}

// filter resolves tokens in a filter's values and decodes it.
func (e *env) filter(raw any) (*dms.FilterExpr, error) {
	resolved, err := e.scope.resolve(raw)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	var f dms.FilterExpr
	if err = json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}
	return &f, nil
}

// matches tests filter against one item's head with the query engine, so
// conditions mean exactly what collection queries mean.
func matches(ctx context.Context, t *dms.Service, subject, collectionID, itemID string, filter *dms.FilterExpr) (bool, error) {
	id, _ := json.Marshal(itemID)
	f := dms.FilterExpr{And: []dms.FilterExpr{{Field: "$id", Op: "eq", Value: id}, *filter}}
	res, err := t.Query(ctx, subject, collectionID, dms.QueryRequest{Query: dms.QuerySpec{Filter: &f}, Surface: "head", Limit: 1})
	if err != nil {
		return false, err
	}
	return len(res.Data) == 1, nil
}

func compare(left any, op string, right any) bool {
	ls, rs := text2(left), text2(right)
	switch op {
	case "empty":
		return ls == ""
	case "not_empty":
		return ls != ""
	case "contains":
		return strings.Contains(strings.ToLower(ls), strings.ToLower(rs))
	}
	c := 0
	lf, lerr := strconv.ParseFloat(ls, 64)
	rf, rerr := strconv.ParseFloat(rs, 64)
	if lerr == nil && rerr == nil && !math.IsNaN(lf) && !math.IsNaN(rf) {
		switch {
		case lf < rf:
			c = -1
		case lf > rf:
			c = 1
		}
	} else {
		c = strings.Compare(ls, rs)
	}
	switch op {
	case "eq":
		return c == 0
	case "ne":
		return c != 0
	case "gt":
		return c > 0
	case "ge":
		return c >= 0
	case "lt":
		return c < 0
	case "le":
		return c <= 0
	}
	return false
}

func runSetVariable(e *env, in map[string]any) (outcome, error) {
	name, err := e.scope.text(in["name"])
	if err != nil {
		return outcome{}, err
	}
	if !inputNamePattern.MatchString(name) {
		return outcome{}, fmt.Errorf("invalid variable name %q", name)
	}
	v, err := e.scope.resolve(in["value"])
	if err != nil {
		return outcome{}, err
	}
	return outcome{Vars: map[string]any{name: v}, Output: map[string]any{"name": name, "value": v}}, nil
}

func runDelay(e *env, in map[string]any) (outcome, error) {
	var wait time.Duration
	if v, ok := in["duration"]; ok {
		s, err := e.scope.text(v)
		if err != nil {
			return outcome{}, err
		}
		if wait, err = time.ParseDuration(s); err != nil || wait < 0 {
			return outcome{}, fmt.Errorf("invalid duration %q", s)
		}
	} else {
		s, err := e.scope.text(in["until"])
		if err != nil {
			return outcome{}, err
		}
		until, err := time.Parse(time.RFC3339, s)
		if err != nil {
			if until, err = time.Parse("2006-01-02", s); err != nil {
				return outcome{}, fmt.Errorf("invalid until %q", s)
			}
		}
		wait = max(until.Sub(e.scope.now), 0)
	}
	until := e.scope.now.Add(wait).UTC().Format(time.RFC3339)
	return outcome{Sleep: wait.Seconds(), Output: map[string]any{"until": until}}, nil
}

func runRaise(e *env, in map[string]any) (outcome, error) {
	name, _ := in["event"].(string)
	data := map[string]any{}
	if v, ok := in["data"]; ok {
		r, err := e.scope.resolve(v)
		if err != nil {
			return outcome{}, err
		}
		m, ok := r.(map[string]any)
		if !ok {
			return outcome{}, errors.New("data must be an object")
		}
		data = m
	}
	e.raise(name, data)
	return outcome{Output: map[string]any{"event": name}}, nil
}

func runFail(e *env, in map[string]any) (outcome, error) {
	msg, err := e.scope.text(in["message"])
	if err != nil {
		return outcome{}, err
	}
	if msg == "" {
		msg = "the workflow failed"
	}
	return outcome{Fail: msg}, nil
}

func runItemGet(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.load(id)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: snapshot(r)}, nil
}

func (e *env) strings(v any) ([]string, error) {
	r, err := e.scope.resolve(v)
	if err != nil {
		return nil, err
	}
	switch t := r.(type) {
	case nil:
		return []string{}, nil
	case string:
		if t == "" {
			return []string{}, nil
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s := text2(x); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}
	return nil, errors.New("tags must be a list of text")
}

func (e *env) object(v any) (map[string]any, error) {
	r, err := e.scope.resolve(v)
	if err != nil {
		return nil, err
	}
	m, ok := r.(map[string]any)
	if !ok {
		return nil, errors.New("values must be an object")
	}
	return normalizeNumbers(m).(map[string]any), nil
}

// normalizeNumbers gives numbers the exact JSON form the DMS validators
// require (json.Number), whatever their in-memory type after tokens and JSON
// round trips.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case float64:
		return json.Number(strconv.FormatFloat(t, 'f', -1, 64))
	case int64:
		return json.Number(strconv.FormatInt(t, 10))
	case int:
		return json.Number(strconv.Itoa(t))
	case map[string]any:
		for k, x := range t {
			t[k] = normalizeNumbers(x)
		}
	case []any:
		for i, x := range t {
			t[i] = normalizeNumbers(x)
		}
	}
	return v
}

func runItemUpdate(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.t.Authorize(e.ctx, e.author, id, "write")
	if err != nil {
		return outcome{}, err
	}
	head, err := e.load(id)
	if err != nil {
		return outcome{}, err
	}
	var u dms.UpdateResource
	if v, ok := in["name"]; ok {
		name, err := e.scope.text(v)
		if err != nil {
			return outcome{}, err
		}
		u.Name = &name
	}
	if v, ok := in["tags"]; ok {
		tags, err := e.strings(v)
		if err != nil {
			return outcome{}, err
		}
		u.Tags = &tags
	}
	if v, ok := in["values"]; ok {
		changes, err := e.object(v)
		if err != nil {
			return outcome{}, err
		}
		values := map[string]any{}
		for k, x := range head.Values {
			values[k] = x
		}
		for k, x := range changes {
			if x == nil {
				delete(values, k)
			} else {
				values[k] = x
			}
		}
		u.Values = &values
	}
	if u.Name == nil && u.Tags == nil && u.Values == nil {
		return outcome{}, errors.New("item.update needs name, tags or values")
	}
	out, err := e.t.Update(e.ctx, e.author, id, r.Version, u)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: snapshot(out)}, nil
}

func runItemCreate(e *env, in map[string]any) (outcome, error) {
	collection, err := e.scope.text(in["collection_id"])
	if err != nil {
		return outcome{}, err
	}
	parent := collection
	if v, ok := in["parent_id"]; ok {
		if parent, err = e.scope.text(v); err != nil {
			return outcome{}, err
		}
	}
	c := dms.CreateResource{Kind: "item"}
	if c.Name, err = e.scope.text(in["name"]); err != nil {
		return outcome{}, err
	}
	if v, ok := in["content_type_id"]; ok {
		if c.ContentTypeID, err = e.scope.text(v); err != nil {
			return outcome{}, err
		}
	}
	if v, ok := in["tags"]; ok {
		if c.Tags, err = e.strings(v); err != nil {
			return outcome{}, err
		}
	}
	if v, ok := in["values"]; ok {
		if c.Values, err = e.object(v); err != nil {
			return outcome{}, err
		}
	}
	out, err := e.t.Create(e.ctx, e.author, parent, c)
	if err != nil {
		return outcome{}, err
	}
	if out.ContainerID == nil || *out.ContainerID != collection {
		return outcome{}, errors.New("parent_id must be in collection_id")
	}
	return outcome{Output: map[string]any{"item_id": out.ID, "item": snapshot(out)}}, nil
}

func runItemsQuery(e *env, in map[string]any) (outcome, error) {
	collection, err := e.scope.text(in["collection_id"])
	if err != nil {
		return outcome{}, err
	}
	q := dms.QueryRequest{Surface: "head", Limit: 50}
	if v, ok := in["filter"]; ok {
		if q.Query.Filter, err = e.filter(v); err != nil {
			return outcome{}, err
		}
	}
	if v, ok := in["sort"]; ok {
		r, err := e.scope.resolve(v)
		if err != nil {
			return outcome{}, err
		}
		raw, _ := json.Marshal(r)
		if err = json.Unmarshal(raw, &q.Query.Sort); err != nil {
			return outcome{}, fmt.Errorf("invalid sort: %w", err)
		}
	}
	if v, ok := in["limit"]; ok {
		r, err := e.scope.resolve(v)
		if err != nil {
			return outcome{}, err
		}
		n, err := strconv.Atoi(text2(r))
		if err != nil || n < 1 || n > 100 {
			return outcome{}, errors.New("limit must be 1 to 100")
		}
		q.Limit = n
	}
	res, err := e.t.Query(e.ctx, e.author, collection, q)
	if err != nil {
		return outcome{}, err
	}
	items := make([]any, len(res.Data))
	for i, r := range res.Data {
		items[i] = snapshot(r)
	}
	return outcome{Output: map[string]any{"items": items, "count": len(items)}}, nil
}

func runItemPublish(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.t.Authorize(e.ctx, e.author, id, "publish")
	if err != nil {
		return outcome{}, err
	}
	if r.PublishedRevisionID != nil && r.HeadRevisionID != nil && *r.PublishedRevisionID == *r.HeadRevisionID {
		return outcome{Output: map[string]any{"published": false}}, nil
	}
	p, err := e.t.Publish(e.ctx, e.author, id, r.Version)
	if err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"published": true, "publication_id": p.ID}}, nil
}

func runItemUnpublish(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.t.Authorize(e.ctx, e.author, id, "publish")
	if err != nil {
		return outcome{}, err
	}
	if r.PublishedRevisionID == nil {
		return outcome{Output: map[string]any{"unpublished": false}}, nil
	}
	if _, err = e.t.Unpublish(e.ctx, e.author, id, r.Version); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"unpublished": true}}, nil
}

func runItemDelete(e *env, in map[string]any) (outcome, error) {
	id, err := e.target(in)
	if err != nil {
		return outcome{}, err
	}
	r, err := e.t.Authorize(e.ctx, e.author, id, "write")
	if err != nil {
		return outcome{}, err
	}
	if err = e.t.Delete(e.ctx, e.author, id, r.Version); err != nil {
		return outcome{}, err
	}
	return outcome{Output: map[string]any{"deleted": true}}, nil
}
