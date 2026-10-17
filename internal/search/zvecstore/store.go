// Package zvecstore implements the optional single-node search store using
// the zvec Go SDK through cgo. It neither opens nor imports the SQLite store.
package zvecstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	zvec "github.com/zvec-ai/zvec-go"
	"papergo/internal/database"
	"papergo/internal/nativeheap"
	"papergo/internal/search"
)

const Name = "zvec"
const format = "papergo-zvec/1"
const layout = 2

// Keep elapsed activity times monotonic without allocating on every read.
var activityEpoch = time.Now()

const nativeIdleTimeout = 5 * time.Minute

type Options struct {
	Languages []string
	// KeywordCandidates bounds keyword matches and their counts/facets.
	KeywordCandidates int
	// NoRebuild refuses missing or incompatible stores (offline copy sources).
	NoRebuild bool
}

type catalog struct {
	Format           string
	Layout           int
	Languages        []string
	Model            string
	Dimensions       int
	Collection       string
	VectorCollection string
}

type request struct {
	ctx   context.Context
	apply func() error
	done  chan error
}
type passage struct{ document, chunk string }

// Store owns native text and vector collections, an in-memory catalog of source
// records and one bounded writer. Vectors are searched by HNSW, never by a
// Go scan. The source catalog supports exact filters, chunk jobs and export.
type Store struct {
	mu              sync.RWMutex
	path            string
	lease           *os.File
	meta            catalog
	handle          *zvec.Collection
	vectorHandle    *zvec.Collection
	stem            *search.Stemmer
	limit           int
	fresh           bool
	dirty           bool
	closed          bool
	documents       map[string]search.Record
	chunks          map[string]search.StoredChunk
	byFile          map[[2]string]map[string]search.StoredChunk
	byGeneration    map[string]map[string]search.StoredChunk
	vectors         map[string]search.StoredVector
	passages        map[string]passage
	writes          chan request
	stop            chan struct{}
	stopped         chan struct{}
	closeOnce       sync.Once
	closeErr        error
	optimizePending bool
	lastNativeUse   atomic.Int64
}

var _ search.Store = (*Store)(nil)

func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stem, err := search.NewStemmer(opts.Languages)
	if err != nil {
		return nil, err
	}
	if opts.KeywordCandidates == 0 {
		opts.KeywordCandidates = 10000
	}
	if opts.KeywordCandidates < 1 || opts.KeywordCandidates > 99999 {
		return nil, errors.New("zvec keyword candidates must be between 1 and 99999")
	}
	if err = initialize(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	lease, err := database.AcquireLease(abs, true)
	if err != nil {
		return nil, fmt.Errorf("zvec requires one process per store: %w", err)
	}
	s := &Store{path: abs, lease: lease, stem: stem, limit: opts.KeywordCandidates, documents: map[string]search.Record{}, chunks: map[string]search.StoredChunk{}, vectors: map[string]search.StoredVector{}, passages: map[string]passage{}, writes: make(chan request, 64), stop: make(chan struct{}), stopped: make(chan struct{})}
	s.byFile = map[[2]string]map[string]search.StoredChunk{}
	s.byGeneration = map[string]map[string]search.StoredChunk{}
	err = s.open(ctx, opts.NoRebuild)
	if err != nil {
		if s.handle != nil {
			s.handle.Close()
		}
		if s.vectorHandle != nil {
			s.vectorHandle.Close()
		}
		lease.Close()
		return nil, err
	}
	go s.writer()
	return s, nil
}

