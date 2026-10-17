package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"papergo/ent/schemarevision"
	"papergo/ent/term"
	"papergo/internal/ai"
	"papergo/internal/search"
)

// SetSearch gives the service its search store: collection queries and
// listings match their search text there, and Search answers global search.
// Call it before the service is used.
func (s *Service) SetSearch(store search.Store) { s.search = store }

// SetEmbeddings gives the service the embedding model of semantic and
// hybrid search. Call it before the service is used.
func (s *Service) SetEmbeddings(model ai.Embedder) { s.embeddings = model }

// Embeddings is the configured embedding model, or nil.
func (s *Service) Embeddings() ai.Embedder { return s.embeddings }

// SearchStore is the configured search store, or nil.
func (s *Service) SearchStore() search.Store { return s.search }

// SearchScopes decides, once per permission scope of the organization's
// items and folders, whether the subject reads drafts there or published
// content: what search trims to. Permissions depend only on a row's scope,
// its workspace and whether it is content, so one live row stands for each
// scope, as in collection queries.
func (s *Service) SearchScopes(ctx context.Context, subject string) (search.Access, error) {
	return read(ctx, s, func(t *Service) (search.Access, error) {
		draftSQL, draftArgs := permissionSQL(ctx, "reps.rep", subject, "read_draft")
		readSQL, readArgs := permissionSQL(ctx, "reps.rep", subject, "read")
		live := `SELECT r.id FROM resources r WHERE r.kind IN ('item','folder') AND r.deleted_at IS NULL AND r.scope_id`
		rows, err := t.Client.QueryContext(ctx, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL),
reps AS (SELECT s.id AS scope, (`+live+`=s.id LIMIT 1) AS rep FROM scopes s WHERE s.id IS NOT NULL
 UNION ALL SELECT NULL, (`+live+` IS NULL LIMIT 1))
SELECT coalesce(scope,''), `+draftSQL+`, `+readSQL+` FROM reps WHERE rep IS NOT NULL`, append(draftArgs, readArgs...)...)
		if err != nil {
			return search.Access{}, err
		}
		access := search.Access{Draft: []string{}, Read: []string{}}
		for rows.Next() {
			var scope string
			var draft, readable bool
			if err = rows.Scan(&scope, &draft, &readable); err != nil {
				break
			}
			if draft {
				access.Draft = append(access.Draft, scope)
			}
			if draft || readable {
				access.Read = append(access.Read, scope)
			}
		}
		return access, errors.Join(err, rows.Err(), rows.Close())
	})
}

// SearchVisible reports which documents the subject may be shown now, on the
// surface asked, whatever the search store holds: entries that are gone, no
// longer readable, or read on the other surface are left out.
func (s *Service) SearchVisible(ctx context.Context, subject, surface string, keys []search.Key) (map[search.Key]bool, error) {
	return read(ctx, s, func(t *Service) (map[search.Key]bool, error) {
		out := map[search.Key]bool{}
		ids := make([]string, 0, len(keys))
		for _, k := range keys {
			ids = append(ids, k.ResourceID)
		}
		draftSQL, draftArgs := permissionSQL(ctx, "r.id", subject, "read_draft")
		readSQL, readArgs := permissionSQL(ctx, "r.id", subject, "read")
		rows, err := t.Client.QueryContext(ctx, `SELECT r.id, c.publishing_enabled,
 EXISTS(SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface='head'),
 EXISTS(SELECT 1 FROM item_surfaces p WHERE p.item_id=r.id AND p.surface='published'), `+draftSQL+`, `+readSQL+` FROM resources r JOIN resources c ON c.id=r.container_id
 WHERE r.id IN (SELECT value FROM json_each(?)) AND r.kind IN ('item','folder') AND r.deleted_at IS NULL`, append(append(draftArgs, readArgs...), jsonIDs(ids))...)
		if err != nil {
			return nil, err
		}
		type state struct{ publishes, head, published, draft, read bool }
		states := map[string]state{}
		for rows.Next() {
			var id string
			var st state
			if err = rows.Scan(&id, &st.publishes, &st.head, &st.published, &st.draft, &st.read); err != nil {
				break
			}
			states[id] = st
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		for _, k := range keys {
			st, ok := states[k.ResourceID]
			if !ok {
				continue
			}
			head := k.Surface == search.SurfaceHead
			if head && !st.head || !head && !st.published {
				continue
			}
			switch surface {
			case search.SurfaceHead:
				// Without publishing the published surface holds the head.
				out[k] = st.draft && head == st.publishes
			case search.SurfacePublished:
				out[k] = !head && (st.read || st.draft)
			default:
				out[k] = st.draft && head == st.publishes || !st.draft && st.read && !head
			}
		}
		return out, nil
	})
}

func jsonIDs(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	raw, _ := json.Marshal(ids)
	return string(raw)
}

// SearchDocuments reads the current surfaces of items and folders as search
// documents. It reads what is stored, whoever asks: search authorizes every
// query, so the index may hold everything. gone lists the IDs that are not
// live items or folders.
func (s *Service) SearchDocuments(ctx context.Context, ids []string) (docs []search.Document, gone []string, err error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	err = func() error {
		entries, err := s.Client.Resource.Query().Where(resource.IDIn(ids...), resource.KindIn(resource.KindItem, resource.KindFolder), resource.DeletedAtIsNil()).All(ctx)
		if err != nil {
			return err
		}
		live := map[string]*ent.Resource{}
		collections := map[string]bool{}
		for _, r := range entries {
			if r.ContainerID != nil {
				live[r.ID] = r
				collections[*r.ContainerID] = false
			}
		}
		for _, id := range ids {
			if live[id] == nil && !slices.Contains(gone, id) {
				gone = append(gone, id)
			}
		}
		if len(live) == 0 {
			return nil
		}
		cs, err := s.Client.Resource.Query().Where(resource.IDIn(slices.Collect(maps.Keys(collections))...)).Select(resource.FieldID, resource.FieldPublishingEnabled).All(ctx)
		if err != nil {
			return err
		}
		for _, c := range cs {
			collections[c.ID] = c.PublishingEnabled
		}
		surfaces, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDIn(slices.Collect(maps.Keys(live))...)).Order(ent.Asc(itemsurface.FieldItemID), ent.Asc(itemsurface.FieldSurface)).All(ctx)
		if err != nil {
			return err
		}
		revisionIDs := make([]string, 0, len(surfaces))
		for _, p := range surfaces {
			revisionIDs = append(revisionIDs, p.RevisionID)
		}
		revisions, err := s.Client.ItemRevision.Query().Where(itemrevision.IDIn(revisionIDs...)).All(ctx)
		if err != nil {
			return err
		}
		byID := map[string]*ent.ItemRevision{}
		for _, rev := range revisions {
			byID[rev.ID] = rev
		}
		defs := map[[2]string][]*ent.FieldDefinition{}
		weights := map[string]map[string]string{}
		for _, p := range surfaces {
			r, rev := live[p.ItemID], byID[p.RevisionID]
			if rev == nil {
				continue
			}
			key := [2]string{rev.SchemaRevisionID, rev.ContentTypeID}
			if _, ok := defs[key]; !ok {
				schema, err := s.Client.SchemaRevision.Query().Where(schemarevision.IDEQ(rev.SchemaRevisionID)).Only(ctx)
				if err != nil {
					return err
				}
				if defs[key], _, err = typedDefinitions(schema.Definition, rev.ContentTypeID); err != nil {
					return err
				}
			}
			if _, ok := weights[*r.ContainerID]; !ok {
				if weights[*r.ContainerID], err = s.searchWeights(ctx, *r.ContainerID); err != nil {
					return err
				}
			}
			content, err := searchValues(rev.Payload, defs[key], weights[*r.ContainerID])
			if err != nil {
				return err
			}
			keywordIDs := append([]string{}, p.Keywords...)
			for _, field := range content.fields {
				if field.Type == "keywords" {
					for _, value := range field.Values {
						keywordIDs = append(keywordIDs, value.Text)
					}
				}
			}
			if len(keywordIDs) > 0 {
				raw, _ := json.Marshal(keywordIDs)
				rows, err := s.Client.QueryContext(ctx, "SELECT DISTINCT coalesce(merged_into_id,id) FROM terms WHERE id IN (SELECT value FROM json_each(?)) ORDER BY 1", string(raw))
				if err != nil {
					return err
				}
				keywordIDs = []string{}
				for rows.Next() {
					var id string
					if err = rows.Scan(&id); err != nil {
						rows.Close()
						return err
					}
					keywordIDs = append(keywordIDs, id)
				}
				err = errors.Join(rows.Err(), rows.Close())
				if err != nil {
					return err
				}
			}
			content.terms = append(content.terms, p.Keywords...)
			content.wordTerms = append(content.wordTerms, p.Keywords...)
			words, err := s.termWords(ctx, content.wordTerms)
			if err != nil {
				return err
			}
			views := search.ViewPublished
			switch {
			case p.Surface == itemsurface.SurfaceHead:
				views = search.ViewHead
			case !collections[*r.ContainerID]:
				// Without publishing the published surface is the head too.
				views |= search.ViewHead
			}
			d := search.Document{ResourceID: r.ID, Kind: string(r.Kind), Surface: string(p.Surface), Views: views, WorkspaceID: r.WorkspaceID,
				CollectionID: *r.ContainerID, ContentTypeID: rev.ContentTypeID, RevisionID: rev.ID, Name: p.Name, KeywordIDs: keywordIDs,
				Keywords: strings.TrimSpace(strings.Join(slices.Concat(content.keywords, []string{words}), " ")), Body: strings.Join(content.body, "\n"),
				TermIDs: content.terms, Fields: content.fields,
				CreatedBy: p.ItemCreatedBy, ModifiedBy: p.ModifiedBy}
			if r.ParentID != nil {
				d.ParentID = *r.ParentID
			}
			if r.ScopeID != nil {
				d.ScopeID = *r.ScopeID
			}
			if rev.BlobID != nil {
				d.BlobID = *rev.BlobID
			}
			d.CreatedAt, _ = time.Parse(time.RFC3339Nano, p.ItemCreatedAt)
			d.ModifiedAt, _ = time.Parse(time.RFC3339Nano, p.ModifiedAt)
			docs = append(docs, d)
		}
		return nil
	}()
	return docs, gone, err
}

// searchContent is what a surface's values give search.
type searchContent struct {
	body, keywords []string
	// terms are every term the surface holds, for term filters; wordTerms
	// those whose names and labels are searchable text.
	terms, wordTerms []string
	fields           []search.Field
}

// searchValues turns a revision's values into search text and typed
// fields, by each field's current search_weight (weights, by field key).
// References are IDs, so they are filters, not text; term names come from
// the term store when the document is built. Notes are text only; dates
// and booleans filters only.
func searchValues(payload []byte, defs []*ent.FieldDefinition, weights map[string]string) (searchContent, error) {
	var out searchContent
	values, err := decodeValues(payload)
	if err != nil {
		return out, err
	}
	for _, d := range defs {
		v, ok := values[d.Key]
		if !ok || v == nil {
			continue
		}
		list := []any{v}
		if many, ok := v.([]any); ok {
			list = many
		}
		weight := weights[d.Key]
		field := search.Field{Key: d.Key, Type: string(d.Type), Scale: d.Scale}
		for _, x := range list {
			if d.Type != "note" {
				value, err := searchFieldValue(d, x)
				if err != nil {
					return out, err
				}
				field.Values = append(field.Values, value)
			}
			switch d.Type {
			case "term", "keywords":
				if id, ok := x.(string); ok {
					out.terms = append(out.terms, id)
					if weight != "none" {
						out.wordTerms = append(out.wordTerms, id)
					}
				}
			case "lookup", "person", "date", "datetime", "boolean":
			default:
				text := fmt.Sprint(x)
				switch {
				case text == "" || weight == "none":
				case weight == "high":
					out.keywords = append(out.keywords, text)
				default:
					out.body = append(out.body, text)
				}
			}
		}
		if len(field.Values) > 0 {
			out.fields = append(out.fields, field)
		}
	}
	return out, nil
}

// searchFieldValue encodes a stored value as the field index does, so
// search filters compare exactly as collection queries do.
func searchFieldValue(d *ent.FieldDefinition, v any) (search.FieldValue, error) {
	switch d.Type {
	case "integer":
		n, err := integer(v)
		return search.FieldValue{Int: n}, err
	case "decimal":
		_, n, err := decimal(v, d.Scale)
		return search.FieldValue{Int: n}, err
	case "boolean":
		if b, _ := v.(bool); b {
			return search.FieldValue{Int: 1}, nil
		}
		return search.FieldValue{}, nil
	case "number", "currency":
		key, err := numberKey(v)
		return search.FieldValue{Text: key}, err
	case "datetime":
		t, err := time.Parse(time.RFC3339Nano, fmt.Sprint(v))
		return search.FieldValue{Text: t.UTC().Format("2006-01-02T15:04:05.000000000Z")}, err
	default:
		return search.FieldValue{Text: fmt.Sprint(v)}, nil
	}
}

// searchWeights are the current search_weight of a collection's fields, by
// key: a field's weight applies to every revision once it is reindexed.
func (s *Service) searchWeights(ctx context.Context, collectionID string) (map[string]string, error) {
	defs, err := s.collectionFields(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range defs {
		out[d.Key] = d.Options.SearchWeight
	}
	return out, nil
}

// termWords are the names, labels and synonyms of terms, and of the terms
// they were merged into, as they are now.
func (s *Service) termWords(ctx context.Context, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	rows, err := s.Client.Term.Query().Where(term.IDIn(ids...)).WithMergedInto().All(ctx)
	if err != nil {
		return "", err
	}
	words := []string{}
	add := func(t *ent.Term) {
		words = append(words, t.Name)
		words = append(words, slices.Sorted(maps.Values(t.Labels))...)
		words = append(words, t.Synonyms...)
	}
	for _, t := range rows {
		add(t)
		if t.Edges.MergedInto != nil {
			add(t.Edges.MergedInto)
		}
	}
	return strings.Join(words, " "), nil
}

// SearchTerms expands a term to itself, its descendants and the terms
// merged into any of them, as under does in collection queries.
func (s *Service) SearchTerms(ctx context.Context, id string) ([]string, error) {
	return read(ctx, s, func(t *Service) ([]string, error) {
		subtree := "terms r JOIN terms t ON t.path>=r.path AND t.path<substr(r.path,1,length(r.path)-1)||'0' WHERE r.id=?"
		rows, err := t.Client.QueryContext(ctx, "SELECT t.id FROM "+subtree+" UNION SELECT m.id FROM terms m WHERE m.merged_into_id IN (SELECT t.id FROM "+subtree+")", id, id)
		if err != nil {
			return nil, err
		}
		out := []string{}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				break
			}
			out = append(out, v)
		}
		return out, errors.Join(err, rows.Err(), rows.Close())
	})
}

// EntriesWithTerms lists, after an ID and in ID order, the live items of a
// workspace whose surfaces hold any of the terms, optionally in one
// collection or outside some: search re-indexes them when a term changes.
func (s *Service) EntriesWithTerms(ctx context.Context, workspaceID, collectionID string, skip []string, termIDs []string, after string, limit int) ([]string, error) {
	if len(termIDs) == 0 {
		return nil, nil
	}
	conds := []string{"p.workspace_id=?", "p.item_id>?"}
	args := []any{workspaceID, after}
	if collectionID != "" {
		conds, args = append(conds, "p.container_id=?"), append(args, collectionID)
	}
	if len(skip) > 0 {
		conds, args = append(conds, "p.container_id NOT IN (SELECT value FROM json_each(?))"), append(args, jsonIDs(skip))
	}
	held := make([]string, len(termIDs))
	for i, id := range termIDs {
		held[i] = "instr(p.payload, ?) > 0"
		args = append(args, `"`+id+`"`)
	}
	conds = append(conds, "("+strings.Join(held, " OR ")+")")
	rows, err := s.Client.QueryContext(ctx, `SELECT DISTINCT p.item_id FROM item_surfaces p WHERE `+strings.Join(conds, " AND ")+` ORDER BY p.item_id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		out = append(out, id)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}
