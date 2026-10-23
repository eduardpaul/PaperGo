package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"papergo/extensions/mcp"
)

// Field types and index states as the API names them.
const (
	fieldTerm     = "term"
	fieldKeywords = "keywords"
	fieldPerson   = "person"
	fieldLookup   = "lookup"
	indexReady    = "ready"
)

// Batch sizes of the API's ID lookups, and how many people a page names.
const (
	termBatch   = 200
	lookupBatch = 50
	maxPeople   = 50
)

// field is what the tools read of a collection's field definition.
type field struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Indexed     bool   `json:"indexed"`
	IndexStatus string `json:"index_status"`
	Options     struct {
		LookupContainerID string `json:"lookup_container_id"`
		TermSetID         string `json:"term_set_id"`
	} `json:"options"`
}

// fields reads a collection's field definitions.
func fields(ctx context.Context, c mcp.Caller, collectionID string) ([]field, error) {
	var out struct {
		Value []field `json:"value"`
	}
	err := get(ctx, c, resourcePath(collectionID, "fields"), nil, &out)
	return out.Value, err
}

// queryable lists the custom fields queries may filter and sort by.
func queryable(fs []field) []string {
	out := []string{}
	for _, f := range fs {
		if f.Indexed && f.IndexStatus == indexReady {
			out = append(out, f.Key)
		}
	}
	return out
}

// skippable reports whether a lookup that only adds names may be left out:
// the API answered but refused, such as for a missing scope or access.
func skippable(err error) bool {
	var api *mcp.APIError
	return errors.As(err, &api)
}

// label names the IDs the entries of one collection hold: their keywords,
// creators and editors, and the terms, people and linked items of their
// fields. It asks the API in a few batched requests, as the caller, and
// leaves out what the caller may not read; an unreadable name is never a
// failure.
func label(ctx context.Context, c mcp.Caller, collectionID *string, entries []item) error {
	if len(entries) == 0 {
		return nil
	}
	var fs []field
	if collectionID != nil {
		var err error
		if fs, err = fields(ctx, c, *collectionID); err != nil && !skippable(err) {
			return err
		}
	}
	held := make([][]string, len(entries))
	var terms, people []string
	lookups := map[string][]string{}
	for i, e := range entries {
		var values map[string]json.RawMessage
		_ = json.Unmarshal(e.Values, &values)
		held[i] = append(held[i], e.Keywords...)
		terms = append(terms, e.Keywords...)
		held[i] = append(held[i], e.CreatedBy, e.UpdatedBy)
		people = append(people, e.CreatedBy, e.UpdatedBy)
		for _, f := range fs {
			ids := idsOf(values[f.Key])
			held[i] = append(held[i], ids...)
			switch f.Type {
			case fieldTerm, fieldKeywords:
				terms = append(terms, ids...)
			case fieldPerson:
				people = append(people, ids...)
			case fieldLookup:
				if f.Options.LookupContainerID != "" {
					lookups[f.Options.LookupContainerID] = append(lookups[f.Options.LookupContainerID], ids...)
				}
			}
		}
	}
	names := map[string]string{}
	steps := []func() error{
		func() error { return termNames(ctx, c, distinct(terms), names) },
		func() error { return peopleNames(ctx, c, distinct(people), names) },
	}
	for container, ids := range lookups {
		steps = append(steps, func() error { return entryNames(ctx, c, container, distinct(ids), names) })
	}
	for _, step := range steps {
		if err := step(); err != nil && !skippable(err) {
			return err
		}
	}
	for i := range entries {
		for _, id := range held[i] {
			if name, ok := names[id]; ok {
				entries[i].Labels[id] = name
			}
		}
	}
	return nil
}

// idsOf reads a field value of IDs: one or an array of them.
func idsOf(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

func distinct(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	return out
}

func termNames(ctx context.Context, c mcp.Caller, ids []string, names map[string]string) error {
	if len(ids) == 0 || !c.HasScope(scopeTaxonomyRead) {
		return nil
	}
	for batch := range slices.Chunk(ids, termBatch) {
		var page struct {
			Value []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"value"`
		}
		if err := get(ctx, c, "/v1/terms", url.Values{"ids": {strings.Join(batch, ",")}}, &page); err != nil {
			return err
		}
		for _, t := range page.Value {
			names[t.ID] = t.Name
		}
	}
	return nil
}

func peopleNames(ctx context.Context, c mcp.Caller, subjects []string, names map[string]string) error {
	if len(subjects) == 0 || !c.HasScope(scopeUserRead) {
		return nil
	}
	if len(subjects) > maxPeople {
		subjects = subjects[:maxPeople]
	}
	for _, subject := range subjects {
		var profile struct {
			DisplayName string `json:"display_name"`
		}
		err := get(ctx, c, "/v1/identity/users/"+url.PathEscape(subject), nil, &profile)
		switch {
		case skippable(err):
		case err != nil:
			return err
		case profile.DisplayName != "":
			names[subject] = profile.DisplayName
		}
	}
	return nil
}

// entryNames names linked items of a collection with one query per batch.
func entryNames(ctx context.Context, c mcp.Caller, collectionID string, ids []string, names map[string]string) error {
	for batch := range slices.Chunk(ids, lookupBatch) {
		values, _ := json.Marshal(batch)
		body := json.RawMessage(`{"query": {"filter": {"field": "$id", "op": "in", "value": ` + string(values) + `}}, "select": []}`)
		res, err := c.Backend().Do(ctx, mcp.Request{Method: http.MethodPost, Path: resourcePath(collectionID, "query"),
			Query: url.Values{"$top": {"50"}}, Body: body})
		if err != nil {
			return err
		}
		var pg page
		if err := unmarshal(res.Body, &pg); err != nil {
			return err
		}
		rs, err := listed(pg, nil)
		if err != nil {
			return err
		}
		for _, r := range rs {
			names[r.ID] = r.Name
		}
	}
	return nil
}
