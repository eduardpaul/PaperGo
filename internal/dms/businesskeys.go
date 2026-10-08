package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/businesskey"
	"papergo/ent/fielddefinition"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
)

// Claims cover both live surfaces. A draft rename cannot free a business key
// that readers still see on the published revision of another item.
func (s *Service) syncBusinessKeys(ctx context.Context, r *ent.Resource) error {
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(*r.ContainerID)).All(ctx)
	if err != nil {
		return err
	}
	return s.claimBusinessKeys(ctx, r.ID, *r.ContainerID, defs)
}

func (s *Service) claimBusinessKeys(ctx context.Context, item, collection string, defs []*ent.FieldDefinition) error {
	defs = uniqueDefinitions(defs)
	if len(defs) == 0 {
		return nil
	}
	if _, err := s.Client.BusinessKey.Delete().Where(businesskey.ItemIDEQ(item)).Exec(ctx); err != nil {
		return err
	}
	surfaces, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(item)).All(ctx)
	if err != nil {
		return err
	}
	rows, err := s.businessKeyRows(collection, surfaces, defs)
	if err != nil {
		return err
	}
	return s.Client.BusinessKey.CreateBulk(rows...).Exec(ctx)
}

func uniqueDefinitions(defs []*ent.FieldDefinition) []*ent.FieldDefinition {
	out := []*ent.FieldDefinition{}
	for _, d := range defs {
		if d.Options.Unique {
			out = append(out, d)
		}
	}
	return out
}

func (s *Service) businessKeyRows(collection string, surfaces []*ent.ItemSurface, defs []*ent.FieldDefinition) ([]*ent.BusinessKeyCreate, error) {
	seen := map[string]bool{}
	rows := []*ent.BusinessKeyCreate{}
	for _, surface := range surfaces {
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
			rows = append(rows, s.Client.BusinessKey.Create().SetContainerID(collection).SetItemID(surface.ItemID).SetFieldKey(d.Key).SetValue(string(raw)))
		}
	}
	return rows, nil
}

func (s *Service) rebuildBusinessKeys(ctx context.Context, collection string) error {
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(collection)).All(ctx)
	if err != nil {
		return err
	}
	if _, err = s.Client.BusinessKey.Delete().Where(businesskey.ContainerIDEQ(collection)).Exec(ctx); err != nil {
		return err
	}
	defs = uniqueDefinitions(defs)
	if len(defs) == 0 {
		return nil
	}
	for after := ""; ; {
		items, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(collection), resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil(), resource.IDGT(after)).Order(ent.Asc(resource.FieldID)).Select(resource.FieldID).Limit(surfaceBatch).All(ctx)
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
		surfaces, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDIn(ids...)).All(ctx)
		if err != nil {
			return err
		}
		byItem := map[string][]*ent.ItemSurface{}
		for _, surface := range surfaces {
			byItem[surface.ItemID] = append(byItem[surface.ItemID], surface)
		}
		// Decode at most one item's two surfaces at a time and flush bounded
		// inserts. Rebuilding never issues a query or delete per item.
		rows := []*ent.BusinessKeyCreate{}
		for _, item := range items {
			built, err := s.businessKeyRows(collection, byItem[item.ID], defs)
			if err != nil {
				return err
			}
			rows = append(rows, built...)
			for len(rows) >= 500 {
				if err := s.Client.BusinessKey.CreateBulk(rows[:500]...).Exec(ctx); err != nil {
					return err
				}
				rows = rows[500:]
			}
		}
		if err := s.Client.BusinessKey.CreateBulk(rows...).Exec(ctx); err != nil {
			return err
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}
