package dms

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
)

// ContentKinds are the kinds of resource a collection holds.
var ContentKinds = []refs.ResourceKind{refs.KindItem, refs.KindFolder}

// IndexEntry is a surface of a live item or folder as an index kept
// outside the database, such as a search index, stores it.
type IndexEntry struct {
	Resource *resources.Resource
	Surface  refs.Surface
	// Publishes reports that the collection publishes; without publishing,
	// its published surface is also the head.
	Publishes  bool
	RevisionID string
	// ContentTypeID is the catalog type the surface's list type copies, so
	// filters and facets count lists of one type together.
	ContentTypeID string
	BlobID        string
	Name          string
	// Keywords are the surface's keyword term IDs.
	Keywords   []string
	Fields     []IndexField
	CreatedAt  time.Time
	CreatedBy  string
	ModifiedAt time.Time
	ModifiedBy string
}

// IndexField is a field's values on a surface. Indexed holds each value
// encoded as the field index encodes it, so an index compares as collection
// queries do; notes have none.
type IndexField struct {
	Definition *catalog.FieldDefinition
	// Weight is the field's current search_weight in its collection.
	Weight  string
	Values  []any
	Indexed []IndexValue
}

// IndexValue is a value as the field index encodes it: integers, decimals
// and booleans as Int, the others as Text.
type IndexValue struct {
	Text string
	Int  int64
}

// IndexEntries reads the current surfaces of items and folders. It reads
// what is stored, whoever asks: an index that answers people must trim
// what it returns by their permissions. gone lists the IDs that are not
// live items or folders.
func (s *Service) IndexEntries(ctx context.Context, ids []string) (entries []IndexEntry, gone []string, err error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids, Kinds: ContentKinds, State: resources.Live})
	if err != nil {
		return nil, nil, err
	}
	live := map[string]*resources.Resource{}
	publishes := map[string]bool{}
	for _, r := range rows {
		if r.ContainerID != nil {
			live[r.ID] = r
			publishes[*r.ContainerID] = false
		}
	}
	for _, id := range ids {
		if live[id] == nil && !slices.Contains(gone, id) {
			gone = append(gone, id)
		}
	}
	if len(live) == 0 {
		return nil, gone, nil
	}
	collections, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: slices.Collect(maps.Keys(publishes))})
	if err != nil {
		return nil, nil, err
	}
	for _, c := range collections {
		publishes[c.ID] = c.PublishingEnabled
	}
	projections, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemIDs: slices.Collect(maps.Keys(live))})
	if err != nil {
		return nil, nil, err
	}
	revisionIDs := make([]string, 0, len(projections))
	for _, p := range projections {
		revisionIDs = append(revisionIDs, p.RevisionID)
	}
	byID, err := s.revisionsByID(ctx, revisionIDs)
	if err != nil {
		return nil, nil, err
	}
	typeIDs := []string{}
	for _, rev := range byID {
		typeIDs = append(typeIDs, rev.ContentTypeID)
	}
	slices.Sort(typeIDs)
	catalogTypes, err := s.DAL.Catalog().CatalogTypeIDs(ctx, slices.Compact(typeIDs))
	if err != nil {
		return nil, nil, err
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
				return nil, nil, err
			}
			if defs[key], _, err = typedDefinitions(schema.Definition, rev.ContentTypeID); err != nil {
				return nil, nil, err
			}
		}
		if _, ok := weights[*r.ContainerID]; !ok {
			if weights[*r.ContainerID], err = s.fieldWeights(ctx, *r.ContainerID); err != nil {
				return nil, nil, err
			}
		}
		fields, err := indexFields(rev.Payload, defs[key], weights[*r.ContainerID])
		if err != nil {
			return nil, nil, err
		}
		e := IndexEntry{Resource: r, Surface: p.Surface, Publishes: publishes[*r.ContainerID], RevisionID: rev.ID, ContentTypeID: catalogTypes[rev.ContentTypeID],
			Name: p.Name, Keywords: p.Keywords, Fields: fields, CreatedAt: p.ItemCreatedAt, CreatedBy: p.ItemCreatedBy, ModifiedAt: p.ModifiedAt, ModifiedBy: p.ModifiedBy}
		if rev.BlobID != nil {
			e.BlobID = *rev.BlobID
		}
		entries = append(entries, e)
	}
	return entries, gone, nil
}

// indexFields are a revision's values by field definition, with the
// field's current weight in weights (by key).
func indexFields(payload []byte, defs []*catalog.FieldDefinition, weights map[string]string) ([]IndexField, error) {
	values, err := decodeValues(payload)
	if err != nil {
		return nil, err
	}
	var out []IndexField
	for _, d := range defs {
		v, ok := values[d.Key]
		if !ok || v == nil {
			continue
		}
		list := []any{v}
		if many, ok := v.([]any); ok {
			list = many
		}
		field := IndexField{Definition: d, Weight: weights[d.Key], Values: list}
		if d.Type != catalog.FieldNote {
			for _, x := range list {
				value, err := indexValue(d, x)
				if err != nil {
					return nil, err
				}
				field.Indexed = append(field.Indexed, value)
			}
		}
		out = append(out, field)
	}
	return out, nil
}

