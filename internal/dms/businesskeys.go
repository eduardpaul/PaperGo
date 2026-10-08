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
	unique := false
	for _, d := range defs {
		unique = unique || d.Options.Unique
	}
	if !unique {
		return nil
	}
	if _, err := s.Client.BusinessKey.Delete().Where(businesskey.ItemIDEQ(item)).Exec(ctx); err != nil {
		return err
	}
	surfaces, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(item)).All(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, surface := range surfaces {
		values, err := decodeValues(surface.Payload)
		if err != nil {
			return err
		}
		for _, d := range defs {
			if !d.Options.Unique || values[d.Key] == nil {
				continue
			}
			value, err := queryValue(d, values[d.Key])
			if err != nil {
				return err
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			key := d.Key + "\x00" + string(raw)
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, err := s.Client.BusinessKey.Create().SetContainerID(collection).SetItemID(item).SetFieldKey(d.Key).SetValue(string(raw)).Save(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) rebuildBusinessKeys(ctx context.Context, collection string) error {
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(collection)).All(ctx)
	if err != nil {
		return err
	}
	if _, err = s.Client.BusinessKey.Delete().Where(businesskey.ContainerIDEQ(collection)).Exec(ctx); err != nil {
		return err
	}
	for after := ""; ; {
		items, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(collection), resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil(), resource.IDGT(after)).Order(ent.Asc(resource.FieldID)).Select(resource.FieldID).Limit(surfaceBatch).All(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := s.claimBusinessKeys(ctx, item.ID, collection, defs); err != nil {
				return err
			}
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}
