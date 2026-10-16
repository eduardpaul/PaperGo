package dms

import (
	"context"
	"encoding/json"
	"time"

	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/resource"
)

// choiceWidening is a supported field-definition change, not a change to the
// accepted wire representation: current multiple choices still require arrays.
func choiceWidening(old, next *ent.FieldDefinition) bool {
	return old.Type == fielddefinition.TypeChoice && next.Type == old.Type && !old.Options.Multiple && next.Options.Multiple && old.Options.LookupContainerID == next.Options.LookupContainerID && old.Options.TermSetID == next.Options.TermSetID
}

// projectWidenedChoices maps a frozen single-choice snapshot into the current
// schema for adoption or restoration. Historical snapshots remain immutable.
func (s *Service) projectWidenedChoices(ctx context.Context, source *ent.ItemRevision, current []*ent.FieldDefinition, values map[string]any) (bool, error) {
	var old map[string]*ent.FieldDefinition
	changed := false
	for _, next := range current {
		if next.Type != fielddefinition.TypeChoice || !next.Options.Multiple {
			continue
		}
		value, scalar := values[next.Key].(string)
		if !scalar {
			continue
		}
		if old == nil {
			schema, err := s.schemaRevision(ctx, source.SchemaRevisionID)
			if err != nil {
				return false, err
			}
			defs, err := definitions(schema.Definition)
			if err != nil {
				return false, err
			}
			old = definitionMap(defs)
		}
		if before := old[next.Key]; before != nil && choiceWidening(before, next) {
			values[next.Key] = []any{value}
			changed = true
		}
	}
	return changed, nil
}

func (s *Service) adoptChoicePromotion(ctx context.Context, subject string, collection *ent.Resource, key string) error {
	for after := ""; ; {
		items, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(collection.ID), resource.KindIn(resource.KindItem, resource.KindFolder), resource.DeletedAtIsNil(), resource.IDGT(after)).Order(ent.Asc(resource.FieldID)).Limit(surfaceBatch).All(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			if _, scalar := item.Values[key].(string); !scalar {
				continue
			}
			if item.HeadRevisionID == nil {
				continue
			}
			if _, err = s.adoptWidenedChoiceHead(ctx, subject, item, true); err != nil {
				return err
			}
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}

// Adoption also runs when a deleted item returns to a collection whose choice
// field widened while it was in the recycle bin.
func (s *Service) adoptWidenedChoiceHead(ctx context.Context, subject string, item *ent.Resource, increment bool) (bool, error) {
	if item.HeadRevisionID == nil {
		return false, nil
	}
	head, err := s.Client.ItemRevision.Get(ctx, *item.HeadRevisionID)
	if err != nil {
		return false, err
	}
	collection, err := s.Client.Resource.Get(ctx, *item.ContainerID)
	if err != nil {
		return false, err
	}
	schema, err := s.schemaRevision(ctx, *collection.SchemaHeadID)
	if err != nil {
		return false, err
	}
	var definition SchemaDefinition
	if err = json.Unmarshal(schema.Definition, &definition); err != nil {
		return false, err
	}
	attached := false
	for _, typ := range definition.ContentTypes {
		attached = attached || typ.ID == head.ContentTypeID
	}
	if !attached {
		return false, nil
	}
	defs, _, err := typedDefinitions(schema.Definition, head.ContentTypeID)
	if err != nil {
		return false, err
	}
	values, err := decodeValues(head.Payload)
	if err != nil {
		return false, err
	}
	changed, err := s.projectWidenedChoices(ctx, head, defs, values)
	if err != nil || !changed {
		return false, err
	}
	if err = overlayRevision(item, head); err != nil {
		return false, err
	}
	item.Values = values
	if increment {
		if err = s.Client.Resource.UpdateOneID(item.ID).AddVersion(1).SetUpdatedBy(subject).SetUpdatedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return false, err
		}
		item.Version++
	}
	_, err = s.recordRevision(ctx, subject, item, nil, true)
	return true, err
}

func promoteChoiceDefault(old, next *ent.FieldDefinition) {
	if len(next.Options.DefaultValue) == 0 || string(old.Options.DefaultValue) != string(next.Options.DefaultValue) {
		return
	}
	var value string
	if json.Unmarshal(next.Options.DefaultValue, &value) == nil {
		next.Options.DefaultValue, _ = json.Marshal([]string{value})
	}
}