// indexValue encodes a stored value as the field index does.
func indexValue(d *catalog.FieldDefinition, v any) (IndexValue, error) {
	switch d.Type {
	case catalog.FieldInteger:
		n, err := integer(v)
		return IndexValue{Int: n}, err
	case catalog.FieldDecimal:
		_, n, err := decimal(v, d.Scale)
		return IndexValue{Int: n}, err
	case catalog.FieldBoolean:
		if b, _ := v.(bool); b {
			return IndexValue{Int: 1}, nil
		}
		return IndexValue{}, nil
	case catalog.FieldNumber, catalog.FieldCurrency:
		key, err := numberKey(v)
		return IndexValue{Text: key}, err
	case catalog.FieldDatetime:
		t, err := time.Parse(time.RFC3339Nano, fmt.Sprint(v))
		return IndexValue{Text: t.UTC().Format("2006-01-02T15:04:05.000000000Z")}, err
	default:
		return IndexValue{Text: fmt.Sprint(v)}, nil
	}
}

// fieldWeights are the current search_weight of a collection's fields, by
// key: a field's weight applies to every revision once it is indexed again.
func (s *Service) fieldWeights(ctx context.Context, collectionID string) (map[string]string, error) {
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

// TermFamily expands a term to itself, its descendants and the terms
// merged into any of them, as under does in collection queries.
func (s *Service) TermFamily(ctx context.Context, id string) ([]string, error) {
	return read(ctx, s, func(t *Service) ([]string, error) {
		return t.DAL.Taxonomy().Family(ctx, id)
	})
}

// ContentTypeFamily expands a catalog type to itself and every catalog type
// deriving from it, as content type filters on indexed entries match; a list
// type stands for the catalog type it copies.
func (s *Service) ContentTypeFamily(ctx context.Context, id string) ([]string, error) {
	return read(ctx, s, func(t *Service) ([]string, error) {
		catalogTypes, err := t.DAL.Catalog().CatalogTypeIDs(ctx, []string{id})
		if err != nil {
			return nil, err
		}
		if id = catalogTypes[id]; id == "" {
			return []string{}, nil
		}
		ids, err := t.DAL.Catalog().Descendants(ctx, []string{id})
		if err != nil {
			return nil, err
		}
		family, err := t.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{IDs: ids, Catalog: true})
		if err != nil {
			return nil, err
		}
		out := make([]string, len(family))
		for i, f := range family {
			out[i] = f.ID
		}
		return out, nil
	})
}

// EntriesWithTerms lists, after an ID and in ID order, the live items of a
// workspace whose surfaces hold any of the terms in their values, or apply
// one of below as a parent or hold it as a keyword, optionally in one
// collection or outside some: an index re-reads them when a term changes.
func (s *Service) EntriesWithTerms(ctx context.Context, workspaceID, collectionID string, skip, termIDs, below []string, after string, limit int) ([]string, error) {
	if len(termIDs) == 0 && len(below) == 0 {
		return nil, nil
	}
	return s.DAL.Content().TermHolders(ctx, content.TermHolders{WorkspaceID: workspaceID, CollectionID: collectionID, Skip: skip, TermIDs: termIDs, Below: below, AfterID: after, Limit: limit})
}

// ReprocessFiles raises file.updated, with the reindex source, for the
// current file of each item that keep accepts, so the workflows that read
// files run on them again. Items without a file are skipped.
func (s *Service) ReprocessFiles(ctx context.Context, actor string, itemIDs []string, keep func(itemID, blobID string) (bool, error)) error {
	return s.write(ctx, func(t *Service) error {
		rows, err := t.DAL.Resources().Resources(ctx, resources.Filter{IDs: itemIDs, Kinds: []refs.ResourceKind{refs.KindItem}, WithHead: true})
		if err != nil {
			return err
		}
		for _, r := range rows {
			rev, err := t.revision(ctx, *r.HeadRevisionID)
			if err != nil {
				return err
			}
			if rev.BlobID == nil {
				continue
			}
			if ok, err := keep(r.ID, *rev.BlobID); err != nil || !ok {
				if err != nil {
					return err
				}
				continue
			}
			b, err := t.blob(ctx, *rev.BlobID)
			if err != nil {
				return err
			}
			t.emitFileChange(ctx, actor, r, b, rev, FileReindex, &b.ID)
		}
		return nil
	})
}
