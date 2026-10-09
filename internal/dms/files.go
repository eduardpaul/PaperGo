package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"papergo/ent"
	"papergo/ent/blob"
	"papergo/ent/resource"
	"strconv"
	"strings"
)

// Library files present a WebDAV-enabled library as a file system: the library
// and its folders are collections, and items are files whose bytes are the blob
// of the caller's visible revision. Every path segment is a visible name.
//
// Live library siblings have unique head names ignoring case (resources.name_key).
// A reader of an explicit-publishing library may still see an older published
// name equal to a sibling's; then the owner of that head name wins, else the
// lowest ID, so listings and path resolution always agree.

// MaxFolderEntries bounds one folder listing, which is read in a single snapshot.
const MaxFolderEntries = 10000

// emptySHA256 identifies the content of an item that has no blob yet.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type File struct {
	*ent.Resource
	// Blob is the content of the visible revision; nil for collections and empty items.
	Blob *ent.Blob
}

func (f *File) Collection() bool { return f.Kind != resource.KindItem }
func (f *File) Size() int64 {
	if f.Blob == nil {
		return 0
	}
	return f.Blob.Size
}
func (f *File) ContentType() string {
	if f.Blob == nil {
		return "application/octet-stream"
	}
	return f.Blob.ContentType
}

// ETag is a strong validator: the content hash of a file, or the lock version of a collection.
func (f *File) ETag() string {
	switch {
	case f.Collection():
		return strconv.Quote(f.ID + "." + strconv.Itoa(f.Version))
	case f.Blob == nil:
		return strconv.Quote(emptySHA256)
	default:
		return strconv.Quote(f.Blob.Sha256)
	}
}

// FileConditions are HTTP preconditions, evaluated in the write transaction.
type FileConditions struct{ IfMatch, IfNoneMatch string }

func (c FileConditions) check(existing *File) error {
	if c.IfMatch != "" && (existing == nil || !etagListMatches(c.IfMatch, existing.ETag())) {
		return ErrPreconditionFailed
	}
	if c.IfNoneMatch != "" && existing != nil && etagListMatches(c.IfNoneMatch, existing.ETag()) {
		return ErrPreconditionFailed
	}
	return nil
}
func etagListMatches(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimPrefix(strings.TrimSpace(v), "W/")
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}

// LibraryFile resolves a path below a WebDAV-enabled library; an empty path is the library itself.
func (s *Service) LibraryFile(ctx context.Context, subject, libraryID string, path []string) (*File, error) {
	return read(ctx, s, func(t *Service) (*File, error) {
		_, f, err := t.resolveFile(ctx, subject, libraryID, path)
		return f, err
	})
}

// LibraryFolder resolves a path and, for a collection, lists its visible entries.
func (s *Service) LibraryFolder(ctx context.Context, subject, libraryID string, path []string) (*File, []*File, error) {
	type listing struct {
		folder  *File
		entries []*File
	}
	out, err := read(ctx, s, func(t *Service) (listing, error) {
		_, f, err := t.resolveFile(ctx, subject, libraryID, path)
		if err != nil || !f.Collection() {
			return listing{folder: f}, err
		}
		entries, err := t.visibleFiles(ctx, subject, t.Client.Resource.Query().Where(resource.ParentIDEQ(f.ID)))
		return listing{f, uniqueNames(entries)}, err
	})
	return out.folder, out.entries, err
}

// WriteTarget authorizes writing the entry at path, or creating one there, and
// returns the existing entry (nil when the path is unmapped). Uploads call it
// before storing any bytes; PutFile repeats every check transactionally.
func (s *Service) WriteTarget(ctx context.Context, subject, libraryID string, path []string, cond FileConditions) (*File, error) {
	return read(ctx, s, func(t *Service) (*File, error) {
		if len(path) == 0 {
			_, lib, err := t.resolveFile(ctx, subject, libraryID, nil)
			if err == nil {
				_, err = t.authorize(ctx, subject, lib.ID, "write")
			}
			return lib, err
		}
		_, parent, existing, err := t.target(ctx, subject, libraryID, path, cond)
		switch {
		case err != nil:
		case existing == nil:
			_, err = t.authorize(ctx, subject, parent.ID, "write")
		case existing.Collection():
			_, err = t.authorize(ctx, subject, existing.ID, "write")
		default:
			err = t.CanUpload(ctx, subject, existing.ID, existing.Version)
		}
		return existing, err
	})
}

type PutFile struct {
	FileConditions
	Blob BlobInput
	// Tags and Values describe a new file; replacing content keeps its metadata.
	Tags          []string
	Values        map[string]any
	ContentTypeID string
}

