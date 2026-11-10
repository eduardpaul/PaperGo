package dms

import (
	"context"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"

	"papergo/internal/dal/catalog"
)

// batchCache keeps what stays the same between the operations of one batch:
// a batch only creates, changes and deletes items and folders, so the
// collections the caller may write to and their field definitions do not
// change. Imports otherwise check the same list and load its columns again
// for every item. The folders the caller may write to are kept until an
// operation touches a folder, which can delete or move one.
type batchCache struct {
	subject     string
	writable    map[string]*resources.Resource
	collections map[string]*resources.Resource
	defs        map[string][]*catalog.FieldDefinition
	// Schema revisions never change once written.
	schemas map[string]*catalog.SchemaRevision
}

func newBatchCache(subject string) *batchCache {
	return &batchCache{subject: subject, writable: map[string]*resources.Resource{}, collections: map[string]*resources.Resource{}, defs: map[string][]*catalog.FieldDefinition{}, schemas: map[string]*catalog.SchemaRevision{}}
}

// authorizeWrite authorizes a write to id, deciding a collection or folder
// once per batch, until the batch changes a folder.
func (s *Service) authorizeWrite(ctx context.Context, subject, id string) (*resources.Resource, error) {
	cache := s.batch
	if cache != nil && cache.subject == subject {
		if r, ok := cache.writable[id]; ok {
			copied := *r
			return &copied, nil
		}
	}
	r, err := s.authorize(ctx, subject, id, "write")
	if err == nil && cache != nil && cache.subject == subject && (r.Kind == refs.KindList || r.Kind == refs.KindLibrary || r.Kind == refs.KindFolder) {
		copied := *r
		cache.writable[id] = &copied
	}
	return r, err
}

// batchTouches tells the batch that an operation is about to change r. A
// folder's deletion, move or change undoes what the batch knows of folders:
// the folder itself and those below it.
func (s *Service) batchTouches(r *resources.Resource) {
	if s.batch == nil || r.Kind != refs.KindFolder {
		return
	}
	for id, kept := range s.batch.writable {
		if kept.Kind == refs.KindFolder {
			delete(s.batch.writable, id)
		}
	}
}

// collectionDefinitions returns a collection's field definitions, loaded once per batch.
func (s *Service) collectionDefinitions(ctx context.Context, collection string) ([]*catalog.FieldDefinition, error) {
	if s.batch != nil {
		if defs, ok := s.batch.defs[collection]; ok {
			return append([]*catalog.FieldDefinition(nil), defs...), nil
		}
	}
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: collection})
	if err == nil && s.batch != nil {
		s.batch.defs[collection] = defs
	}
	return append([]*catalog.FieldDefinition(nil), defs...), err
}

// batchCollection reads a collection, once per batch. Items' writes do not
// change the collection row a batch reads: its schema head and settings.
func (s *Service) batchCollection(ctx context.Context, id string) (*resources.Resource, error) {
	if s.batch != nil {
		if c, ok := s.batch.collections[id]; ok {
			copied := *c
			return &copied, nil
		}
	}
	c, err := s.DAL.Resources().Resource(ctx, id)
	if err == nil && s.batch != nil {
		copied := *c
		s.batch.collections[id] = &copied
	}
	return c, err
}

// schemaRevision reads an immutable schema revision, once per batch.
func (s *Service) schemaRevision(ctx context.Context, id string) (*catalog.SchemaRevision, error) {
	if s.batch != nil {
		if schema, ok := s.batch.schemas[id]; ok {
			return schema, nil
		}
	}
	schema, err := s.DAL.Catalog().Schema(ctx, id)
	if err == nil && s.batch != nil {
		s.batch.schemas[id] = schema
	}
	return schema, err
}
