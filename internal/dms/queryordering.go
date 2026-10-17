package dms

import (
	"context"
	"papergo/ent"
	"strconv"
)

type queryOrder struct {
	kind       string
	descending bool
}

func sortAlias(i int) string {
	if i == 0 {
		return "sort_value"
	}
	return "sort_value_" + strconv.Itoa(i)
}

// A multi-key page carries every typed rank into its cursor. A single key can
// use the index-driven scan, with the same ordering and cursor semantics.
func (s *Service) queryRankedIDs(ctx context.Context, q compiledQuery, query string, args ...any) ([]rankedID, error) {
	if len(q.orders) <= 1 {
		return s.rankedIDs(ctx, query, args...)
	}
	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rankedID{}
	for rows.Next() {
		var id string
		values := make([]any, len(q.orders))
		targets := make([]any, len(values)+1)
		targets[0] = &id
		for i := range values {
			targets[i+1] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			return nil, err
		}
		out = append(out, rankedID{id: id, rank: values})
	}
	return out, rows.Err()
}

func querySelection(defs map[string]*ent.FieldDefinition, selection []string) ([]string, bool, error) {
	if selection == nil {
		return nil, true, nil
	}
	if len(selection) > 50 {
		return nil, false, invalid("at most 50 selected fields")
	}
	fields := []string{}
	keywords := false
	seen := map[string]bool{}
	for _, key := range selection {
		if seen[key] || !builtinColumn(key) && defs[key] == nil {
			return nil, false, invalid("select must contain unique known fields")
		}
		seen[key] = true
		if key == "$keywords" {
			keywords = true
		} else if !builtinColumn(key) {
			fields = append(fields, key)
		}
	}
	return fields, keywords, nil
}
