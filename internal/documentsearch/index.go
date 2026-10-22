package documentsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/search"
)

// documents reads the search documents of items and folders as they are
// now. gone lists the IDs that are not live items or folders.
func documents(ctx context.Context, s *dms.Service, ids []string) ([]search.Document, []string, error) {
	entries, gone, err := s.IndexEntries(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	docs := make([]search.Document, 0, len(entries))
	for _, e := range entries {
		d, err := document(ctx, s, e)
		if err != nil {
			return nil, nil, err
		}
		docs = append(docs, d)
	}
	return docs, gone, nil
}

// document is the search document of an entry. References are IDs, so
// they are filters, not text; term names come from the term store as they
// are now. Notes are text only; dates and booleans filters only. A field's
// search_weight puts its text with the name's weight (high), in the body,
// or nowhere (none).
func document(ctx context.Context, s *dms.Service, e dms.IndexEntry) (search.Document, error) {
	var body, keywords, terms, wordTerms []string
	var fields []search.Field
	keywordIDs := append([]string{}, e.Keywords...)
	for _, f := range e.Fields {
		d := f.Definition
		if len(f.Indexed) > 0 {
			field := search.Field{Key: d.Key, Type: string(d.Type), Scale: d.Scale}
			for _, v := range f.Indexed {
				field.Values = append(field.Values, search.FieldValue{Text: v.Text, Int: v.Int})
			}
			fields = append(fields, field)
		}
		for _, x := range f.Values {
			switch d.Type {
			case catalog.FieldTerm, catalog.FieldKeywords:
				if id, ok := x.(string); ok {
					terms = append(terms, id)
					if f.Weight != "none" {
						wordTerms = append(wordTerms, id)
					}
					if d.Type == catalog.FieldKeywords {
						keywordIDs = append(keywordIDs, id)
					}
				}
			case catalog.FieldLookup, catalog.FieldPerson, catalog.FieldDate, catalog.FieldDatetime, catalog.FieldBoolean:
			default:
				text := fmt.Sprint(x)
				switch {
				case text == "" || f.Weight == "none":
				case f.Weight == "high":
					keywords = append(keywords, text)
				default:
					body = append(body, text)
				}
			}
		}
	}
	if len(keywordIDs) > 0 {
		found, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: keywordIDs})
		if err != nil {
			return search.Document{}, err
		}
		canonical := map[string]bool{}
		for _, t := range found {
			if t.MergedIntoID != "" {
				canonical[t.MergedIntoID] = true
			} else {
				canonical[t.ID] = true
			}
		}
		keywordIDs = slices.Sorted(maps.Keys(canonical))
	}
	terms = append(terms, e.Keywords...)
	wordTerms = append(wordTerms, e.Keywords...)
	words, err := termWords(ctx, s, wordTerms)
	if err != nil {
		return search.Document{}, err
	}
	r := e.Resource
	views := search.ViewPublished
	switch {
	case e.Surface == refs.SurfaceHead:
		views = search.ViewHead
	case !e.Publishes:
		// Without publishing the published surface is the head too.
		views |= search.ViewHead
	}
	d := search.Document{Archived: r.Archived, ArchivedAt: r.ArchivedAt, ResourceID: r.ID, Kind: string(r.Kind), Surface: string(e.Surface), Views: views, WorkspaceID: r.WorkspaceID,
		CollectionID: *r.ContainerID, ContentTypeID: e.ContentTypeID, RevisionID: e.RevisionID, Name: e.Name, KeywordIDs: keywordIDs,
		Keywords: strings.TrimSpace(strings.Join(slices.Concat(keywords, []string{words}), " ")), Body: strings.Join(body, "\n"),
		TermIDs: terms, Fields: fields, BlobID: e.BlobID,
		CreatedAt: e.CreatedAt, CreatedBy: e.CreatedBy, ModifiedAt: e.ModifiedAt, ModifiedBy: e.ModifiedBy}
	if r.ParentID != nil {
		d.ParentID = *r.ParentID
	}
	if r.ScopeID != nil {
		d.ScopeID = *r.ScopeID
	}
	if r.ArchivedBy != nil {
		d.ArchivedBy = *r.ArchivedBy
	}
	return d, nil
}

