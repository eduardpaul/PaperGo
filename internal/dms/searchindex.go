package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"slices"
	"time"

	"papergo/internal/dal/operations"
	"papergo/internal/dal/workflows"
	"papergo/internal/search"
)

// ErrSearchUnavailable means no search store is configured, or it is being
// rebuilt.
var ErrSearchUnavailable = search.ErrUnavailable

// ErrSearchOff means the collection turned off the workflow that keeps its
// search documents current, so its documents may be stale.
var ErrSearchOff = errors.New("search is turned off for this collection")

// searchRole is the workflow role whose filler keeps search documents
// current (workflow.RoleSearchIndex).
const searchRole = "search.index"

// searchOff reports whether a configured collection provider is unavailable.
// Before defaults have been prepared, indexing is treated as on. Explicitly
// paused, archived or deleted providers keep collection search off.
func (s *Service) searchOff(ctx context.Context, collectionID string) (bool, error) {
	c, err := s.DAL.Resources().Resource(ctx, collectionID)
	if err != nil {
		return false, err
	}
	provider := true
	own, err := s.DAL.Workflows().List(ctx, workflows.Filter{WorkspaceID: c.WorkspaceID, Role: searchRole, AutomaticProvider: &provider, CollectionID: collectionID})
	if err != nil {
		return false, err
	}
	if len(own) > 0 {
		return !slices.ContainsFunc(own, func(w workflows.Workflow) bool {
			return !w.Deleted() && w.AutomaticEnabled && w.State == workflows.StatePublished
		}), nil
	}
	return false, nil
}