func (s *Store) open(ctx context.Context, noRebuild bool) error {
	raw, err := os.ReadFile(filepath.Join(s.path, "catalog.json"))
	if errors.Is(err, os.ErrNotExist) {
		if noRebuild {
			return errors.New("source zvec store is missing")
		}
		entries, e := os.ReadDir(s.path)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if len(entries) > 0 {
			return errors.New("zvec path contains an unrelated directory")
		}
		s.fresh = true
		s.meta = catalog{Format: format, Layout: layout, Languages: s.stem.Languages()}
		return nil
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &s.meta) != nil || s.meta.Format != format || (s.meta.Collection != "" && filepath.Base(s.meta.Collection) != s.meta.Collection) || s.meta.Collection == "." {
		return errors.New("not a PaperGo zvec store")
	}
	_, dirty := os.Stat(filepath.Join(s.path, "dirty"))
	if s.meta.Layout != layout || s.meta.Collection == "" || !slices.Equal(s.meta.Languages, s.stem.Languages()) || dirty == nil {
		if noRebuild {
			return errors.New("source zvec store or languages require a reindex")
		}
		// Only a recognized and exclusively locked store is disposable.
		if err = os.RemoveAll(s.path); err != nil {
			return err
		}
		s.meta = catalog{Format: format, Layout: layout, Languages: s.stem.Languages()}
		s.fresh = true
		return nil
	}
	if !errors.Is(dirty, os.ErrNotExist) {
		return dirty
	}
	if err = s.openCollections(); err != nil {
		return err
	}
	if err = s.readRecords(ctx); err != nil {
		return err
	}
	s.indexPassages()
	s.touchNative()
	return nil
}

// openCollections reopens persisted indexes without reloading the Go catalog
// or rebuilding vectors. The store is unpublished or exclusively locked.
func (s *Store) openCollections() error {
	if s.meta.VectorCollection != "" && (filepath.Base(s.meta.VectorCollection) != s.meta.VectorCollection || s.meta.VectorCollection == ".") {
		return errors.New("invalid zvec vector collection")
	}
	handle, err := zvec.Open(filepath.Join(s.path, s.meta.Collection), nil)
	if err != nil {
		return err
	}
	var vectorHandle *zvec.Collection
	if s.meta.VectorCollection != "" {
		vectorHandle, err = zvec.Open(filepath.Join(s.path, s.meta.VectorCollection), nil)
		if err != nil {
			return errors.Join(err, handle.Close())
		}
	}
	s.handle, s.vectorHandle = handle, vectorHandle
	s.touchNative()
	return nil
}

func (s *Store) touchNative() {
	s.lastNativeUse.Store(int64(time.Since(activityEpoch)))
}

func (s *Store) Name() string { return Name }
func (s *Store) Fresh() bool  { return s.fresh }

func hash(parts ...string) string {
	raw, _ := json.Marshal(parts)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])[:63]
}
func documentKey(d search.Document) string { return "d" + hash(d.ResourceID, d.Surface) }
func chunkKey(c search.StoredChunk) string {
	return "c" + hash(c.Generation, c.Chunk.Part, strconv.Itoa(c.Chunk.Ordinal))
}
func vectorKey(model, input string) string { return "v" + hash(model, input) }
func passageKey(d, c string) string        { return "p" + hash(d, c) }

func (s *Store) readRecords(ctx context.Context) error {
	options := zvec.NewIteratorOptions()
	defer options.Destroy()
	if err := options.SetIncludeVector(false); err != nil {
		return err
	}
	iterator, err := s.handle.CreateIterator(options)
	if err != nil {
		return err
	}
	defer iterator.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		doc, err := iterator.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		err = func() error {
			defer doc.Destroy()
			raw, err := nativePayload(doc)
			if err != nil {
				return err
			}
			if len(raw) == 0 {
				return nil
			}
			var record search.Record
			if err = json.Unmarshal(raw, &record); err != nil {
				return err
			}
			s.remember(record)
			return nil
		}()
		if err != nil {
			return err
		}
	}
}

func (s *Store) remember(record search.Record) {
	switch {
	case record.Document != nil:
		s.documents[documentKey(*record.Document)] = record
	case record.Chunk != nil:
		s.putChunk(*record.Chunk)
	case record.Vector != nil:
		s.vectors[vectorKey(record.Vector.Model, record.Vector.InputHash)] = *record.Vector
	}
}

func (s *Store) indexPassages() {
	s.passages = map[string]passage{}
	for dk, r := range s.documents {
		for ck, c := range s.byFile[[2]string{r.Document.ResourceID, r.Document.BlobID}] {
			if c.Published && c.ItemID == r.Document.ResourceID && c.BlobID == r.Document.BlobID {
				s.passages[passageKey(dk, ck)] = passage{dk, ck}
			}
		}
	}
}