// PutFile stores content at path in one transaction: it creates the item with
// its first revision, or adds a content revision to the existing file.
func (s *Service) PutFile(ctx context.Context, subject, libraryID string, path []string, in PutFile) (out *File, created bool, err error) {
	err = s.write(ctx, func(t *Service) error {
		_, parent, existing, e := t.target(ctx, subject, libraryID, path, in.FileConditions)
		if e != nil {
			return e
		}
		if existing != nil && existing.Collection() {
			return ErrExists
		}
		id := ""
		if existing != nil {
			if _, e = t.attachBlob(ctx, subject, existing.ID, existing.Version, in.Blob); e != nil {
				return e
			}
			id = existing.ID
		} else {
			r, e := t.create(ctx, subject, parent.ID, CreateResource{Kind: "item", Name: path[len(path)-1], Tags: in.Tags, Values: in.Values, ContentTypeID: in.ContentTypeID}, &in.Blob)
			if e != nil {
				return e
			}
			id, created = r.ID, true
		}
		out, e = t.fileByID(ctx, subject, id)
		return e
	})
	return
}

// MakeFolder creates the folder at path; its parent must exist.
func (s *Service) MakeFolder(ctx context.Context, subject, libraryID string, path []string) (out *File, err error) {
	err = s.write(ctx, func(t *Service) error {
		lib, parent, name, e := t.resolveParent(ctx, subject, libraryID, path)
		if e != nil {
			return e
		}
		if _, e = t.fileChild(ctx, subject, lib, parent.ID, name); e == nil {
			return ErrExists
		} else if !errors.Is(e, ErrNotFound) {
			return e
		}
		r, e := t.create(ctx, subject, parent.ID, CreateResource{Kind: "folder", Name: name}, nil)
		out = &File{Resource: r}
		return e
	})
	return
}

// DeleteFile deletes the entry at path, with everything below it.
func (s *Service) DeleteFile(ctx context.Context, subject, libraryID string, path []string, cond FileConditions) error {
	if len(path) == 0 {
		return ErrForbidden
	}
	return s.write(ctx, func(t *Service) error {
		_, f, e := t.resolveFile(ctx, subject, libraryID, path)
		if e != nil {
			return e
		}
		if e = cond.check(f); e != nil {
			return e
		}
		return t.delete(ctx, subject, f.ID, f.Version)
	})
}

// MoveFile renames or moves an entry within its library. With overwrite, an
// entry already at the destination is deleted in the same transaction;
// created reports that nothing was overwritten.
func (s *Service) MoveFile(ctx context.Context, subject, libraryID string, from, to []string, overwrite bool) (created bool, err error) {
	if len(from) == 0 || len(to) == 0 {
		return false, ErrForbidden
	}
	err = s.write(ctx, func(t *Service) error {
		_, src, e := t.resolveFile(ctx, subject, libraryID, from)
		if e != nil {
			return e
		}
		lib, parent, name, e := t.resolveParent(ctx, subject, libraryID, to)
		if e != nil {
			return e
		}
		existing, e := t.fileChild(ctx, subject, lib, parent.ID, name)
		if errors.Is(e, ErrNotFound) {
			existing, e = nil, nil
		}
		if e != nil {
			return e
		}
		// The destination may name the source itself, as in a case-only rename.
		created = existing == nil || existing.ID == src.ID
		if !created {
			if !overwrite {
				return ErrPreconditionFailed
			}
			if e = t.delete(ctx, subject, existing.ID, existing.Version); e != nil {
				return e
			}
		}
		var in UpdateResource
		if src.ParentID == nil || *src.ParentID != parent.ID {
			in.ParentID = &parent.ID
		}
		if name != src.Name {
			in.Name = &name
		}
		if in.ParentID == nil && in.Name == nil {
			return nil
		}
		_, e = t.update(ctx, subject, src.ID, src.Version, in)
		return e
	})
	return
}

// resolveFile returns the library whenever it is accessible, even if the path is not.
func (s *Service) resolveFile(ctx context.Context, subject, libraryID string, path []string) (*ent.Resource, *File, error) {
	lib, err := s.authorize(ctx, subject, libraryID, "read")
	if err != nil {
		return nil, nil, err
	}
	if lib.Kind != resource.KindLibrary || !lib.WebdavEnabled {
		return nil, nil, ErrNotFound
	}
	if len(path) > MaxDepth {
		return lib, nil, ErrNotFound
	}
	f := &File{Resource: lib}
	for _, name := range path {
		if !f.Collection() {
			return lib, nil, ErrNotFound
		}
		if f, err = s.fileChild(ctx, subject, lib, f.ID, name); err != nil {
			return lib, nil, err
		}
	}
	return lib, f, nil
}

