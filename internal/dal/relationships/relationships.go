// Package relationships owns the typed and untyped links between items and
// the relationship types whose policies govern typed links. A type's id is
// the id of its predicate term in the relationship types term set. Drivers
// implement Queries; this package decides what those statements mean.
package relationships

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/taxonomy"
)

// Type is a relationship type's policy. Undirected types have no inverse
// label and no endpoint limits.
type Type struct {
	ID, InverseLabel         string
	Directed                 bool
	MaxIncoming, MaxOutgoing *int
	// Attributes is the schema of the metadata typed links carry.
	Attributes           json.RawMessage
	Version              int
	CreatedAt, UpdatedAt time.Time
}

// Same reports whether two types behave alike. Attributes compare as JSON
// values: PostgreSQL does not keep their original text.
func (t Type) Same(o Type) bool {
	var a, b any
	return t.Directed == o.Directed && t.InverseLabel == o.InverseLabel && sameLimit(t.MaxIncoming, o.MaxIncoming) &&
		sameLimit(t.MaxOutgoing, o.MaxOutgoing) && json.Unmarshal(t.Attributes, &a) == nil && json.Unmarshal(o.Attributes, &b) == nil &&
		reflect.DeepEqual(a, b)
}

// Limited reports whether the type bounds its endpoints' links.
func (t Type) Limited() bool { return t.MaxIncoming != nil || t.MaxOutgoing != nil }

func sameLimit(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// Relationship links two items. An undirected link keeps the lower item id
// as its source. An empty TypeID is an untyped link.
type Relationship struct {
	ID, SourceID, TargetID, TypeID string
	Directed                       bool
	Metadata                       map[string]any
	Version                        int
	CreatedAt                      time.Time
}

// Peer is the other end of the link from id.
func (r Relationship) Peer(id string) string {
	if r.TargetID == id {
		return r.SourceID
	}
	return r.TargetID
}

// Filter selects links ordered by id. Zero fields do not filter.
type Filter struct {
	IDs []string
	// Endpoint selects links at either end of an item; Endpoints of any of
	// the items.
	Endpoint           string
	Endpoints          []string
	SourceID, TargetID string
	// TypeIDs selects links of these types; Untyped selects untyped links.
	TypeIDs  []string
	Untyped  bool
	Directed *bool
	// Workspace selects links with an end in the workspace.
	Workspace string
	AfterID   string
	Limit     int
}

// Queries are the statements a driver runs for relationships. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	Term(ctx context.Context, id string) (taxonomy.Term, error)
	TermSet(ctx context.Context, id string) (taxonomy.Set, error)
	RelationshipType(ctx context.Context, id string) (Type, error)
	InsertRelationshipType(ctx context.Context, t Type) error
	DeleteRelationshipType(ctx context.Context, id string) error
	// LockRelationshipType serializes links of a type.
	LockRelationshipType(ctx context.Context, id string) error
	Relationship(ctx context.Context, id string) (Relationship, error)
	Relationships(ctx context.Context, f Filter) ([]Relationship, error)
	CountRelationships(ctx context.Context, f Filter) (int, error)
	InsertRelationship(ctx context.Context, r Relationship) error
	// UpdateRelationship writes r's type and metadata when its version still
	// is r.Version-1.
	UpdateRelationship(ctx context.Context, r Relationship) (bool, error)
	DeleteRelationship(ctx context.Context, id string) error
}

// Repo applies the relationship rules for every engine. read runs single
// statements; write runs a function in one transaction, joining the
// caller's when there is one.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Type returns a relationship type, or dberr.ErrNotFound.
func (r *Repo) Type(ctx context.Context, id string) (Type, error) {
	return r.read.RelationshipType(ctx, id)
}

// CreateType adds the policy of a current, unmerged term of the
// relationship types set.
func (r *Repo) CreateType(ctx context.Context, t Type) (Type, error) {
	if !t.Directed && (t.InverseLabel != "" || t.Limited()) {
		return Type{}, fmt.Errorf("%w: undirected relationship types have no inverse label or endpoint limits", dberr.ErrCheckViolation)
	}
	if len(t.Attributes) == 0 {
		t.Attributes = json.RawMessage(`{}`)
	}
	t.Version = 1
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	return t, r.write(ctx, func(q Queries) error {
		if err := predicate(ctx, q, t.ID); err != nil {
			return err
		}
		return q.InsertRelationshipType(ctx, t)
	})
}

// DeleteType deletes a type no link uses.
func (r *Repo) DeleteType(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error { return q.DeleteRelationshipType(ctx, id) })
}

// predicate checks that id is a current term of the relationship types set.
func predicate(ctx context.Context, q Queries, id string) error {
	term, err := q.Term(ctx, id)
	if dberr.IsNotFound(err) {
		return fmt.Errorf("%w: predicate term not found", dberr.ErrInvalidReference)
	}
	if err != nil {
		return err
	}
	if term.Deprecated {
		return fmt.Errorf("%w: relationship types need an active predicate term", dberr.ErrInvalidReference)
	}
	set, err := q.TermSet(ctx, term.SetID)
	if err != nil {
		return err
	}
	if set.Key != taxonomy.RelationshipTypesSetKey {
		return fmt.Errorf("%w: relationship types need a term of the %q set", dberr.ErrInvalidReference, taxonomy.RelationshipTypesSetKey)
	}
	return nil
}

