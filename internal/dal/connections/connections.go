// Package connections owns the connections of workspaces for every database
// driver: outside endpoints, MQTT brokers and HTTP services, that apps and
// workflows reach through named channels. Drivers implement Queries; this
// package decides what those statements mean. Targets and credentials are
// sealed before they reach it, so it never holds them in the clear.
package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// Kind is what a connection reaches.
type Kind string

const (
	// KindMQTT is an MQTT broker: channels are topic filters.
	KindMQTT Kind = "mqtt"
	// KindHTTP is an HTTP service: channels are methods and path patterns.
	KindHTTP Kind = "http"
)

func (k Kind) Valid() bool { return k == KindMQTT || k == KindHTTP }

// Connection is a stored connection. Settings and Channels are the kind's
// plain configuration as JSON; ConfigCiphertext seals its target and
// credentials, and ConfigHash identifies what it seals.
type Connection struct {
	ID, WorkspaceID, Key, Name, CreatedBy, UpdatedBy string
	Kind                                             Kind
	Enabled                                          bool
	ConfigCiphertext                                 []byte
	ConfigHash                                       string
	Settings, Channels                               json.RawMessage
	Version                                          int
	CreatedAt, UpdatedAt                             time.Time
}

// Filter selects connections, ordered by name.
type Filter struct {
	WorkspaceIDs []string
	Key          string
	Kind         Kind
	// Enabled, when set, selects enabled connections only.
	Enabled bool
}

// Queries are the statements a driver runs for connections. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	FindConnection(ctx context.Context, id string) (Connection, error)
	Connections(ctx context.Context, f Filter) ([]Connection, error)
	InsertConnection(ctx context.Context, c Connection) error
	// UpdateConnection writes c's mutable state when the stored version is
	// from.
	UpdateConnection(ctx context.Context, c Connection, from int64) error
	// DeleteConnection deletes a connection at version from.
	DeleteConnection(ctx context.Context, id string, from int64) error
}

// Repo runs connection reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds connection rules to a driver. write runs fn atomically,
// inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (r *Repo) Find(ctx context.Context, id string) (Connection, error) {
	return r.read.FindConnection(ctx, id)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Connection, error) {
	return r.read.Connections(ctx, f)
}

func check(c Connection) error {
	if c.Key == "" || c.Name == "" || !c.Kind.Valid() || len(c.ConfigCiphertext) == 0 || c.ConfigHash == "" || !json.Valid(c.Settings) || !json.Valid(c.Channels) {
		return fmt.Errorf("%w: a connection needs a key, a name, a valid kind, its sealed configuration, settings and channels", dberr.ErrCheckViolation)
	}
	return nil
}

// Create records a connection in a live workspace. A missing ID is
// generated; the connection is at version 1.
func (r *Repo) Create(ctx context.Context, c Connection) (Connection, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if err := check(c); err != nil {
		return Connection{}, err
	}
	at := now()
	c.Version, c.CreatedAt, c.UpdatedAt, c.UpdatedBy = 1, at, at, c.CreatedBy
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, c.WorkspaceID); err != nil {
			return err
		}
		ws, err := q.Resource(ctx, c.WorkspaceID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || ws.Kind != refs.KindWorkspace || ws.Deleted {
			return fmt.Errorf("%w: a connection belongs to a live workspace", dberr.ErrInvalidContainment)
		}
		return q.InsertConnection(ctx, c)
	})
	if err != nil {
		return Connection{}, err
	}
	return c, nil
}

// lock returns the current connection after serializing writers of its
// workspace.
func lock(ctx context.Context, q Queries, id string) (Connection, error) {
	c, err := q.FindConnection(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	if err := q.LockWorkspace(ctx, c.WorkspaceID); err != nil {
		return Connection{}, err
	}
	return q.FindConnection(ctx, id)
}

// Replace writes next over a connection read at version, as subject, and
// returns it at its new version. Its ID, workspace, key, kind and creation
// never change.
func (r *Repo) Replace(ctx context.Context, id string, version int, subject string, next Connection) (Connection, error) {
	var out Connection
	err := r.write(ctx, func(q Queries) error {
		c, err := lock(ctx, q, id)
		if err != nil {
			return err
		}
		if c.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		next.ID, next.WorkspaceID, next.Key, next.Kind, next.CreatedBy, next.CreatedAt = c.ID, c.WorkspaceID, c.Key, c.Kind, c.CreatedBy, c.CreatedAt
		next.Version, next.UpdatedAt, next.UpdatedBy = c.Version+1, now(), subject
		if err = check(next); err != nil {
			return err
		}
		if err = q.UpdateConnection(ctx, next, int64(version)); err != nil {
			return err
		}
		out = next
		return nil
	})
	return out, err
}

// Delete deletes a connection read at version.
func (r *Repo) Delete(ctx context.Context, id string, version int) error {
	return r.write(ctx, func(q Queries) error {
		c, err := lock(ctx, q, id)
		if err != nil {
			return err
		}
		if c.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		return q.DeleteConnection(ctx, id, int64(version))
	})
}
