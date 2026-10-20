package dms

import (
	"context"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"reflect"
	"slices"

	"papergo/internal/dal/taxonomy"
)

func termState(t taxonomy.Term) TermInput {
	return TermInput{Name: t.Name, ParentID: optional(t.ParentID), Description: t.Description, Color: optional(t.Color),
		SortOrder: t.SortOrder, Labels: t.Labels, Synonyms: t.Synonyms,
		Deprecated: t.Deprecated, AvailableAsKeyword: t.AvailableAsKeyword}
}

func (s *Service) emitTermChanged(ctx context.Context, actor string, previous, current taxonomy.Term) error {
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
	workspaces, err := s.DAL.Resources().IDs(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, State: resources.Live})
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaces {
		s.emit(ctx, EventTermChanged, actor, workspaceID, nil, "", map[string]any{
			"term_id": current.ID, "term_set_id": current.SetID, "version": current.Version,
			"changed_fields": changed, "previous": before, "current": after,
		})
	}
	return nil
}