func (s *Store) putChunk(c search.StoredChunk) {
	k := chunkKey(c)
	if old, ok := s.chunks[k]; ok && (old.ItemID != c.ItemID || old.BlobID != c.BlobID) {
		s.removeChunk(k)
	}
	s.chunks[k] = c
	file := [2]string{c.ItemID, c.BlobID}
	if s.byFile[file] == nil {
		s.byFile[file] = map[string]search.StoredChunk{}
	}
	s.byFile[file][k] = c
	if s.byGeneration[c.Generation] == nil {
		s.byGeneration[c.Generation] = map[string]search.StoredChunk{}
	}
	s.byGeneration[c.Generation][k] = c
}

func (s *Store) removeChunk(k string) {
	c := s.chunks[k]
	delete(s.chunks, k)
	file := [2]string{c.ItemID, c.BlobID}
	delete(s.byFile[file], k)
	if len(s.byFile[file]) == 0 {
		delete(s.byFile, file)
	}
	delete(s.byGeneration[c.Generation], k)
	if len(s.byGeneration[c.Generation]) == 0 {
		delete(s.byGeneration, c.Generation)
	}
}

func (s *Store) writer() {
	defer close(s.stopped)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.maintain(time.Now())
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		case r := <-s.writes:
			s.mu.Lock()
			err := r.ctx.Err()
			if err == nil && (s.closed || s.dirty) {
				err = search.ErrUnavailable
			}
			if err == nil {
				err = r.apply()
				if s.handle != nil {
					s.touchNative()
				}
			}
			s.mu.Unlock()
			r.done <- err
		}
	}
}

func (s *Store) write(ctx context.Context, apply func() error) error {
	r := request{ctx: ctx, apply: apply, done: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop:
		return search.ErrUnavailable
	case s.writes <- r:
	}
	select {
	case err := <-r.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopped:
		return search.ErrUnavailable
	}
}

func (s *Store) read(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.dirty {
		return search.ErrUnavailable
	}
	return fn()
}

// Catalog reads (status, exports, chunk jobs and filter-only queries) stay
// available while native indexes sleep. Only actual native queries wake them.
func (s *Store) readNative(ctx context.Context, fn func() error) error {
	for {
		asleep := false
		err := s.read(ctx, func() error {
			if s.handle == nil && s.meta.Collection != "" {
				asleep = true
				return nil
			}
			if s.handle != nil {
				defer s.touchNative()
			}
			return fn()
		})
		if err != nil || !asleep {
			return err
		}
		// Reopening shares the bounded writer and its exclusive lock. Loop
		// back under the read lock before exposing any handles to the query.
		if err = s.write(ctx, func() error {
			if s.handle != nil {
				return nil
			}
			return s.openCollections()
		}); err != nil {
			return fmt.Errorf("%w: reopen native indexes: %w", search.ErrUnavailable, err)
		}
	}
}

func (s *Store) saveCatalog() error {
	raw, err := json.Marshal(s.meta)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(s.path, "catalog.tmp"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(s.path, "catalog.tmp"), filepath.Join(s.path, "catalog.json")); err != nil {
		return err
	}
	return syncDirectory(s.path)
}

func (s *Store) ensure() error {
	if s.handle != nil {
		return nil
	}
	if s.meta.Collection != "" {
		return s.openCollections()
	}
	if err := os.MkdirAll(s.path, 0700); err != nil {
		return err
	}
	// Publish ownership before native creation. A process exit during schema
	// creation is recognizable as an incomplete derived store on restart.
	if err := s.saveCatalog(); err != nil {
		return err
	}
	if err := s.markDirty(); err != nil {
		return err
	}
	name := "text-" + uuid.NewString()
	h, err := createCollection(filepath.Join(s.path, name), 0)
	if err != nil {
		return err
	}
	s.handle = h
	s.meta.Collection = name
	return s.saveCatalog()
}

