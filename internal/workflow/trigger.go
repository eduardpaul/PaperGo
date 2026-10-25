package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dms"
)

// MaxTriggerBytes bounds the metadata copied into each run. File bytes,
// permissions, relationships and history are never part of a snapshot.
const MaxTriggerBytes = 64 << 10

// TriggerObject is the content that caused a run. Kind discriminates manual
// selections, calls, items, folders and files. The SDK adds manual and call
// input and the primary item alias from the run payload, without persisting
// another copy. A call carries no snapshot, so a run that calls a workflow
// on many items stays small: the called run reads its item.
type TriggerObject struct {
	Kind string `json:"kind"`
	// ParentRunID is the run that called this one.
	ParentRunID string             `json:"parent_run_id,omitempty"`
	Item        *TriggerResource   `json:"item,omitempty"`
	Folder      *TriggerResource   `json:"folder,omitempty"`
	File        *TriggerFile       `json:"file,omitempty"`
	Items       []TriggerSelection `json:"items,omitempty"`
}

// TriggerSelection preserves every selected ID in launch order, including
// items whose content the execution author cannot read at dispatch.
type TriggerSelection struct {
	ID       string           `json:"id"`
	Snapshot *TriggerResource `json:"snapshot,omitempty"`
}

// TriggerResource contains revision content, without mutable resource state
// such as an ETag or parent. Writes must obtain a current ETag through the API.
type TriggerResource struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	CollectionID   string          `json:"collection_id"`
	RevisionID     string          `json:"revision_id"`
	RevisionNumber int             `json:"revision_number"`
	ContentTypeID  string          `json:"content_type_id"`
	Name           string          `json:"name"`
	Keywords       []string        `json:"keywords"`
	Values         json.RawMessage `json:"values,omitempty"`
	ValuesOmitted  bool            `json:"values_omitted"`
}

// TriggerFile describes the triggering file version, never its stored bytes
// or internal object key.
type TriggerFile struct {
	ID          string          `json:"id"`
	Version     int             `json:"version"`
	Filename    string          `json:"filename"`
	ContentType blobs.MediaType `json:"content_type"`
	Size        int64           `json:"size"`
}

// triggerObject runs in the dispatch transaction, while queued events still
// retain their revisions. Missing/inaccessible content has no snapshot; this
// must not grant an event consumer access to another author's draft.
func triggerObject(ctx context.Context, t *dms.Service, author string, e dms.Event) (*TriggerObject, error) {
	if e.Type == TriggerCall {
		parent, _ := e.Data["parent_run_id"].(string)
		return &TriggerObject{Kind: TriggerCall, ParentRunID: parent}, nil
	}
	if e.Type == TriggerManual || e.Type == TestStart {
		object := &TriggerObject{Kind: TriggerManual, Items: []TriggerSelection{}}
		for _, id := range eventMembers(e) {
			member := e
			member.ResourceID, member.Data = id, nil
			content, err := contentTriggerObject(ctx, t, author, member)
			if err != nil {
				return nil, err
			}
			selected := TriggerSelection{ID: id}
			if content != nil {
				selected.Snapshot = content.Item
			}
			object.Items = append(object.Items, selected)
		}
		if err := boundTrigger(object); err != nil {
			return nil, err
		}
		return object, nil
	}
	return contentTriggerObject(ctx, t, author, e)
}

func contentTriggerObject(ctx context.Context, t *dms.Service, author string, e dms.Event) (*TriggerObject, error) {
	if eventItemID(e) == "" {
		return nil, nil
	}
	r, err := t.Authorize(ctx, author, e.ResourceID, "read")
	if errors.Is(err, dms.ErrNotFound) || errors.Is(err, dms.ErrForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !r.Content() || r.WorkspaceID != e.WorkspaceID {
		return nil, nil
	}
	canReadDraft := true
	if r.Kind == refs.KindItem {
		allowed, err := t.AuthorizeMany(ctx, author, "read_draft", []string{r.ID})
		if err != nil {
			return nil, err
		}
		canReadDraft = allowed[r.ID]
	}
	revisionID, _ := e.Data["revision_id"].(string)
	if revisionID == "" {
		// Manual, schedule, test and other events without a revision capture
		// the readable content at dispatch, once, rather than on each resume.
		selected := r.HeadRevisionID
		if !canReadDraft || selected == nil || e.Type == TriggerSchedule && e.Data["surface"] == string(refs.SurfacePublished) {
			selected = r.PublishedRevisionID
		}
		if selected == nil {
			return nil, nil
		}
		revisionID = *selected
	}
	if !canReadDraft && (r.PublishedRevisionID == nil || *r.PublishedRevisionID != revisionID) {
		return nil, nil
	}
	rev, err := t.DAL.Revisions().Revision(ctx, revisionID)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rev.ItemID != r.ID {
		return nil, fmt.Errorf("trigger revision does not belong to resource %s", r.ID)
	}
	resource := &TriggerResource{
		ID: r.ID, WorkspaceID: r.WorkspaceID, CollectionID: rev.ContainerID,
		RevisionID: rev.ID, RevisionNumber: rev.RevisionNumber, ContentTypeID: rev.ContentTypeID,
		Name: rev.Name, Keywords: append([]string{}, rev.Keywords...), Values: rev.Payload,
	}
	if len(resource.Values) == 0 || string(resource.Values) == "null" {
		resource.Values = json.RawMessage(`{}`)
	}
	if len(resource.Values) > MaxTriggerBytes {
		resource.Values = nil
		resource.ValuesOmitted = true
	}
	object := &TriggerObject{Kind: string(r.Kind)}
	if r.Kind == refs.KindFolder {
		object.Folder = resource
	} else {
		object.Item = resource
	}
	if (e.Type == dms.EventFileAdded || e.Type == dms.EventFileUpdated) && rev.BlobID != nil {
		b, err := t.DAL.Blobs().ItemBlob(ctx, r.ID, *rev.BlobID)
		if err != nil {
			return nil, err
		}
		object.Kind = "file"
		object.File = &TriggerFile{ID: b.ID, Version: b.Version, Filename: b.Filename, ContentType: b.ContentType, Size: b.Size}
	}
	if err := boundTrigger(object); err != nil {
		return nil, err
	}
	return object, nil
}

// boundTrigger applies one budget to the complete selection, not per item.
func boundTrigger(object *TriggerObject) error {
	raw, err := json.Marshal(object)
	if err != nil {
		return err
	}
	if len(raw) > MaxTriggerBytes {
		// Large field payloads remain behind the API rather than bloating every
		// run or preventing workflows that only need identity/file metadata.
		omit := func(resource *TriggerResource) {
			if resource != nil {
				resource.Values = nil
				resource.ValuesOmitted = true
			}
		}
		omit(object.Item)
		omit(object.Folder)
		for _, selected := range object.Items {
			omit(selected.Snapshot)
		}
		raw, err = json.Marshal(object)
		if err != nil {
			return err
		}
		if len(raw) > MaxTriggerBytes {
			return dms.Invalid(fmt.Sprintf("workflow trigger metadata exceeds %d bytes", MaxTriggerBytes))
		}
	}
	return nil
}