// Find returns a link, or dberr.ErrNotFound.
func (r *Repo) Find(ctx context.Context, id string) (Relationship, error) {
	return r.read.Relationship(ctx, id)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Relationship, error) {
	if f.empty() {
		return []Relationship{}, nil
	}
	return r.read.Relationships(ctx, f)
}

func (r *Repo) Count(ctx context.Context, f Filter) (int, error) {
	if f.empty() {
		return 0, nil
	}
	return r.read.CountRelationships(ctx, f)
}

// empty reports whether f selects nothing: an empty endpoint list.
func (f Filter) empty() bool { return f.Endpoints != nil && len(f.Endpoints) == 0 }

// Existing returns the link that linking in would return, if there is one.
func (r *Repo) Existing(ctx context.Context, in Relationship) (Relationship, bool, error) {
	return existing(ctx, r.read, canonical(in))
}

// canonical puts an undirected link's lower item id first.
func canonical(in Relationship) Relationship {
	if !in.Directed && in.SourceID > in.TargetID {
		in.SourceID, in.TargetID = in.TargetID, in.SourceID
	}
	return in
}

func existing(ctx context.Context, q Queries, in Relationship) (Relationship, bool, error) {
	same := Filter{SourceID: in.SourceID, TargetID: in.TargetID, Directed: &in.Directed, Untyped: in.TypeID == "", Limit: 1}
	if in.TypeID != "" {
		same.TypeIDs = []string{in.TypeID}
	}
	rows, err := q.Relationships(ctx, same)
	if err != nil || len(rows) == 0 {
		return Relationship{}, false, err
	}
	return rows[0], true, nil
}

// Link links two live items. A typed link takes its direction from its
// type and respects the type's endpoint limits. Linking items that are
// already linked the same way returns the existing link and created false.
func (r *Repo) Link(ctx context.Context, in Relationship) (out Relationship, created bool, err error) {
	if in.SourceID == in.TargetID {
		return out, false, fmt.Errorf("%w: an item cannot link to itself", dberr.ErrCheckViolation)
	}
	in = canonical(in)
	err = r.write(ctx, func(q Queries) error {
		for _, id := range []string{in.SourceID, in.TargetID} {
			item, err := q.Resource(ctx, id)
			if err != nil && !dberr.IsNotFound(err) {
				return err
			}
			if err != nil || item.Kind != refs.KindItem || item.Deleted {
				return fmt.Errorf("%w: relationships link live items", dberr.ErrInvalidReference)
			}
		}
		var typ Type
		if in.TypeID != "" {
			if err := q.LockRelationshipType(ctx, in.TypeID); err != nil {
				return err
			}
			var err error
			if typ, err = q.RelationshipType(ctx, in.TypeID); dberr.IsNotFound(err) {
				return fmt.Errorf("%w: unknown relationship type", dberr.ErrInvalidReference)
			} else if err != nil {
				return err
			}
			if err := predicate(ctx, q, typ.ID); err != nil {
				return err
			}
			if typ.Directed != in.Directed {
				return fmt.Errorf("%w: direction differs from the relationship type", dberr.ErrCheckViolation)
			}
		}
		var found bool
		if out, found, err = existing(ctx, q, in); err != nil || found {
			return err
		}
		for _, limit := range []struct {
			max *int
			f   Filter
		}{{typ.MaxOutgoing, Filter{TypeIDs: []string{typ.ID}, SourceID: in.SourceID}}, {typ.MaxIncoming, Filter{TypeIDs: []string{typ.ID}, TargetID: in.TargetID}}} {
			if limit.max == nil {
				continue
			}
			n, err := q.CountRelationships(ctx, limit.f)
			if err != nil {
				return err
			}
			if n >= *limit.max {
				return fmt.Errorf("%w: the relationship type allows %d links per item", dberr.ErrCardinalityExceeded, *limit.max)
			}
		}
		out = in
		if out.ID == "" {
			out.ID = uuid.NewString()
		}
		if out.Metadata == nil {
			out.Metadata = map[string]any{}
		}
		out.Version, out.CreatedAt, created = 1, time.Now().UTC(), true
		return q.InsertRelationship(ctx, out)
	})
	return out, created, err
}

// Save writes a link's type and metadata as its next version.
func (r *Repo) Save(ctx context.Context, in Relationship, version int) (Relationship, error) {
	var out Relationship
	err := r.write(ctx, func(q Queries) error {
		cur, err := q.Relationship(ctx, in.ID)
		if err != nil {
			return err
		}
		if cur.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		cur.TypeID, cur.Metadata, cur.Version = in.TypeID, in.Metadata, version+1
		if cur.Metadata == nil {
			cur.Metadata = map[string]any{}
		}
		ok, err := q.UpdateRelationship(ctx, cur)
		if err == nil && !ok {
			err = dberr.ErrConcurrentUpdate
		}
		out = cur
		return err
	})
	return out, err
}

// Unlink deletes a link.
func (r *Repo) Unlink(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error { return q.DeleteRelationship(ctx, id) })
}