func (s *Store) markDirty() error {
	f, err := os.OpenFile(filepath.Join(s.path, "dirty"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = errors.Join(f.Sync(), f.Close(), syncDirectory(s.path))
	if err != nil {
		return err
	}
	s.dirty = true
	return nil
}

// mutation fences non-atomic native batches. A crash or partial failure
// leaves a durable dirty marker: queries stop and startup rebuilds derived
// data, so a partly applied batch never masquerades as a completed index.
func (s *Store) mutation(fn func() error) error {
	if err := s.ensure(); err != nil {
		return err
	}
	if err := s.markDirty(); err != nil {
		return err
	}
	var err error
	if err = fn(); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(s.path, "dirty")); err != nil {
		return err
	}
	if err = syncDirectory(s.path); err != nil {
		return err
	}
	s.dirty = false
	s.optimizePending = true
	return nil
}

// Seal writing segments and build their configured FTS/HNSW indexes. zvec
// searches recent buffered writes too; maintenance also bounds fragmentation.
func (s *Store) optimize() error {
	if s.handle == nil || s.dirty || !s.optimizePending {
		return nil
	}
	err := s.mutation(func() error {
		if err := s.handle.Optimize(); err != nil {
			return err
		}
		if s.vectorHandle != nil {
			return s.vectorHandle.Optimize()
		}
		return nil
	})
	if err == nil {
		s.optimizePending = false
	}
	return err
}

func (s *Store) maintain(now time.Time) error {
	defer nativeheap.Release()
	if s.dirty {
		return nil
	}
	if err := s.optimize(); err != nil {
		return err
	}
	if s.handle != nil && len(s.writes) == 0 && now.Sub(activityEpoch)-time.Duration(s.lastNativeUse.Load()) >= nativeIdleTimeout {
		if err := s.closeCollections(); err != nil {
			// A failed flush/close must never look like an empty, healthy
			// unloaded index. Keep the existing quarantine/rebuild contract.
			s.dirty = true
			s.closeErr = errors.Join(s.closeErr, err, s.markDirty())
			return s.closeErr
		}
	}
	return nil
}

// closeCollections leaves ownership and the source catalog intact. It runs
// only after readers/writers finish, on idle eviction or final Close.
func (s *Store) closeCollections() (err error) {
	if s.handle != nil {
		err = errors.Join(err, s.handle.Flush(), s.handle.Close())
		s.handle = nil
	}
	if s.vectorHandle != nil {
		err = errors.Join(err, s.vectorHandle.Flush(), s.vectorHandle.Close())
		s.vectorHandle = nil
	}
	return err
}

func (s *Store) sourceRow(record search.Record) *row {
	var key, kind string
	switch {
	case record.Document != nil:
		key, kind = documentKey(*record.Document), "doc"
	case record.Chunk != nil:
		key, kind = chunkKey(*record.Chunk), "chunk"
	case record.Vector != nil:
		key, kind = vectorKey(record.Vector.Model, record.Vector.InputHash), "vector"
	}
	r := newRow(key)
	r.text("row_key", key)
	r.text("row_kind", kind)
	raw, err := json.Marshal(record)
	if err != nil {
		r.err = err
	}
	r.text("payload", string(raw))
	if record.Document != nil {
		d := *record.Document
		s.metadata(r, d, key)
		meta := strings.Join([]string{d.Name, d.Keywords, d.Body}, "\n")
		text := meta
		for _, c := range s.published(d) {
			text += "\n" + search.EmbeddingInput(c.Chunk.Text, c.Chunk.Enrichment)
		}
		r.text("content", s.indexText(text))
		r.text("metadata", s.indexText(meta))
	}
	return r
}

func (s *Store) metadata(r *row, d search.Document, key string) {
	r.text("document_key", key)
	for column, value := range map[string]string{"resource_id": d.ResourceID, "surface": d.Surface, "scope_id": d.ScopeID, "workspace_id": d.WorkspaceID, "collection_id": d.CollectionID, "content_type_id": d.ContentTypeID, "created_by": d.CreatedBy, "modified_by": d.ModifiedBy, "kind": d.Kind} {
		r.text(column, encode(value))
	}
	r.number("views", int64(d.Views))
	r.number("modified_at", d.ModifiedAt.UnixNano())
	tokens := []string{}
	for _, v := range d.TermIDs {
		tokens = append(tokens, "t"+encode(v))
	}
	for _, v := range d.KeywordIDs {
		tokens = append(tokens, "g"+encode(v))
	}
	r.array("tokens", tokens)
}

func (s *Store) published(d search.Document) []search.StoredChunk {
	var out []search.StoredChunk
	if d.BlobID != "" {
		for _, c := range s.byFile[[2]string{d.ResourceID, d.BlobID}] {
			if c.Published && c.ItemID == d.ResourceID && c.BlobID == d.BlobID {
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chunk.Part != out[j].Chunk.Part {
			return out[i].Chunk.Part < out[j].Chunk.Part
		}
		return out[i].Chunk.Ordinal < out[j].Chunk.Ordinal
	})
	return out
}

// vectorRow mirrors the surface's metadata only when a real vector exists.
func (s *Store) vectorRow(pk string, p passage) *row {
	d := *s.documents[p.document].Document
	c := s.chunks[p.chunk]
	v, ok := s.vectors[vectorKey(s.meta.Model, c.Chunk.InputHash)]
	if !ok || len(v.Vector) != s.meta.Dimensions {
		return nil
	}
	r := newRow(pk)
	s.metadata(r, d, p.document)
	r.text("row_key", pk)
	r.text("row_kind", "passage")
	r.vector(v.Vector)
	return r
}

func (s *Store) syncResources(ids map[string]bool, contentChanged bool) error {
	expected := map[string]passage{}
	var rows, updates, vectorRows []*row
	removeVectors := []string{}
	for dk, record := range s.documents {
		d := *record.Document
		if !ids[d.ResourceID] {
			continue
		}
		rows = append(rows, s.sourceRow(record))
		for _, c := range s.published(d) {
			ck := chunkKey(c)
			pk := passageKey(dk, ck)
			p := passage{dk, ck}
			expected[pk] = p
			r := newRow(pk)
			s.metadata(r, d, dk)
			if _, exists := s.passages[pk]; exists && !contentChanged {
				updates = append(updates, r)
			} else {
				r.text("row_key", pk)
				r.text("row_kind", "passage")
				r.text("payload", "")
				r.text("content", s.indexText(search.EmbeddingInput(c.Chunk.Text, c.Chunk.Enrichment)))
				rows = append(rows, r)
			}
			if s.vectorHandle != nil {
				if v := s.vectorRow(pk, p); v != nil {
					vectorRows = append(vectorRows, v)
				} else {
					removeVectors = append(removeVectors, pk)
				}
			}
		}
	}
	// Own all native row allocations even if a delete/write fails.
	defer func() {
		for _, batch := range [][]*row{rows, updates, vectorRows} {
			for _, r := range batch {
				if r.doc != nil {
					r.close()
				}
			}
		}
	}()
	stale := []string{}
	for pk, p := range s.passages {
		record, exists := s.documents[p.document]
		if !exists || ids[record.Document.ResourceID] {
			if _, keep := expected[pk]; !keep {
				stale = append(stale, pk)
				delete(s.passages, pk)
			}
		}
	}
	if len(stale) > 0 {
		if err := s.handle.DeleteByFilter(in("row_key", stale)); err != nil {
			return err
		}
	}
	if s.vectorHandle != nil {
		removeVectors = append(removeVectors, stale...)
		if len(removeVectors) > 0 {
			if err := s.vectorHandle.DeleteByFilter(in("row_key", removeVectors)); err != nil {
				return err
			}
		}
	}
	if err := writeRows(s.handle, rows, false); err != nil {
		return err
	}
	if err := writeRows(s.handle, updates, true); err != nil {
		return err
	}
	if s.vectorHandle != nil {
		if err := writeRows(s.vectorHandle, vectorRows, false); err != nil {
			return err
		}
	}
	for pk, p := range expected {
		s.passages[pk] = p
	}
	return nil
}

func (s *Store) UpsertDocuments(ctx context.Context, docs []search.Document, stamp int64) error {
	// Copy all caller-owned slices before a queued write can outlive ctx.
	raw, err := json.Marshal(docs)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &docs); err != nil {
		return err
	}
	for _, d := range docs {
		if err := validateDocument(d); err != nil {
			return err
		}
	}
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			ids := map[string]bool{}
			for _, d := range docs {
				d := d
				s.documents[documentKey(d)] = search.Record{Document: &d, Stamp: stamp}
				ids[d.ResourceID] = true
			}
			return s.syncResources(ids, false)
		})
	})
}

