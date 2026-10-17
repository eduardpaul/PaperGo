package workflow

import (
	"context"
	"papergo/internal/dms"
)

type ExplainInput struct {
	ItemID    string `json:"item_id"`
	EventType string `json:"event_type"`
	Source    string `json:"source,omitempty"`
}
type Explanation struct {
	Eligible bool   `json:"eligible"`
	Code     string `json:"code"`
	Reason   string `json:"reason"`
}

// Explain evaluates subscriptions and conditions without raising events or running activities.
func (s *Service) Explain(ctx context.Context, subject, id string, in ExplainInput) (Explanation, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Explanation, error) {
		w, err := live(ctx, t, id)
		if err != nil {
			return Explanation{}, err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "read"); err != nil {
			return Explanation{}, err
		}
		item, err := t.Authorize(ctx, subject, in.ItemID, "read_draft")
		if err != nil {
			return Explanation{}, err
		}
		if item.WorkspaceID != w.WorkspaceID || item.ContainerID == nil {
			return Explanation{}, dms.Invalid("Choose an item in this workspace.")
		}
		if in.EventType != dms.EventFileAdded && in.EventType != dms.EventFileUpdated && in.EventType != dms.EventItemUpdated {
			return Explanation{}, dms.Invalid("Check file.added, file.updated or item.updated.")
		}
		if w.State != "published" || !w.AutomaticEnabled {
			return Explanation{Code: "automatic_off", Reason: "Automatic runs are off. Manual use is independent."}, nil
		}
		if w.Role != nil && (w.CollectionID == nil || *w.CollectionID != *item.ContainerID || !w.AutomaticProvider) {
			return Explanation{Code: "other_scope", Reason: "This workflow is not the automatic provider for this library."}, nil
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return Explanation{}, err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return Explanation{}, err
		}
		source := in.Source
		if source == "" {
			source = "upload"
		}
		data := map[string]any{"source": source}
		if item.ContentTypeID != nil {
			data["content_type_id"] = *item.ContentTypeID
		}
		if in.EventType == dms.EventFileAdded || in.EventType == dms.EventFileUpdated {
			blob, err := t.CurrentFile(ctx, subject, item.ID)
			if err != nil {
				return Explanation{Code: "no_file", Reason: "This item has no readable current file."}, nil
			}
			data["content_type"] = blob.ContentType
		}
		event := dms.Event{Type: in.EventType, WorkspaceID: w.WorkspaceID, CollectionID: *item.ContainerID, ResourceID: item.ID, Data: data}
		m := matchable{w: w, v: v, def: def}
		ok, err := m.accepts(ctx, t, event)
		if err != nil {
			return Explanation{}, err
		}
		if !ok {
			return Explanation{Code: "no_matching_rule", Reason: "The event, file source, file type or library does not match an active automatic rule."}, nil
		}
		if def.Condition != nil {
			ok, e := matches(ctx, t, v.CreatedBy, *item.ContainerID, item.ID, def.Condition)
			if e != nil {
				return Explanation{Code: "condition_unavailable", Reason: "The workflow cannot evaluate its condition with the execution author's current permissions."}, nil
			}
			if !ok {
				return Explanation{Code: "condition_not_met", Reason: "This item does not meet the workflow's condition."}, nil
			}
		}
		return Explanation{Eligible: true, Code: "eligible", Reason: "Eligible when this event occurs. Admission, permissions and concurrency are checked again at execution."}, nil
	})
}