// termWords are the names, labels and synonyms of terms, and of the terms
// they were merged into, as they are now.
func termWords(ctx context.Context, s *dms.Service, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	rows, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: ids})
	if err != nil {
		return "", err
	}
	words := []string{}
	add := func(t taxonomy.Term) {
		words = append(words, t.Name)
		words = append(words, slices.Sorted(maps.Values(t.Labels))...)
		words = append(words, t.Synonyms...)
	}
	for _, t := range rows {
		add(t)
		if t.Merged() {
			target, err := s.DAL.Taxonomy().Term(ctx, t.MergedIntoID)
			if err != nil {
				return "", err
			}
			add(target)
		}
	}
	return strings.Join(words, " "), nil
}

// syncEntries brings the search documents of items and folders to their
// current state: surfaces that exist are written, others removed. It reads
// state, not events, so running it again, late or out of order is harmless.
func (x *Search) syncEntries(ctx context.Context, s *dms.Service, ids []string) (written, removed int, err error) {
	docs, gone, err := documents(ctx, s, ids)
	if err != nil {
		return 0, 0, err
	}
	present := map[search.Key]bool{}
	for _, d := range docs {
		present[search.Key{ResourceID: d.ResourceID, Surface: d.Surface}] = true
	}
	stored, err := x.Store.Keys(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	var stale []search.Key
	for _, k := range stored {
		if !present[k] && !slices.Contains(gone, k.ResourceID) {
			stale = append(stale, k)
		}
	}
	if len(gone) > 0 || len(stale) > 0 {
		if err = x.Store.DeleteDocuments(ctx, search.Selection{ResourceIDs: gone, Keys: stale}); err != nil {
			return 0, 0, err
		}
	}
	if err = x.Store.UpsertDocuments(ctx, docs, time.Now().UnixNano()); err != nil {
		return 0, 0, err
	}
	// Content belongs to file versions a surface still references.
	files := map[string][]string{}
	for _, d := range docs {
		if _, ok := files[d.ResourceID]; !ok {
			files[d.ResourceID] = []string{}
		}
		if d.BlobID != "" && !slices.Contains(files[d.ResourceID], d.BlobID) {
			files[d.ResourceID] = append(files[d.ResourceID], d.BlobID)
		}
	}
	for id, keep := range files {
		if err = x.Store.DeleteContent(ctx, id, keep); err != nil {
			return 0, 0, err
		}
	}
	return len(docs), len(gone) + len(stale), nil
}

// syncJob is a sync operation's payload: the entries of a workspace whose
// documents to write.
type syncJob struct {
	ResourceIDs []string `json:"resource_ids"`
}

// SyncResult is a finished sync's result: how many of its entries the
// batch that ran it wrote and removed.
type SyncResult struct {
	Written int `json:"written"`
	Removed int `json:"removed"`
}

// maxSync bounds the entries one sync request names.
const maxSync = 500

// syncAdmission puts every sync in one group, so the worker runs them
// together.
func syncAdmission(payload json.RawMessage) (string, string, error) {
	return "search", string(payload), nil
}

// validateSync runs in the transaction that queues a sync: the entries
// must be of the workspace, which the caller may read.
func validateSync(ctx context.Context, t *dms.Service, subject, workspaceID string, payload json.RawMessage) error {
	var j syncJob
	if err := json.Unmarshal(payload, &j); err != nil || len(j.ResourceIDs) == 0 || len(j.ResourceIDs) > maxSync {
		return dms.Invalid(fmt.Sprintf("give 1 to %d resource_ids", maxSync))
	}
	ws, err := t.Authorize(ctx, subject, workspaceID, "read")
	if err != nil {
		return err
	}
	if ws.Kind != refs.KindWorkspace {
		return dms.Invalid("search syncs belong to a workspace")
	}
	rows, err := t.DAL.Resources().Resources(ctx, resources.Filter{IDs: j.ResourceIDs})
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.WorkspaceID != workspaceID {
			return dms.Invalid("resource " + r.ID + " is not in the workspace")
		}
	}
	return nil
}