func (s *Store) DeleteDocuments(ctx context.Context, sel search.Selection) error {
	sel.ResourceIDs = slices.Clone(sel.ResourceIDs)
	sel.Keys = slices.Clone(sel.Keys)
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			var removed []string
			ids := map[string]bool{}
			for dk, r := range s.documents {
				d := *r.Document
				drop := slices.Contains(sel.ResourceIDs, d.ResourceID) || sel.Before > 0 && r.Stamp < sel.Before
				for _, k := range sel.Keys {
					drop = drop || k.ResourceID == d.ResourceID && k.Surface == d.Surface
				}
				if drop {
					removed = append(removed, dk)
					ids[d.ResourceID] = true
					delete(s.documents, dk)
				}
			}
			liveItems := map[string]bool{}
			for _, r := range s.documents {
				liveItems[r.Document.ResourceID] = true
			}
			for ck, c := range s.chunks {
				keep := liveItems[c.ItemID]
				if slices.Contains(sel.ResourceIDs, c.ItemID) || sel.Before > 0 && !keep {
					removed = append(removed, ck)
					s.removeChunk(ck)
				}
			}
			if len(removed) > 0 {
				if err := s.handle.DeleteByFilter(in("row_key", removed)); err != nil {
					return err
				}
			}
			return s.syncResources(ids, false)
		})
	})
}

