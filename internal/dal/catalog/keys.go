package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// BusinessKey claims a unique field's value for an item or folder of a
// collection: no other item of the collection can hold the same value.
type BusinessKey struct {
	ID, CollectionID, ItemID, FieldKey, Value string
	CreatedAt                                 time.Time
}

// KeyFilter selects business keys. Zero fields do not filter.
type KeyFilter struct {
	CollectionID, ItemID, FieldKey string
}

// KeyQueries are the statements a driver runs for business keys.
type KeyQueries interface {
	BusinessKeys(ctx context.Context, f KeyFilter) ([]BusinessKey, error)
	InsertBusinessKeys(ctx context.Context, keys []BusinessKey) error
	DeleteBusinessKeys(ctx context.Context, f KeyFilter) error
}

// Keys lists business keys.
func (r *Repo) Keys(ctx context.Context, f KeyFilter) ([]BusinessKey, error) {
	return r.read.BusinessKeys(ctx, f)
}

// ClaimKeys claims values for items of their collections. A value another
// item of the collection holds is dberr.ErrUniqueViolation.
func (r *Repo) ClaimKeys(ctx context.Context, keys []BusinessKey) error {
	if len(keys) == 0 {
		return nil
	}
	at := time.Now().UTC()
	for i := range keys {
		if keys[i].ID == "" {
			keys[i].ID = uuid.NewString()
		}
		keys[i].CreatedAt = at
	}
	return r.write(ctx, func(q Queries) error { return q.InsertBusinessKeys(ctx, keys) })
}

// ClaimKey claims a value for a live item or folder of its collection.
func (r *Repo) ClaimKey(ctx context.Context, k BusinessKey) (BusinessKey, error) {
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, k.ItemID); err != nil {
			return err
		}
		item, err := q.Resource(ctx, k.ItemID)
		if dberr.IsNotFound(err) {
			return fmt.Errorf("%w: business key owner not found", dberr.ErrInvalidReference)
		}
		if err != nil {
			return err
		}
		if item.Kind != refs.KindItem && item.Kind != refs.KindFolder || item.ContainerID != k.CollectionID || item.Deleted {
			return fmt.Errorf("%w: invalid business key owner", dberr.ErrInvalidContainment)
		}
		if k.ID == "" {
			k.ID = uuid.NewString()
		}
		k.CreatedAt = time.Now().UTC()
		return q.InsertBusinessKeys(ctx, []BusinessKey{k})
	})
	return k, err
}

// ReleaseKeys releases the business keys f selects.
func (r *Repo) ReleaseKeys(ctx context.Context, f KeyFilter) error {
	if f == (KeyFilter{}) {
		return fmt.Errorf("%w: release keys of a collection, item or field", dberr.ErrCheckViolation)
	}
	return r.read.DeleteBusinessKeys(ctx, f)
}
