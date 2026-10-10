package dms

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent/fielddefinition"
	"papergo/ent/resource"
	"strconv"
	"time"
)

// Typed field filters are scoped to one collection, never arbitrary JSON SQL.
func (s *Service) fieldFilter(ctx context.Context, subject string, in Browse) (func(*entsql.Selector), candidateSet, error) {
	if in.ParentID == "" {
		return nil, candidateSet{}, invalid("field filters require parent_id for a list, library or folder")
	}
	parent, err := s.authorize(ctx, subject, in.ParentID, "read")
	if err != nil {
		return nil, candidateSet{}, err
	}
	container := parent.ID
	if parent.Kind == resource.KindFolder && parent.ContainerID != nil {
		container = *parent.ContainerID
	} else if parent.Kind != resource.KindList && parent.Kind != resource.KindLibrary {
		return nil, candidateSet{}, invalid("field filters require a collection scope")
	}
	d, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(container), fielddefinition.KeyEQ(in.FilterField)).Only(ctx)
	if err != nil {
		return nil, candidateSet{}, invalid("field filter references an unknown field")
	}
	if !queryable(d) {
		return nil, candidateSet{}, unqueryable(d.Key, d)
	}
	op := "="
	switch in.FilterOp {
	case "", "eq":
	case "gt":
		op = ">"
	case "gte":
		op = ">="
	case "lt":
		op = "<"
	case "lte":
		op = "<="
	default:
		return nil, candidateSet{}, invalid("filter_op must be eq, gt, gte, lt or lte")
	}
	column := "value_text"
	var value any = in.FilterValue
	switch string(d.Type) {
	case "integer":
		column = "value_integer"
		value, err = integer(json.Number(in.FilterValue))
	case "decimal":
		column = "value_integer"
		_, value, err = decimal(in.FilterValue, d.Scale)
	case "number":
		column = "value_number"
		value, err = number(json.Number(in.FilterValue))
	case "boolean":
		if op != "=" {
			return nil, candidateSet{}, invalid("boolean filters only support eq")
		}
		column = "value_boolean"
		value, err = strconv.ParseBool(in.FilterValue)
	case "datetime":
		var date time.Time
		date, err = time.Parse(time.RFC3339, in.FilterValue)
		value = date.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if err != nil {
		return nil, candidateSet{}, invalid("invalid typed filter value")
	}
	// Either surface may match; the exact predicate checks the selected one. Field
	// keys have one immutable type per collection, so type and scale can be omitted.
	candidate := `SELECT f.item_id FROM field_values f WHERE f.container_id=? AND f.surface IN ('head','published') AND f.field_key=? AND f.` + column + op + `?`
	candidateArgs := []any{container, in.FilterField, value}
	return func(sel *entsql.Selector) {
		selected, args := surfaceSQL(sel.C(resource.FieldID), subject, in.Surface)
		query := `EXISTS (SELECT 1 FROM field_values f WHERE f.container_id=? AND f.item_id=` + sel.C(resource.FieldID) + ` AND f.surface=` + selected + ` AND f.field_key=? AND f.field_type=? AND f.scale=? AND f.` + column + op + `?)`
		values := []any{container}
		values = append(values, args...)
		values = append(values, in.FilterField, string(d.Type), d.Scale, value)
		sel.Where(entsql.ExprP(query, values...))
	}, candidateSet{sql: candidate, args: candidateArgs, estimate: candidate, estimateArgs: candidateArgs}, nil
}