func (s *Store) SetScopes(ctx context.Context, scopes map[string]string) error {
	scopes = maps.Clone(scopes)
	return s.write(ctx, func() error {
		return s.mutation(func() error {
			ids := map[string]bool{}
			for dk, r := range s.documents {
				if scope, ok := scopes[r.Document.ResourceID]; ok {
					d := *r.Document
					d.ScopeID = scope
					r.Document = &d
					s.documents[dk] = r
					ids[d.ResourceID] = true
				}
			}
			return s.syncResources(ids, false)
		})
	})
}

func (s *Store) Keys(ctx context.Context, ids []string) (out []search.Key, err error) {
	err = s.read(ctx, func() error {
		for _, r := range s.documents {
			d := *r.Document
			if slices.Contains(ids, d.ResourceID) {
				out = append(out, search.Key{ResourceID: d.ResourceID, Surface: d.Surface})
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].ResourceID != out[j].ResourceID {
				return out[i].ResourceID < out[j].ResourceID
			}
			return out[i].Surface < out[j].Surface
		})
		return nil
	})
	return
}

func (s *Store) Stats(ctx context.Context) (out search.Stats, err error) {
	err = s.read(ctx, func() error {
		out.Documents = int64(len(s.documents))
		for _, c := range s.chunks {
			if c.Published {
				out.Chunks++
			}
		}
		err := filepath.WalkDir(s.path, func(path string, d os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !d.IsDir() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				out.Bytes += info.Size()
			}
			return ctx.Err()
		})
		return err
	})
	return
}