// SyncSearch brings the search documents of items and folders to their
// current state: surfaces that exist are written, others removed. It reads
// state, not events, so running it again, late or out of order is harmless.
// Without a search store it does nothing.
func (s *Service) SyncSearch(ctx context.Context, ids []string) (written, removed int, err error) {
	if s.search == nil || len(ids) == 0 {
		return 0, 0, nil
	}
	docs, gone, err := s.SearchDocuments(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	present := map[search.Key]bool{}
	for _, d := range docs {
		present[search.Key{ResourceID: d.ResourceID, Surface: d.Surface}] = true
	}
	stored, err := s.search.Keys(ctx, ids)
	if err != nil {
		return 0, 0, err
	}
	var stale []search.Key
	for _, k := range stored {
		if !present[k] && !containsID(gone, k.ResourceID) {
			stale = append(stale, k)
		}
	}
	if len(gone) > 0 || len(stale) > 0 {
		if err = s.search.DeleteDocuments(ctx, search.Selection{ResourceIDs: gone, Keys: stale}); err != nil {
			return 0, 0, err
		}
	}
	if err = s.search.UpsertDocuments(ctx, docs, time.Now().UnixNano()); err != nil {
		return 0, 0, err
	}
	// Content belongs to file versions a surface still references.
	files := map[string][]string{}
	for _, d := range docs {
		if _, ok := files[d.ResourceID]; !ok {
			files[d.ResourceID] = []string{}
		}
		if d.BlobID != "" && !containsID(files[d.ResourceID], d.BlobID) {
			files[d.ResourceID] = append(files[d.ResourceID], d.BlobID)
		}
	}
	for id, keep := range files {
		if err = s.search.DeleteContent(ctx, id, keep); err != nil {
			return 0, 0, err
		}
	}
	return len(docs), len(gone) + len(stale), nil
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// OperationKindSearchReindex rebuilds the search index from every live item
// and folder, as an organization operation.
const OperationKindSearchReindex = "search_reindex"

const searchReindexBatch = 200

// searchReindexPayload is what a reindex does. Its stamp is the time the
// operation was accepted: documents not written since are removed when it
// ends.
type searchReindexPayload struct {
	// Rebuild marks the rebuild of an empty store: search is unavailable
	// until it completes.
	Rebuild bool `json:"rebuild,omitempty"`
	// Content also asks the workflows that read files (file.added with the
	// reindex source, such as search.content) to index the current file of
	// items whose file has no content in the store, or of every item with
	// Force.
	Content bool `json:"content,omitempty"`
	Force   bool `json:"force,omitempty"`
}

// SearchReindex chooses what a reindex rebuilds besides documents.
type SearchReindex struct {
	Content bool `json:"content"`
	Force   bool `json:"force"`
}

// systemSubject is the creator of operations PaperGo starts itself.
const systemSubject = "papergo"

// RebuildSearch queues a rebuild of the search store from every live item
// and folder, when the store is new or was recreated, unless one is already
// queued. Search answers ErrSearchUnavailable until it completes.
func (s *Service) RebuildSearch(ctx context.Context) error {
	return s.write(ctx, func(t *Service) error {
		pending, err := t.DAL.Operations().Exists(ctx, operations.Filter{Kinds: []string{OperationKindSearchReindex}, Statuses: []operations.Status{operations.StatusPending}, CreatedBy: systemSubject})
		if err != nil || pending {
			s.searchRebuild.Store(pending)
			return err
		}
		raw, _ := json.Marshal(searchReindexPayload{Rebuild: true, Content: true})
		if _, _, err = t.accept(ctx, operations.Operation{Kind: OperationKindSearchReindex, CreatedBy: systemSubject, Payload: raw}, false); err != nil {
			return err
		}
		s.searchRebuild.Store(true)
		return nil
	})
}

// ResumeSearchRebuild marks search unavailable while a rebuild queued before
// a restart is still pending.
func (s *Service) ResumeSearchRebuild(ctx context.Context) error {
	pending, err := s.DAL.Operations().Exists(ctx, operations.Filter{Kinds: []string{OperationKindSearchReindex}, Statuses: []operations.Status{operations.StatusPending}, CreatedBy: systemSubject})
	if err == nil {
		s.searchRebuild.Store(pending)
	}
	return err
}

func searchReindexHandler() OperationHandler {
	return OperationHandler{Scope: "organization.manage",
		Validate: func(ctx context.Context, s *Service, subject, containerID string, payload json.RawMessage) error {
			if containerID != "" {
				return invalid("the search index belongs to the organization")
			}
			if s.search == nil {
				return ErrSearchUnavailable
			}
			return nil
		},
		Step: func(ctx context.Context, s *Service, op *operations.Operation) error {
			return s.searchReindexStep(ctx, op)
		},
		Failed: func(context.Context, *Service, *operations.Operation, error) error {
			return nil
		}}
}

// StartSearchReindex queues the reindex operation.
func (s *Service) StartSearchReindex(ctx context.Context, subject string, in SearchReindex) (*OperationView, error) {
	if in.Force && !in.Content {
		return nil, invalid("force applies to a content reindex")
	}
	raw, _ := json.Marshal(searchReindexPayload{Content: in.Content, Force: in.Force})
	return s.EnqueueOperation(ctx, subject, "", OperationKindSearchReindex, raw)
}

// searchReindexStep writes the documents of one batch of entries, by ID,
// with the operation's stamp; the last step removes what it did not write.
func (s *Service) searchReindexStep(ctx context.Context, op *operations.Operation) error {
	if s.search == nil {
		return ErrSearchUnavailable
	}
	var p searchReindexPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return err
	}
	ids, err := s.DAL.Resources().IDs(ctx, resources.Filter{Kinds: contentKinds, State: resources.Live, AfterID: op.AfterItemID, Limit: searchReindexBatch})
	if err != nil {
		return err
	}
	docs, _, err := s.SearchDocuments(ctx, ids)
	if err != nil {
		return err
	}
	stamp := op.CreatedAt.UnixNano()
	if err = s.search.UpsertDocuments(ctx, docs, stamp); err != nil {
		return err
	}
	if p.Content {
		if err = s.reindexContent(ctx, op.CreatedBy, ids, p.Force); err != nil {
			return err
		}
	}
	op.Processed += len(ids)
	if len(ids) == searchReindexBatch {
		op.AfterItemID = ids[len(ids)-1]
		return s.SaveOperation(ctx, op)
	}
	if err = s.search.DeleteDocuments(ctx, search.Selection{Before: stamp}); err != nil {
		return err
	}
	op.Status, op.Progress = operations.StatusSucceeded, 100
	if err = s.SaveOperation(ctx, op); err != nil {
		return err
	}
	if p.Rebuild {
		s.searchRebuild.Store(false)
	}
	return nil
}

// reindexContent raises file.added (source reindex) for the current file of
// the items without content in the store, or of all of them with force.
func (s *Service) reindexContent(ctx context.Context, actor string, ids []string, force bool) error {
	rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids, Kinds: []refs.ResourceKind{refs.KindItem}, WithHead: true})
	if err != nil {
		return err
	}
	for _, r := range rows {
		rev, err := s.revision(ctx, *r.HeadRevisionID)
		if err != nil {
			return err
		}
		if rev.BlobID == nil {
			continue
		}
		if !force {
			c, err := s.search.Content(ctx, r.ID, *rev.BlobID)
			if err != nil {
				return err
			}
			if c.Generation != "" {
				continue
			}
		}
		b, err := s.blob(ctx, *rev.BlobID)
		if err != nil {
			return err
		}
		s.emitFileChange(ctx, actor, r, b, rev, FileReindex, &b.ID)
	}
	return nil
}

