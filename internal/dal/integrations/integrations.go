// Package integrations owns list integrations shared by every database
// driver: the external sources a list mirrors, the items each source
// manages and the provider data frozen with item revisions. Providers and
// their network access live outside the DAL. Drivers implement Queries; this
// package decides what those statements mean.
package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// Integration is an external source mirrored into a list. Its configuration
// is encrypted by the caller; a worker holds its lease while refreshing.
type Integration struct {
	ID, ContainerID, Kind, Name, Generation, ConfigHash, ConfiguredBy string
	CredentialID, LeaseToken, Error                                   string
	ConfigCiphertext                                                  []byte
	CredentialScopes                                                  []string
	State                                                             json.RawMessage
	Paused                                                            bool
	Version, ItemCount                                                int
	LastAttemptAt, LastSuccessAt, LeaseUntil                          *time.Time
	CreatedAt, UpdatedAt                                              time.Time
}

// Leased reports whether a worker holds the integration's lease at a time.
func (i Integration) Leased(at time.Time) bool {
	return i.LeaseUntil != nil && i.LeaseUntil.After(at)
}

// Item binds an item to the external entry it mirrors. A detached item keeps
// its binding without a source; an empty IntegrationID is stored as NULL.
type Item struct {
	ID, ItemID, IntegrationID, ExternalID, ProviderKind string
	ManagedFields                                       []string
	CreatedAt                                           time.Time
}

// RevisionData is opaque provider data frozen with an item revision.
type RevisionData struct {
	ID, RevisionID, ProviderKind string
	Data                         json.RawMessage
	CreatedAt                    time.Time
}

// Filter selects integrations by creation. Unpaused selects the active ones.
type Filter struct {
	ContainerID string
	Unpaused    bool
}

// Queries are the statements a driver runs for integrations. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	FindIntegration(ctx context.Context, id string) (Integration, error)
	Integrations(ctx context.Context, f Filter) ([]Integration, error)
	CountIntegrations(ctx context.Context, f Filter) (int64, error)
	InsertIntegration(ctx context.Context, i Integration) error
	// UpdateIntegration writes every mutable setting of i.
	UpdateIntegration(ctx context.Context, i Integration) error
	DeleteIntegration(ctx context.Context, id string) error
	// ReleaseIntegrationLease clears a lease the token still holds.
	ReleaseIntegrationLease(ctx context.Context, id, token string) error
	IntegrationItem(ctx context.Context, itemID string) (Item, error)
	IntegrationItems(ctx context.Context, integrationID string) ([]Item, error)
	InsertIntegrationItem(ctx context.Context, i Item) error
	// DetachIntegrationItems removes the source of an integration's items.
	DetachIntegrationItems(ctx context.Context, integrationID string) error
	RevisionData(ctx context.Context, revisionID string) (RevisionData, error)
	InsertRevisionData(ctx context.Context, d RevisionData) error
}

// Repo runs integration reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds integration rules to a driver. write runs fn atomically,
// inside the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (r *Repo) Find(ctx context.Context, id string) (Integration, error) {
	return r.read.FindIntegration(ctx, id)
}

// List returns integrations in creation order.
func (r *Repo) List(ctx context.Context, f Filter) ([]Integration, error) {
	return r.read.Integrations(ctx, f)
}

func (r *Repo) Count(ctx context.Context, f Filter) (int64, error) {
	return r.read.CountIntegrations(ctx, f)
}

// Exists reports whether any integration matches f.
func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	n, err := r.read.CountIntegrations(ctx, f)
	return n > 0, err
}