// resolveParent resolves the collection that holds the last path segment.
func (s *Service) resolveParent(ctx context.Context, subject, libraryID string, path []string) (*ent.Resource, *File, string, error) {
	if len(path) == 0 {
		return nil, nil, "", ErrForbidden
	}
	lib, parent, err := s.resolveFile(ctx, subject, libraryID, path[:len(path)-1])
	if errors.Is(err, ErrNotFound) && lib != nil || err == nil && !parent.Collection() {
		return nil, nil, "", ErrMissingParent
	}
	if err != nil {
		return nil, nil, "", err
	}
	return lib, parent, path[len(path)-1], nil
}

// target resolves the parent of path and the entry already at path, if any.
func (s *Service) target(ctx context.Context, subject, libraryID string, path []string, cond FileConditions) (*ent.Resource, *File, *File, error) {
	lib, parent, name, err := s.resolveParent(ctx, subject, libraryID, path)
	if err != nil {
		return nil, nil, nil, err
	}
	existing, err := s.fileChild(ctx, subject, lib, parent.ID, name)
	if errors.Is(err, ErrNotFound) {
		existing, err = nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return lib, parent, existing, cond.check(existing)
}

// fileChild finds the visible entry named name directly below parentID.
func (s *Service) fileChild(ctx context.Context, subject string, lib *ent.Resource, parentID, name string) (*File, error) {
	key := nameKey(name)
	q := s.Client.Resource.Query().Where(resource.ParentIDEQ(parentID))
	if lib.PublishingEnabled {
		// Readers may see a published name that differs from the indexed head name.
		q.Where(func(sel *entsql.Selector) {
			sel.Where(entsql.Or(
				entsql.EQ(sel.C(resource.FieldNameKey), key),
				entsql.ExprP(sel.C(resource.FieldID)+` IN (SELECT p.item_id FROM item_surfaces p JOIN resources c ON c.id=p.item_id WHERE c.parent_id=? AND p.surface='published' AND unicode_lower(p.name)=?)`, parentID, key),
			))
		})
	} else {
		q.Where(resource.NameKeyEQ(key))
	}
	files, err := s.visibleFiles(ctx, subject, q)
	if err != nil {
		return nil, err
	}
	for _, f := range uniqueNames(files) {
		if nameKey(f.Name) == key {
			return f, nil
		}
	}
	return nil, ErrNotFound
}
func (s *Service) fileByID(ctx context.Context, subject, id string) (*File, error) {
	files, err := s.visibleFiles(ctx, subject, s.Client.Resource.Query().Where(resource.IDEQ(id)))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNotFound
	}
	return files[0], nil
}

// visibleFiles loads the live, readable, visible resources of q in ID order,
// with each item's visible surface and the blob of its visible revision.
func (s *Service) visibleFiles(ctx context.Context, subject string, q *ent.ResourceQuery) ([]*File, error) {
	rows, err := q.Where(resource.DeletedAtIsNil(), permissionPredicate(subject, "read"), func(sel *entsql.Selector) {
		text, args := visibleSQL(sel.C(resource.FieldID), subject, "auto")
		sel.Where(entsql.ExprP(text, args...))
	}).Order(ent.Asc(resource.FieldID)).Limit(MaxFolderEntries + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > MaxFolderEntries {
		return nil, invalid("folders with more than " + strconv.Itoa(MaxFolderEntries) + " entries cannot be listed as files")
	}
	revisions, err := s.overlayPage(ctx, subject, "auto", rows, nil)
	if err != nil {
		return nil, err
	}
	blobIDs := []string{}
	for _, rev := range revisions {
		if rev.BlobID != nil {
			blobIDs = append(blobIDs, *rev.BlobID)
		}
	}
	blobs := map[string]*ent.Blob{}
	if len(blobIDs) > 0 {
		rows, err := s.Client.Blob.Query().Where(blob.IDIn(blobIDs...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range rows {
			blobs[b.ID] = b
		}
	}
	out := make([]*File, 0, len(rows))
	for _, r := range rows {
		f := &File{Resource: r}
		if rev := revisions[r.ID]; rev != nil && rev.BlobID != nil {
			f.Blob = blobs[*rev.BlobID]
		}
		out = append(out, f)
	}
	return out, nil
}

// uniqueNames keeps one entry per visible name: the owner of that head name,
// else the first (lowest ID) entry.
func uniqueNames(files []*File) []*File {
	out := make([]*File, 0, len(files))
	index := map[string]int{}
	for _, f := range files {
		key := nameKey(f.Name)
		i, seen := index[key]
		if !seen {
			index[key] = len(out)
			out = append(out, f)
		} else if ownsName(f, key) && !ownsName(out[i], key) {
			out[i] = f
		}
	}
	return out
}
func ownsName(f *File, key string) bool { return f.NameKey != nil && *f.NameKey == key }