// StartSync queues the documents of resources of a workspace to be
// written. The search worker runs queued syncs together.
func StartSync(ctx context.Context, s *dms.Service, subject, workspaceID string, ids []string) (*dms.OperationView, error) {
	raw, err := json.Marshal(syncJob{ResourceIDs: ids})
	if err != nil {
		return nil, err
	}
	return s.EnqueueOperation(ctx, subject, workspaceID, KindSync, raw)
}

// sync writes the documents of the entries of a group of syncs at once,
// outside the database writer, and ends them.
func (x *Search) sync(ctx context.Context, s *dms.Service, ops []*operations.Operation) error {
	var ids []string
	for _, op := range ops {
		var j syncJob
		if err := json.Unmarshal(op.Payload, &j); err != nil {
			return err
		}
		for _, id := range j.ResourceIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var res SyncResult
	for batch := range slices.Chunk(ids, maxSync) {
		written, removed, err := x.syncEntries(ctx, s, batch)
		if err != nil {
			return err
		}
		res.Written += written
		res.Removed += removed
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		for _, op := range ops {
			op.Status, op.Result, op.Progress, op.CompletedAt = operations.StatusSucceeded, raw, 100, &completed
			if err := t.SaveOperation(ctx, op); err != nil {
				return err
			}
		}
		return nil
	})
}

// reindexBatch is how many entries a reindex writes at a time.
const reindexBatch = 200

// reindexPayload is what a reindex does. Documents not written since it was
// accepted are removed when it ends.
type reindexPayload struct {
	// Rebuild marks the rebuild of an empty store: search is unavailable
	// until it completes.
	Rebuild bool `json:"rebuild,omitempty"`
	// Content also asks the workflows that read files (file.updated with
	// the reindex source, such as search.content) to index the current file
	// of items whose file has no content in the store, or of every item
	// with Force.
	Content bool `json:"content,omitempty"`
	Force   bool `json:"force,omitempty"`
}

// Reindex chooses what a reindex rewrites besides documents.
type Reindex struct {
	Content bool `json:"content"`
	Force   bool `json:"force"`
}

func validateReindex(_ context.Context, _ *dms.Service, _, containerID string, _ json.RawMessage) error {
	if containerID != "" {
		return dms.Invalid("the search index belongs to the organization")
	}
	return nil
}

// StartReindex queues a reindex as subject.
func StartReindex(ctx context.Context, s *dms.Service, subject string, in Reindex) (*dms.OperationView, error) {
	if in.Force && !in.Content {
		return nil, dms.Invalid("force applies to a content reindex")
	}
	raw, _ := json.Marshal(reindexPayload{Content: in.Content, Force: in.Force})
	return s.EnqueueOperation(ctx, subject, "", KindReindex, raw)
}

