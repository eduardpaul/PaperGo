package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
)

// schemaFieldIDs maps every field key a collection's schema revisions ever
// held to its catalog definition.
func (s *Service) schemaFieldIDs(ctx context.Context, collection string) (map[string]string, error) {
	schemas, err := s.DAL.Catalog().Schemas(ctx, catalog.SchemaFilter{ContainerID: collection})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, schema := range schemas {
		var definition SchemaDefinition
		if err = json.Unmarshal(schema.Definition, &definition); err != nil {
			return nil, err
		}
		for _, f := range definition.Fields {
			out[f.ID] = f.DefinitionID
		}
	}
	return out, nil
}

// Historical keys retain their catalog identity in item values and projections.
func (s *Service) validateFieldHistory(ctx context.Context, collection string, defs []*catalog.FieldDefinition) error {
	known := definitionMap(defs)
	held, err := s.schemaFieldIDs(ctx, collection)
	if err != nil {
		return err
	}
	for key, id := range held {
		if d := known[key]; d != nil && d.ID != id {
			return invalid("field keys cannot be reused; use a new key: " + key)
		}
	}
	return nil
}
