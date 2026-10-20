package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"strconv"
	"time"
)

// Typed field filters are scoped to one collection, never arbitrary JSON.
func (s *Service) fieldFilter(ctx context.Context, subject string, in Browse) (*content.FieldMatch, error) {
	if in.ParentID == "" {
		return nil, invalid("field filters require parent_id for a list, library or folder")
	}
	parent, err := s.authorize(ctx, subject, in.ParentID, "read")
	if err != nil {
		return nil, err
	}
	container := parent.ID
	if parent.Kind == refs.KindFolder && parent.ContainerID != nil {
		container = *parent.ContainerID
	} else if parent.Kind != refs.KindList && parent.Kind != refs.KindLibrary {
		return nil, invalid("field filters require a collection scope")
	}
	d, err := s.DAL.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: container, Keys: []string{in.FilterField}})
	if err != nil {
		return nil, invalid("field filter references an unknown field")
	}
	if !queryable(d) {
		return nil, unqueryable(d.Key, d)
	}
	op := content.OpEQ
	switch in.FilterOp {
	case "", "eq":
	case "gt":
		op = content.OpGT
	case "gte":
		op = content.OpGTE
	case "lt":
		op = content.OpLT
	case "lte":
		op = content.OpLTE
	default:
		return nil, invalid("filter_op must be eq, gt, gte, lt or lte")
	}
	column := content.ValueText
	var value any = in.FilterValue
	switch d.Type {
	case catalog.FieldInteger:
		column = content.ValueInteger
		value, err = integer(json.Number(in.FilterValue))
	case catalog.FieldDecimal:
		column = content.ValueInteger
		_, value, err = decimal(in.FilterValue, d.Scale)
	case catalog.FieldNumber, catalog.FieldCurrency:
		value, err = numberKey(json.Number(in.FilterValue))
	case catalog.FieldBoolean:
		if op != content.OpEQ {
			return nil, invalid("boolean filters only support eq")
		}
		column = content.ValueBoolean
		value, err = strconv.ParseBool(in.FilterValue)
	case catalog.FieldDatetime:
		var date time.Time
		date, err = time.Parse(time.RFC3339, in.FilterValue)
		value = date.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if err != nil {
		return nil, invalid("invalid typed filter value")
	}
	// Field keys have one immutable type per collection.
	return &content.FieldMatch{ContainerID: container, Key: in.FilterField, Type: d.Type, Scale: d.Scale, Column: column, Op: op, Value: value}, nil
}