// reindex writes the documents of every live item and folder with the
// operation's stamp, a batch at a time outside the database writer,
// checkpointing after each; the last batch removes what it did not write.
func (x *Search) reindex(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var p reindexPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return err
	}
	stamp := op.CreatedAt.UnixNano()
	for {
		ids, err := s.DAL.Resources().IDs(ctx, resources.Filter{Kinds: dms.ContentKinds, State: resources.Live, AfterID: op.AfterItemID, Limit: reindexBatch})
		if err != nil {
			return err
		}
		docs, _, err := documents(ctx, s, ids)
		if err != nil {
			return err
		}
		if err = x.Store.UpsertDocuments(ctx, docs, stamp); err != nil {
			return err
		}
		if p.Content {
			err = s.ReprocessFiles(ctx, op.CreatedBy, ids, func(itemID, blobID string) (bool, error) {
				if p.Force {
					return true, nil
				}
				c, err := x.Store.Content(ctx, itemID, blobID)
				return c.Generation == "", err
			})
			if err != nil {
				return err
			}
		}
		op.Processed += len(ids)
		if len(ids) < reindexBatch {
			break
		}
		op.AfterItemID = ids[len(ids)-1]
		if err = s.Write(ctx, func(t *dms.Service) error { return t.SaveOperation(ctx, op) }); err != nil {
			return err
		}
	}
	if err := x.Store.DeleteDocuments(ctx, search.Selection{Before: stamp}); err != nil {
		return err
	}
	err := s.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		op.Status, op.Progress, op.CompletedAt = operations.StatusSucceeded, 100, &completed
		return t.SaveOperation(ctx, op)
	})
	if err == nil && p.Rebuild {
		x.rebuilding.Store(false)
	}
	return err
}

// matchLimit bounds the entries a search in a collection query or listing
// considers; more set search_capped.
var matchLimit = 10_000

// MatchText finds the entries of a collection (or a workspace) whose text
// matches as a literal phrase whose last word is a prefix, as people type.
// It does not search while the store is rebuilt or unavailable, or in a
// collection that paused its search.index provider: entries match by name
// there.
func (x *Search) MatchText(ctx context.Context, s *dms.Service, subject string, in dms.TextMatch) (dms.TextMatches, error) {
	if x.rebuilding.Load() {
		return dms.TextMatches{}, dms.ErrNoTextMatch
	}
	if in.CollectionID != "" {
		off, err := indexOff(ctx, s, in.CollectionID)
		if err != nil {
			return dms.TextMatches{}, err
		}
		if off {
			return dms.TextMatches{}, dms.ErrNoTextMatch
		}
	}
	q := search.Literal(in.Text)
	if q == nil {
		return dms.TextMatches{Head: []string{}, Published: []string{}}, nil
	}
	content, err := contentAllowed(ctx, s, subject)
	if err != nil {
		return dms.TextMatches{}, err
	}
	m, err := x.Store.Match(ctx, search.MatchQuery{Archive: in.Archive, CollectionID: in.CollectionID, WorkspaceID: in.WorkspaceID, Text: q, Content: content, Limit: matchLimit})
	if errors.Is(err, search.ErrUnavailable) {
		return dms.TextMatches{}, dms.ErrNoTextMatch
	}
	if err != nil {
		return dms.TextMatches{}, err
	}
	return dms.TextMatches{Head: m.Head, Published: m.Published, Capped: m.Capped}, nil
}

// indexOff reports whether a collection's search.index provider is paused,
// archived or deleted, so its documents may be stale. Before defaults have
// been prepared, indexing is on.
func indexOff(ctx context.Context, s *dms.Service, collectionID string) (bool, error) {
	c, err := s.DAL.Resources().Resource(ctx, collectionID)
	if err != nil {
		return false, err
	}
	provider := true
	own, err := s.DAL.Workflows().List(ctx, workflows.Filter{WorkspaceID: c.WorkspaceID, Role: RoleIndex, AutomaticProvider: &provider, CollectionID: collectionID})
	if err != nil || len(own) == 0 {
		return false, err
	}
	return !slices.ContainsFunc(own, func(w workflows.Workflow) bool {
		return !w.Deleted() && w.AutomaticEnabled && w.State == workflows.StatePublished
	}), nil
}

// contentAllowed reports whether subject may search file contents;
// metadata remains searchable when they may not.
func contentAllowed(ctx context.Context, s *dms.Service, subject string) (bool, error) {
	err := s.RequireScope(ctx, subject, "document.read")
	if errors.Is(err, dms.ErrForbidden) {
		return false, nil
	}
	return err == nil, err
}