func (s *Store) Export(ctx context.Context, yield func(search.Record) error) error {
	return s.read(ctx, func() error {
		keys := make([]string, 0, len(s.documents))
		for k := range s.documents {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			raw, err := json.Marshal(s.documents[k])
			if err != nil {
				return err
			}
			var record search.Record
			if err = json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if err := yield(record); err != nil {
				return err
			}
		}
		keys = keys[:0]
		for k := range s.chunks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			c := s.chunks[k]
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := yield(search.Record{Chunk: &c}); err != nil {
				return err
			}
		}
		keys = keys[:0]
		for k := range s.vectors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := s.vectors[k]
			v.Vector = slices.Clone(v.Vector)
			if v.UnusedSince != nil {
				stamp := *v.UnusedSince
				v.UnusedSince = &stamp
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := yield(search.Record{Vector: &v}); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateDocument(d search.Document) error {
	if d.ResourceID == "" || (d.Surface != search.SurfaceHead && d.Surface != search.SurfacePublished) {
		return errors.New("invalid search document")
	}
	return nil
}

func (s *Store) Import(ctx context.Context, records []search.Record) error {
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &records); err != nil {
		return err
	}
	for _, r := range records {
		n := 0
		if r.Document != nil {
			n++
			if err := validateDocument(*r.Document); err != nil {
				return err
			}
		}
		if r.Chunk != nil {
			n++
			c := r.Chunk
			if c.ItemID == "" || c.BlobID == "" || c.Generation == "" {
				return errors.New("invalid stored chunk")
			}
		}
		if r.Vector != nil {
			n++
			v := r.Vector
			if err := validateStoredVector(*v); err != nil {
				return err
			}
		}
		if n != 1 {
			return errors.New("import needs exactly one source record")
		}
	}
	return s.write(ctx, func() error {
		dimensions := map[string]int{}
		for _, v := range s.vectors {
			dimensions[v.Model] = len(v.Vector)
		}
		for _, r := range records {
			if v := r.Vector; v != nil {
				if dims := dimensions[v.Model]; dims != 0 && dims != len(v.Vector) {
					return errors.New("inconsistent embedding model dimensions")
				}
				dimensions[v.Model] = len(v.Vector)
			}
		}
		return s.mutation(func() error {
			ids := map[string]bool{}
			rows := []*row{}
			for _, r := range records {
				if r.Vector != nil && r.Vector.Model != s.meta.Model {
					if err := s.switchModel(r.Vector.Model, len(r.Vector.Vector)); err != nil {
						return err
					}
				}
				s.remember(r)
				if r.Document != nil {
					ids[r.Document.ResourceID] = true
				} else {
					rows = append(rows, s.sourceRow(r))
					if r.Chunk != nil {
						ids[r.Chunk.ItemID] = true
					}
				}
			}
			if err := writeRows(s.handle, rows, false); err != nil {
				return err
			}
			if len(s.vectors) > 0 {
				for _, d := range s.documents {
					ids[d.Document.ResourceID] = true
				}
			}
			return s.syncResources(ids, true)
		})
	})
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.stopped
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		s.closeErr = errors.Join(s.closeErr, s.optimize(), s.closeCollections())
		s.closeErr = errors.Join(s.closeErr, s.lease.Close())
		nativeheap.Release()
	})
	return s.closeErr
}

// switchModel builds only the vector space. Text/source records stay in
// their own collection; every row in HNSW has a non-null real vector.
func (s *Store) switchModel(model string, dims int) error {
	if model == s.meta.Model && dims == s.meta.Dimensions && s.vectorHandle != nil {
		return nil
	}
	if dims < 1 || dims > 65536 {
		return errors.New("invalid zvec vector dimensions")
	}
	oldHandle, oldMeta := s.vectorHandle, s.meta
	name := hash(model)[:16] + "-" + strconv.Itoa(dims) + "-" + uuid.NewString()
	h, err := createCollection(filepath.Join(s.path, name), dims)
	if err != nil {
		return err
	}
	s.vectorHandle = h
	s.meta.Model = model
	s.meta.Dimensions = dims
	s.meta.VectorCollection = name
	rows := []*row{}
	for pk, p := range s.passages {
		if r := s.vectorRow(pk, p); r != nil {
			rows = append(rows, r)
		}
	}
	err = writeRows(h, rows, false)
	if err == nil {
		err = h.Flush()
	}
	if err == nil {
		err = s.saveCatalog()
	}
	if err != nil {
		h.Close()
		s.vectorHandle = oldHandle
		s.meta = oldMeta
		return err
	}
	if oldHandle != nil {
		if err = oldHandle.Close(); err != nil {
			return err
		}
		return os.RemoveAll(filepath.Join(s.path, oldMeta.VectorCollection))
	}
	return nil
}
