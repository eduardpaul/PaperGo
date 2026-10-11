package dms

import (
	"context"
	"papergo/ent"
)

// Historical keys retain their catalog identity in item values and projections.
func (s *Service) validateFieldHistory(ctx context.Context, collection string, defs []*ent.FieldDefinition) error {
	known := definitionMap(defs)
	rows, e := s.Client.QueryContext(ctx, `SELECT DISTINCT json_extract(f.value,'$.id'),json_extract(f.value,'$.definition_id') FROM schema_revisions s,json_each(s.definition,'$.fields') f WHERE s.container_id=?`, collection)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var key, id string
		if e = rows.Scan(&key, &id); e != nil {
			return e
		}
		if d := known[key]; d != nil && d.ID != id {
			return invalid("field keys cannot be reused; use a new key: " + key)
		}
	}
	return rows.Err()
}