// Create records an integration of a live list from its kind, name,
// generation, configuration and configuring credential.
func (r *Repo) Create(ctx context.Context, in Integration) (Integration, error) {
	at := now()
	i := Integration{
		ID: uuid.NewString(), ContainerID: in.ContainerID, Kind: in.Kind, Name: in.Name, Generation: in.Generation, ConfigHash: in.ConfigHash,
		ConfiguredBy: in.ConfiguredBy, CredentialID: in.CredentialID, ConfigCiphertext: in.ConfigCiphertext, CredentialScopes: in.CredentialScopes,
		State: json.RawMessage(`{}`), Version: 1, CreatedAt: at, UpdatedAt: at,
	}
	if i.CredentialScopes == nil {
		i.CredentialScopes = []string{}
	}
	err := r.write(ctx, func(q Queries) error {
		list, err := q.Resource(ctx, in.ContainerID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || list.Kind != refs.KindList || list.Deleted {
			return fmt.Errorf("%w: integrations require a live list", dberr.ErrInvalidContainment)
		}
		return q.InsertIntegration(ctx, i)
	})
	if err != nil {
		return Integration{}, err
	}
	return i, nil
}

// Save writes an integration's settings, configuration, refresh state and
// lease as of now.
func (r *Repo) Save(ctx context.Context, i *Integration) error {
	i.UpdatedAt = now()
	if i.CredentialScopes == nil {
		i.CredentialScopes = []string{}
	}
	return r.read.UpdateIntegration(ctx, *i)
}

// Remove deletes an integration; its items stay as ordinary items.
func (r *Repo) Remove(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.DetachIntegrationItems(ctx, id); err != nil {
			return err
		}
		return q.DeleteIntegration(ctx, id)
	})
}

// Detach turns an integration's items into ordinary items, as when its source
// is reset.
func (r *Repo) Detach(ctx context.Context, id string) error {
	return r.read.DetachIntegrationItems(ctx, id)
}

// ReleaseLease clears a lease the token still holds.
func (r *Repo) ReleaseLease(ctx context.Context, id, token string) error {
	return r.read.ReleaseIntegrationLease(ctx, id, token)
}

// Item returns the binding of an item, or dberr.ErrNotFound.
func (r *Repo) Item(ctx context.Context, itemID string) (Item, error) {
	return r.read.IntegrationItem(ctx, itemID)
}

// Items returns an integration's items, or the detached items when the ID is
// empty.
func (r *Repo) Items(ctx context.Context, integrationID string) ([]Item, error) {
	return r.read.IntegrationItems(ctx, integrationID)
}

// AddItem binds an item of an integration's list to an external entry.
func (r *Repo) AddItem(ctx context.Context, in Item) (Item, error) {
	i := Item{ID: uuid.NewString(), ItemID: in.ItemID, IntegrationID: in.IntegrationID, ExternalID: in.ExternalID, ProviderKind: in.ProviderKind, ManagedFields: in.ManagedFields, CreatedAt: now()}
	if i.ManagedFields == nil {
		i.ManagedFields = []string{}
	}
	err := r.write(ctx, func(q Queries) error {
		item, err := q.Resource(ctx, in.ItemID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || item.Kind != refs.KindItem {
			return fmt.Errorf("%w: integration item requires an item resource", dberr.ErrInvalidContainment)
		}
		if in.IntegrationID != "" {
			source, err := q.FindIntegration(ctx, in.IntegrationID)
			if err != nil && !dberr.IsNotFound(err) {
				return err
			}
			if err != nil || source.ContainerID != item.ContainerID || source.Kind != in.ProviderKind {
				return fmt.Errorf("%w: integration item must belong to its integration list", dberr.ErrInvalidContainment)
			}
		}
		return q.InsertIntegrationItem(ctx, i)
	})
	if err != nil {
		return Item{}, err
	}
	return i, nil
}

// RevisionData returns the provider data of a revision, or nil when it has
// none.
func (r *Repo) RevisionData(ctx context.Context, revisionID string) (*RevisionData, error) {
	d, err := r.read.RevisionData(ctx, revisionID)
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// SaveRevisionData freezes provider data with a revision.
func (r *Repo) SaveRevisionData(ctx context.Context, revisionID, providerKind string, data json.RawMessage) error {
	return r.read.InsertRevisionData(ctx, RevisionData{ID: uuid.NewString(), RevisionID: revisionID, ProviderKind: providerKind, Data: data, CreatedAt: now()})
}
