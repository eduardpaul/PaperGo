package dms

import (
	"context"
	"fmt"
	"maps"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"slices"
	"strings"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/taxonomy"
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
		draft, readable, err := t.DAL.Content().SearchScopes(ctx, principal(ctx, subject))
		return search.Access{Draft: draft, Read: readable}, err
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
		states, err := t.DAL.Content().SearchStates(ctx, principal(ctx, subject), ids)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			st, ok := states[k.ResourceID]
			if !ok {
				continue
			}
			head := k.Surface == search.SurfaceHead
			if head && !st.Head || !head && !st.Published {
				continue
			}
			switch surface {
			case search.SurfaceHead:
				// Without publishing the published surface holds the head.
				out[k] = st.Draft && head == st.Publishes
			case search.SurfacePublished:
				out[k] = !head && (st.Read || st.Draft)
			default:
				out[k] = st.Draft && head == st.Publishes || !st.Draft && st.Read && !head
			}
		}
		return out, nil
	})
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
		entries, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids, Kinds: contentKinds, State: resources.Live})
		if err != nil {
			return err
		}
		live := map[string]*resources.Resource{}
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
		cs, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: slices.Collect(maps.Keys(collections))})
		if err != nil {
			return err
		}
		for _, c := range cs {
			collections[c.ID] = c.PublishingEnabled
		}
		projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemIDs: slices.Collect(maps.Keys(live))})
		if err != nil {
			return err
		}
		revisionIDs := make([]string, 0, len(projections))
		for _, p := range projections {
			revisionIDs = append(revisionIDs, p.RevisionID)
		}
		byID, err := s.revisionsByID(ctx, revisionIDs)
		if err != nil {
			return err
		}
		defs := map[[2]string][]*catalog.FieldDefinition{}
		weights := map[string]map[string]string{}
		for _, p := range projections {
			r, rev := live[p.ItemID], byID[p.RevisionID]
			if rev == nil {
				continue
			}
			key := [2]string{rev.SchemaRevisionID, rev.ContentTypeID}
			if _, ok := defs[key]; !ok {
				schema, err := s.DAL.Catalog().Schema(ctx, rev.SchemaRevisionID)
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
				if catalog.FieldType(field.Type) == catalog.FieldKeywords {
					for _, value := range field.Values {
						keywordIDs = append(keywordIDs, value.Text)
					}
				}
			}
			if len(keywordIDs) > 0 {
				terms, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: keywordIDs})
				if err != nil {
					return err
				}
				canonical := map[string]bool{}
				for _, term := range terms {
					if term.MergedIntoID != "" {
						canonical[term.MergedIntoID] = true
					} else {
						canonical[term.ID] = true
					}
				}
				keywordIDs = slices.Sorted(maps.Keys(canonical))
			}
			content.terms = append(content.terms, p.Keywords...)
			content.wordTerms = append(content.wordTerms, p.Keywords...)
			words, err := s.termWords(ctx, content.wordTerms)
			if err != nil {
				return err
			}
			views := search.ViewPublished
			switch {
			case p.Surface == refs.SurfaceHead:
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
			d.CreatedAt, d.ModifiedAt = p.ItemCreatedAt, p.ModifiedAt
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
func searchValues(payload []byte, defs []*catalog.FieldDefinition, weights map[string]string) (searchContent, error) {
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
			if d.Type != catalog.FieldNote {
				value, err := searchFieldValue(d, x)
				if err != nil {
					return out, err
				}
				field.Values = append(field.Values, value)
			}
			switch d.Type {
			case catalog.FieldTerm, catalog.FieldKeywords:
				if id, ok := x.(string); ok {
					out.terms = append(out.terms, id)
					if weight != "none" {
						out.wordTerms = append(out.wordTerms, id)
					}
				}
			case catalog.FieldLookup, catalog.FieldPerson, catalog.FieldDate, catalog.FieldDatetime, catalog.FieldBoolean:
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
func searchFieldValue(d *catalog.FieldDefinition, v any) (search.FieldValue, error) {
	switch d.Type {
	case catalog.FieldInteger:
		n, err := integer(v)
		return search.FieldValue{Int: n}, err
	case catalog.FieldDecimal:
		_, n, err := decimal(v, d.Scale)
		return search.FieldValue{Int: n}, err
	case catalog.FieldBoolean:
		if b, _ := v.(bool); b {
			return search.FieldValue{Int: 1}, nil
		}
		return search.FieldValue{}, nil
	case catalog.FieldNumber, catalog.FieldCurrency:
		key, err := numberKey(v)
		return search.FieldValue{Text: key}, err
	case catalog.FieldDatetime:
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

// SearchTerms expands a term to itself, its descendants and the terms
// merged into any of them, as under does in collection queries.
func (s *Service) SearchTerms(ctx context.Context, id string) ([]string, error) {
	return read(ctx, s, func(t *Service) ([]string, error) {
		return t.DAL.Taxonomy().Family(ctx, id)
	})
}

// EntriesWithTerms lists, after an ID and in ID order, the live items of a
// workspace whose surfaces hold any of the terms, optionally in one
// collection or outside some: search re-indexes them when a term changes.
func (s *Service) EntriesWithTerms(ctx context.Context, workspaceID, collectionID string, skip []string, termIDs []string, after string, limit int) ([]string, error) {
	if len(termIDs) == 0 {
		return nil, nil
	}
	return s.DAL.Content().TermHolders(ctx, content.TermHolders{WorkspaceID: workspaceID, CollectionID: collectionID, Skip: skip, TermIDs: termIDs, AfterID: after, Limit: limit})
}
