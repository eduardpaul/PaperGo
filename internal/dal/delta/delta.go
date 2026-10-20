// Package delta owns the positions clients synchronize collections from,
// shared by every database driver. A view numbers a subject's snapshots of a
// collection; states record the fingerprint each item had over a range of
// snapshots; a token names one snapshot and caches its page of changes.
// Drivers implement Queries; this package decides what those statements mean.
package delta

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// View is a subject's numbered snapshots of a collection.
type View struct {
	ID, Subject, ContainerID string
	Sequence                 int64
	CreatedAt                time.Time
}

// Token names a snapshot of a view. Changes holds the page of entries the
// token returned until PageExpiresAt.
type Token struct {
	ID, ViewID               string
	Sequence                 int64
	Changes                  json.RawMessage
	PageExpiresAt, ExpiresAt time.Time
	CreatedAt                time.Time
}

// State is the fingerprint an item had from one snapshot until another.
type State struct {
	ID, ViewID, ItemID, Fingerprint string
	FromSequence                    int64
	CreatedAt                       time.Time
}

// Queries are the statements a driver runs for delta synchronization. Each
// method executes on the transaction or connection the driver was bound to.
type Queries interface {
	SyncView(ctx context.Context, subject, containerID string) (View, error)
	SyncViewByID(ctx context.Context, id string) (View, error)
	InsertSyncView(ctx context.Context, v View) error
	SetSyncViewSequence(ctx context.Context, id string, sequence int64) error
	// SyncStates returns the fingerprint of each item at a snapshot.
	SyncStates(ctx context.Context, viewID string, sequence int64) (map[string]string, error)
	// EndSyncState ends an item's open state before a snapshot.
	EndSyncState(ctx context.Context, viewID, itemID string, sequence int64) error
	InsertSyncState(ctx context.Context, s State) error
	CountSyncStates(ctx context.Context) (int64, error)
	SyncToken(ctx context.Context, id string) (Token, error)
	InsertSyncToken(ctx context.Context, t Token) error
	SetSyncTokenExpiry(ctx context.Context, id string, at time.Time) error
	// DeleteExpiredSyncTokens deletes tokens expired at or before now and
	// clears the cached pages expired by then.
	DeleteExpiredSyncTokens(ctx context.Context, now time.Time) error
	// DeleteUnreachableSyncStates deletes the ended states no token can
	// name, then the views without tokens and their states.
	DeleteUnreachableSyncStates(ctx context.Context) error
}

// Repo runs synchronization reads and single statements on its connection
// and multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds synchronization rules to a driver. write runs fn
// atomically, inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// View returns a subject's view of a collection.
func (r *Repo) View(ctx context.Context, subject, containerID string) (View, error) {
	return r.read.SyncView(ctx, subject, containerID)
}

func (r *Repo) ViewByID(ctx context.Context, id string) (View, error) {
	return r.read.SyncViewByID(ctx, id)
}

// CreateView starts a subject's view of a collection with no snapshot.
func (r *Repo) CreateView(ctx context.Context, subject, containerID string) (View, error) {
	v := View{ID: uuid.NewString(), Subject: subject, ContainerID: containerID, CreatedAt: now()}
	return v, r.read.InsertSyncView(ctx, v)
}

// States returns the fingerprint of each item at a snapshot of a view.
func (r *Repo) States(ctx context.Context, viewID string, sequence int64) (map[string]string, error) {
	return r.read.SyncStates(ctx, viewID, sequence)
}

// Advance records the next snapshot of a view: the ended items no longer
// have their previous fingerprint, and the started items have a new one.
func (r *Repo) Advance(ctx context.Context, v View, ended []string, started map[string]string) (View, error) {
	next := v.Sequence + 1
	at := now()
	err := r.write(ctx, func(q Queries) error {
		for _, id := range ended {
			if err := q.EndSyncState(ctx, v.ID, id, next); err != nil {
				return err
			}
		}
		for id, fingerprint := range started {
			if err := q.InsertSyncState(ctx, State{ID: uuid.NewString(), ViewID: v.ID, ItemID: id, Fingerprint: fingerprint, FromSequence: next, CreatedAt: at}); err != nil {
				return err
			}
		}
		return q.SetSyncViewSequence(ctx, v.ID, next)
	})
	v.Sequence = next
	return v, err
}

// CountStates counts the stored item states of every view.
func (r *Repo) CountStates(ctx context.Context) (int64, error) {
	return r.read.CountSyncStates(ctx)
}

func (r *Repo) Token(ctx context.Context, id string) (Token, error) {
	return r.read.SyncToken(ctx, id)
}

// CreateToken names the current snapshot of a view and caches its changes.
func (r *Repo) CreateToken(ctx context.Context, v View, changes json.RawMessage, pageTTL, ttl time.Duration) (Token, error) {
	at := now()
	t := Token{ID: uuid.NewString(), ViewID: v.ID, Sequence: v.Sequence, Changes: changes, PageExpiresAt: at.Add(pageTTL), ExpiresAt: at.Add(ttl), CreatedAt: at}
	return t, r.read.InsertSyncToken(ctx, t)
}

// Renew moves a token's expiry.
func (r *Repo) Renew(ctx context.Context, id string, expiresAt time.Time) error {
	return r.read.SetSyncTokenExpiry(ctx, id, expiresAt.UTC())
}

// CollectExpired deletes expired tokens, clears expired pages and removes the
// states and views no remaining token can name.
func (r *Repo) CollectExpired(ctx context.Context, at time.Time) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.DeleteExpiredSyncTokens(ctx, at.UTC()); err != nil {
			return err
		}
		return q.DeleteUnreachableSyncStates(ctx)
	})
}
