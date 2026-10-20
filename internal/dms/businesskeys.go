package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
)

// Claims cover both live surfaces. A draft rename cannot free a business key
// that readers still see on the published revision of another item.
func (s *Service) syncBusinessKeys(ctx context.Context, r *resources.Resource) error {
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: *r.ContainerID})
	if err != nil {
		return err
	}
	return s.claimBusinessKeys(ctx, r.ID, *r.ContainerID, defs)
}

func (s *Service) claimBusinessKeys(ctx context.Context, item, collection string, defs []*catalog.FieldDefinition) error {
	defs = uniqueDefinitions(defs)
	if len(defs) == 0 {
		return nil
	}
	if err := s.DAL.Catalog().ReleaseKeys(ctx, catalog.KeyFilter{ItemID: item}); err != nil {
		return err
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemID: item})
	if err != nil {
		return err
	}
	keys, err := businessKeys(collection, projections, defs)
	if err != nil {
		return err
	}
	return s.DAL.Catalog().ClaimKeys(ctx, keys)
}

func uniqueDefinitions(defs []*catalog.FieldDefinition) []*catalog.FieldDefinition {
	out := []*catalog.FieldDefinition{}
	for _, d := range defs {
		if d.Options.Unique {
			out = append(out, d)
		}
	}
	return out
}

func businessKeys(collection string, projections []surfaces.Surface, defs []*catalog.FieldDefinition) ([]catalog.BusinessKey, error) {
	seen := map[string]bool{}
	rows := []catalog.BusinessKey{}
	for _, surface := range projections {
		values, err := decodeValues(surface.Payload)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			if values[d.Key] == nil {
				continue
			}
			value, err := queryValue(d, values[d.Key])
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			key := surface.ItemID + "\x00" + d.Key + "\x00" + string(raw)
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, catalog.BusinessKey{CollectionID: collection, ItemID: surface.ItemID, FieldKey: d.Key, Value: string(raw)})
		}
	}
	return rows, nil
}

func (s *Service) rebuildBusinessKeys(ctx context.Context, collection string) error {
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: collection})
	if err != nil {
		return err
	}
	if err = s.DAL.Catalog().ReleaseKeys(ctx, catalog.KeyFilter{CollectionID: collection}); err != nil {
		return err
	}
	defs = uniqueDefinitions(defs)
	if len(defs) == 0 {
		return nil
	}
	for after := ""; ; {
		items, err := s.DAL.Resources().Resources(ctx, resources.Filter{ContainerID: collection, Kinds: ContentKinds, State: resources.Live, AfterID: after, Limit: surfaceBatch})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.ID
		}
		projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemIDs: ids})
		if err != nil {
			return err
		}
		byItem := map[string][]surfaces.Surface{}
		for _, surface := range projections {
			byItem[surface.ItemID] = append(byItem[surface.ItemID], surface)
		}
		// Decode at most one item's two surfaces at a time and claim one
		// page's keys together. Rebuilding never issues a query or delete
		// per item.
		keys := []catalog.BusinessKey{}
		for _, item := range items {
			built, err := businessKeys(collection, byItem[item.ID], defs)
			if err != nil {
				return err
			}
			keys = append(keys, built...)
		}
		if err := s.DAL.Catalog().ClaimKeys(ctx, keys); err != nil {
			return err
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}
