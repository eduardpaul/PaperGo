// Package settings owns the values features keep for the organization and
// for single resources, which lower levels inherit when they leave them
// unset. Drivers implement Queries; this package decides what those
// statements mean. A feature gives its values their meaning.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

// Key names a feature's settings.
type Key string

const (
	// KeyComments are the comment settings.
	KeyComments Key = "comments"
	// KeyTrustedHosts is the organization's list of trusted outside hosts.
	KeyTrustedHosts Key = "trusted_hosts"
)

func (k Key) Valid() bool { return k == KeyComments || k == KeyTrustedHosts }

// Setting is one feature's value at one level: the organization's when
// ResourceID is empty.
type Setting struct {
	ID, ResourceID string
	Key            Key
	Value          json.RawMessage
	Version        int64
	UpdatedBy      string
	UpdatedAt      time.Time
}

// Queries are the statements a driver runs for settings.
type Queries interface {
	// Settings returns the key's settings of resourceIDs and, with
	// organization, the organization's.
	Settings(ctx context.Context, key Key, resourceIDs []string, organization bool) ([]Setting, error)
	InsertSetting(ctx context.Context, s Setting) error
	// UpdateSetting writes s's value when the stored version is from.
	UpdateSetting(ctx context.Context, s Setting, from int64) error
}

// Repo runs setting reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds setting rules to a driver. write runs fn atomically, inside
// the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

// Find returns the key's setting of a resource, or the organization's when
// resourceID is empty; found is false when it was never set.
func (r *Repo) Find(ctx context.Context, key Key, resourceID string) (s Setting, found bool, err error) {
	var rows []Setting
	if resourceID == "" {
		rows, err = r.read.Settings(ctx, key, nil, true)
	} else {
		rows, err = r.read.Settings(ctx, key, []string{resourceID}, false)
	}
	if err != nil || len(rows) == 0 {
		return Setting{ResourceID: resourceID, Key: key}, false, err
	}
	return rows[0], true, nil
}

// Chain returns the key's settings of resourceIDs and the organization's,
// by resource ID ("" for the organization), for resolving inheritance.
func (r *Repo) Chain(ctx context.Context, key Key, resourceIDs []string) (map[string]Setting, error) {
	rows, err := r.read.Settings(ctx, key, resourceIDs, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Setting, len(rows))
	for _, s := range rows {
		out[s.ResourceID] = s
	}
	return out, nil
}

// Put stores the key's value for a resource, or the organization when
// resourceID is empty, if its current version is from: 0 for a setting
// never set.
func (r *Repo) Put(ctx context.Context, key Key, resourceID string, value json.RawMessage, from int64, by string) (Setting, error) {
	if !key.Valid() {
		return Setting{}, fmt.Errorf("%w: invalid setting key", dberr.ErrCheckViolation)
	}
	var out Setting
	err := r.write(ctx, func(q Queries) error {
		var ids []string
		if resourceID != "" {
			ids = []string{resourceID}
		}
		rows, err := q.Settings(ctx, key, ids, resourceID == "")
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		if len(rows) == 0 {
			if from != 0 {
				return dberr.ErrConcurrentUpdate
			}
			out = Setting{ID: uuid.NewString(), ResourceID: resourceID, Key: key, Value: value, Version: 1, UpdatedBy: by, UpdatedAt: now}
			return q.InsertSetting(ctx, out)
		}
		out = rows[0]
		if out.Version != from {
			return dberr.ErrConcurrentUpdate
		}
		out.Value, out.Version, out.UpdatedBy, out.UpdatedAt = value, from+1, by, now
		return q.UpdateSetting(ctx, out, from)
	})
	return out, err
}
