package dms

import (
	"context"
	"sort"

	"papergo/ent"
	"papergo/ent/itemhistory"
	"papergo/ent/itemrevision"
	"papergo/ent/resource"
)

type RevisionView struct {
	*ent.ItemRevision
	IsCurrent     bool     `json:"is_current"`
	ChangedFields []string `json:"changed_fields"`
}

func revisionChanges(current, previous *ent.ItemRevision) ([]string, error) {
	values, err := decodeValues(current.Payload)
	if err != nil {
		return nil, err
	}
	old := map[string]any{}
	if previous != nil {
		if old, err = decodeValues(previous.Payload); err != nil {
			return nil, err
		}
	}
	keys := map[string]bool{}
	for key := range values {
		keys[key] = true
	}
	for key := range old {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		value, exists := values[key]
		before, wasPresent := old[key]
		if exists != wasPresent || !smartEqual(value, before) {
			changed = append(changed, key)
		}
	}
	if previous == nil || current.Name != previous.Name {
		changed = append(changed, "$name")
	}
	if previous == nil || !smartEqual(current.Tags, previous.Tags) {
		changed = append(changed, "$tags")
	}
	if previous != nil && current.ContentTypeID != previous.ContentTypeID {
		changed = append(changed, "$content_type_id")
	}
	sort.Strings(changed)
	return changed, nil
}

func (s *Service) currentHistoryNumber(ctx context.Context, id string) (number int, err error) {
	err = s.scan(ctx, []any{&number}, "SELECT COALESCE(MAX(number),0) FROM item_histories WHERE item_id=?", id)
	return
}
func historyView(history *ent.ItemHistory, current int) RevisionView {
	revision := *history.Edges.Revision
	revision.RevisionNumber = history.Number
	revision.Edges.History = nil
	return RevisionView{&revision, history.Number == current, history.ChangedFields}
}
func (s *Service) RevisionHistory(ctx context.Context, subject, id string, after, limit int) (Page[RevisionView], error) {
	return read(ctx, s, func(t *Service) (Page[RevisionView], error) {
		item, err := t.authorize(ctx, subject, id, "read")
		if err != nil {
			return Page[RevisionView]{}, err
		}
		if item.Kind != resource.KindItem {
			return Page[RevisionView]{}, invalid("revisions belong to items")
		}
		if after < 0 {
			return Page[RevisionView]{}, invalid("history cursor must be a positive recorded version number")
		}
		query := t.Client.ItemHistory.Query().Where(itemhistory.ItemIDEQ(id))
		if after > 0 {
			query.Where(itemhistory.NumberLT(after))
		}
		rows, err := query.WithRevision(func(q *ent.ItemRevisionQuery) { q.WithSchemaRevision() }).Order(ent.Desc(itemhistory.FieldNumber)).Limit(pageSize(limit) + 1).All(ctx)
		if err != nil {
			return Page[RevisionView]{}, err
		}
		current, err := t.currentHistoryNumber(ctx, id)
		if err != nil {
			return Page[RevisionView]{}, err
		}
		out := make([]RevisionView, 0, len(rows))
		for _, row := range rows {
			out = append(out, historyView(row, current))
		}
		return numberedPage(out, limit, func(v RevisionView) int { return v.RevisionNumber }), nil
	})
}

func (s *Service) Revision(ctx context.Context, subject, id string, number int) (RevisionView, error) {
	return read(ctx, s, func(t *Service) (RevisionView, error) {
		item, err := t.authorize(ctx, subject, id, "read")
		if err != nil {
			return RevisionView{}, err
		}
		if item.Kind != resource.KindItem || number < 1 {
			return RevisionView{}, ErrNotFound
		}
		history, err := t.Client.ItemHistory.Query().Where(itemhistory.ItemIDEQ(id), itemhistory.NumberEQ(number)).WithRevision(func(q *ent.ItemRevisionQuery) { q.WithSchemaRevision() }).Only(ctx)
		if ent.IsNotFound(err) {
			return RevisionView{}, ErrNotFound
		}
		if err != nil {
			return RevisionView{}, err
		}
		current, err := t.currentHistoryNumber(ctx, id)
		if err != nil {
			return RevisionView{}, err
		}
		return historyView(history, current), nil
	})
}

// RestoreRevision restores metadata through the current mutation pipeline.
// File content is retained; restoring a blob is a separate content operation.
func (s *Service) RestoreRevision(ctx context.Context, subject, id string, number, version int) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		item, err := t.authorize(ctx, subject, id, "write")
		if err != nil {
			return err
		}
		if item.Kind != resource.KindItem || number < 1 {
			return ErrNotFound
		}
		if item.Version != version {
			return ErrPreconditionFailed
		}
		old, err := t.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.HasHistoryWith(itemhistory.NumberEQ(number))).Only(ctx)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err = t.overlayHead(ctx, item); err != nil {
			return err
		}
		values, err := decodeValues(old.Payload)
		if err != nil {
			return err
		}
		collection, err := t.Client.Resource.Get(ctx, *item.ContainerID)
		if err != nil {
			return err
		}
		schema, err := t.Client.SchemaRevision.Get(ctx, *collection.SchemaHeadID)
		if err != nil {
			return err
		}
		defs, _, err := typedDefinitions(schema.Definition, old.ContentTypeID)
		if err != nil {
			return err
		}
		if _, err = t.projectWidenedChoices(ctx, old, defs, values); err != nil {
			return err
		}
		// Removed fields stay removed. Clear current fields absent from the snapshot.
		known := definitionMap(defs)
		for key := range values {
			if known[key] == nil {
				delete(values, key)
			}
		}
		for key := range item.Values {
			if _, exists := values[key]; !exists && known[key] != nil {
				values[key] = nil
			}
		}
		if err = t.restoreIntegrationData(ctx, old.ID, id); err != nil {
			return err
		}
		out, err = t.update(ctx, subject, id, version, UpdateResource{Name: &old.Name, Tags: &old.Tags, Values: &values, ContentTypeID: &old.ContentTypeID})
		if err != nil {
			return err
		}
		return t.audit(ctx, subject, "item.restore_revision", out, map[string]any{"restored_revision_number": number, "revision_id": *out.HeadRevisionID})
	})
	return
}
