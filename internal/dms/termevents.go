package dms

import (
	"context"
	"reflect"
	"slices"

	"papergo/ent"
	"papergo/ent/resource"
)

func termState(t *ent.Term) TermInput {
	return TermInput{Name: t.Name, ParentID: t.ParentID, Description: t.Description, Color: t.Color,
		SortOrder: t.SortOrder, Labels: t.Labels, Synonyms: t.Synonyms,
		Deprecated: t.Deprecated, AvailableAsKeyword: t.AvailableAsKeyword}
}

func (s *Service) emitTermChanged(ctx context.Context, actor string, previous, current *ent.Term) error {
	before, after := termState(previous), termState(current)
	fields := map[string][2]any{
		"name": {before.Name, after.Name}, "description": {before.Description, after.Description},
		"color": {before.Color, after.Color}, "sort_order": {before.SortOrder, after.SortOrder},
		"labels": {before.Labels, after.Labels}, "synonyms": {before.Synonyms, after.Synonyms},
		"deprecated": {before.Deprecated, after.Deprecated}, "available_as_keyword": {before.AvailableAsKeyword, after.AvailableAsKeyword},
	}
	changed := []string{}
	for field, values := range fields {
		if !reflect.DeepEqual(values[0], values[1]) {
			changed = append(changed, field)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	slices.Sort(changed)
	workspaces, err := s.Client.Resource.Query().Where(resource.KindEQ(resource.KindWorkspace), resource.DeletedAtIsNil()).IDs(ctx)
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaces {
		s.emit(ctx, EventTermChanged, actor, workspaceID, nil, "", map[string]any{
			"term_id": current.ID, "term_set_id": current.TermSetID, "version": current.Version,
			"changed_fields": changed, "previous": before, "current": after,
		})
	}
	return nil
}