// searchMatchLimit bounds the entries a search in a collection query or
// listing considers; more set search_capped.
var searchMatchLimit = 10_000

// searchMatches finds the entries of a collection (or a workspace) whose
// text matches as a literal phrase whose last word is a prefix, as people
// type. It returns the IDs matching on each surface as JSON lists, for the
// query's SQL, which authorizes, filters, sorts and pages.
func (s *Service) searchMatches(ctx context.Context, subject, collectionID, workspaceID, text string) (head, published []string, capped bool, err error) {
	if s.search == nil || s.searchRebuild.Load() {
		return nil, nil, false, ErrSearchUnavailable
	}
	if collectionID != "" {
		off, err := s.searchOff(ctx, collectionID)
		if err != nil {
			return nil, nil, false, err
		}
		if off {
			return nil, nil, false, ErrSearchOff
		}
	}
	q := search.Literal(text)
	if q == nil {
		return []string{}, []string{}, false, nil
	}
	content, err := s.searchContentAllowed(ctx, subject)
	if err != nil {
		return nil, nil, false, err
	}
	m, err := s.search.Match(ctx, search.MatchQuery{CollectionID: collectionID, WorkspaceID: workspaceID, Text: q, Content: content, Limit: searchMatchLimit})
	if errors.Is(err, search.ErrUnavailable) {
		return nil, nil, false, ErrSearchUnavailable
	}
	if err != nil {
		return nil, nil, false, err
	}
	return m.Head, m.Published, m.Capped, nil
}

// Metadata remains searchable when the caller cannot read file contents.
func (s *Service) searchContentAllowed(ctx context.Context, subject string) (bool, error) {
	err := s.RequireScope(ctx, subject, "document.read")
	if errors.Is(err, ErrForbidden) {
		return false, nil
	}
	return err == nil, err
}

// SearchStatus describes the search store, for organization managers.
type SearchStatus struct {
	Store      string `json:"store"`
	Rebuilding bool   `json:"rebuilding"`
	Documents  int64  `json:"documents"`
	// Chunks counts the published chunks of file content.
	Chunks int64 `json:"chunks"`
	Bytes  int64 `json:"bytes"`
	// Model is the embedding model of semantic search, empty when none is
	// configured or it is unavailable; Embeddings describes its vectors.
	Model      string                 `json:"model,omitempty"`
	Embeddings *search.EmbeddingStats `json:"embeddings,omitempty"`
}

// SearchStatus reports the search store's state.
func (s *Service) SearchStatus(ctx context.Context, subject string) (SearchStatus, error) {
	if err := s.RequireScope(ctx, subject, "organization.manage"); err != nil {
		return SearchStatus{}, err
	}
	if s.search == nil {
		return SearchStatus{}, ErrSearchUnavailable
	}
	st, err := s.search.Stats(ctx)
	if err != nil {
		return SearchStatus{}, err
	}
	out := SearchStatus{Store: s.search.Name(), Rebuilding: s.searchRebuild.Load(), Documents: st.Documents, Chunks: st.Chunks, Bytes: st.Bytes}
	// A model that cannot name its vector space now (a provider measuring
	// its dimensions is down) is left out rather than failing the status.
	if model, err := s.embeddingKey(ctx); err == nil && model != "" {
		out.Model = model
		vectors, err := s.search.Embeddings(ctx, out.Model)
		if err != nil {
			return SearchStatus{}, err
		}
		out.Embeddings = &vectors
	}
	return out, nil
}

// embeddingKey is the configured embedding model's key, empty when none is.
func (s *Service) embeddingKey(ctx context.Context) (string, error) {
	if s.embeddings == nil {
		return "", nil
	}
	return s.embeddings.Key(ctx)
}
