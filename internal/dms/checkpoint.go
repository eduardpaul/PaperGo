package dms

import (
	"context"
	"time"

	"papergo/internal/dal/activitystore"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/database"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resultcache"
)

// storageStatusRunRetention is the default RUN_RETENTION. The status count of
// dispatched events uses it so the figure has a documented window.
const storageStatusRunRetention = 30 * 24 * time.Hour

// StorageRunCount is how many workflow runs have a status. Name is the
// operation kind runs are executed as.
type StorageRunCount struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// StorageExpired counts operational rows a cleanup pass can collect.
// Dispatched events use the default 30-day run retention. Live changes use
// the seven-day live retention. Operations use the 30-day operation retention.
// Caches and activity records use their own expires_at.
type StorageExpired struct {
	CachedResults    int `json:"cached_results"`
	ActivityRecords  int `json:"activity_records"`
	LiveChanges      int `json:"live_changes"`
	Operations       int `json:"operations"`
	DispatchedEvents int `json:"dispatched_events"`
}

// StorageRetained counts records cleanup leaves in place.
type StorageRetained struct {
	AuditEvents  int `json:"audit_events"`
	Publications int `json:"publications"`
}

// StorageStatus is the operator view of database allocation, the journal,
// search and blob bytes, runs, and cleanup. It carries counts and schema
// object names, not document text, file bytes, or credentials.
type StorageStatus struct {
	database.Checkpoint
	DatabaseBytes   int64                      `json:"database_bytes"`
	PageSize        int                        `json:"page_size"`
	PageCount       int                        `json:"page_count"`
	FreelistCount   int                        `json:"freelist_count"`
	FreelistBytes   int64                      `json:"freelist_bytes"`
	UnusedBytes     int64                      `json:"unused_bytes"`
	AccountedBytes  int64                      `json:"accounted_bytes"`
	Tables          []database.TableAllocation `json:"tables"`
	SearchStore     string                     `json:"search_store"`
	SearchBytes     int64                      `json:"search_bytes"`
	SearchDocuments int64                      `json:"search_documents"`
	BlobBytes       int64                      `json:"blob_bytes"`
	Runs            []StorageRunCount          `json:"runs"`
	Expired         StorageExpired             `json:"expired"`
	Retained        StorageRetained            `json:"retained"`
}

// StorageStatus reports allocation and a passive checkpoint for an organization
// manager. The checkpoint may copy committed frames into the database file.
// It leaves the journal file in place.
func (s *Service) StorageStatus(ctx context.Context, subject string) (StorageStatus, error) {
	if err := s.RequireScope(ctx, subject, "organization.manage"); err != nil {
		return StorageStatus{}, err
	}
	checkpoint, err := database.ObserveCheckpoint(ctx, s.db)
	if err != nil {
		return StorageStatus{}, err
	}
	alloc, err := database.ReadAllocation(ctx, s.db)
	if err != nil {
		return StorageStatus{}, err
	}
	out := StorageStatus{
		Checkpoint:    checkpoint,
		DatabaseBytes: alloc.DatabaseBytes, PageSize: alloc.PageSize, PageCount: alloc.PageCount,
		FreelistCount: alloc.FreelistCount, FreelistBytes: alloc.FreelistBytes, UnusedBytes: alloc.UnusedBytes,
		AccountedBytes: alloc.AccountedBytes, Tables: alloc.Tables,
		Runs: []StorageRunCount{},
	}
	if out.Tables == nil {
		out.Tables = []database.TableAllocation{}
	}
	if s.search != nil {
		stats, err := s.search.Stats(ctx)
		if err != nil {
			return StorageStatus{}, err
		}
		out.SearchStore = s.search.Name()
		out.SearchBytes = stats.Bytes
		out.SearchDocuments = stats.Documents
	}
	if out.BlobBytes, err = s.DAL.Blobs().StoredBytes(ctx); err != nil {
		return StorageStatus{}, err
	}
	now := time.Now().UTC()
	if out.Expired.CachedResults, err = count(resultcache.New(s.db).CountExpired(ctx, now)); err != nil {
		return StorageStatus{}, err
	}
	if out.Expired.ActivityRecords, err = activitystore.New(s.db).CountExpired(ctx, now); err != nil {
		return StorageStatus{}, err
	}
	if out.Expired.LiveChanges, err = count(s.DAL.LiveChanges().CountExpired(ctx, now.Add(-LiveRetention))); err != nil {
		return StorageStatus{}, err
	}
	expired := now.Add(-OperationRetention)
	if out.Expired.Operations, err = count(s.DAL.Operations().Count(ctx, operations.Filter{Finished: true, UpdatedThrough: &expired})); err != nil {
		return StorageStatus{}, err
	}
	dispatched := now.Add(-storageStatusRunRetention)
	if out.Expired.DispatchedEvents, err = count(s.DAL.Events().Count(ctx, eventstore.Filter{DispatchedThrough: &dispatched})); err != nil {
		return StorageStatus{}, err
	}
	if out.Retained.AuditEvents, err = count(s.DAL.Audit().Count(ctx, auditstore.Filter{})); err != nil {
		return StorageStatus{}, err
	}
	if out.Retained.Publications, err = s.DAL.Revisions().CountPublications(ctx); err != nil {
		return StorageStatus{}, err
	}
	out.Runs, err = storageRuns(ctx, s)
	return out, err
}

func storageRuns(ctx context.Context, s *Service) ([]StorageRunCount, error) {
	out := []StorageRunCount{}
	for _, status := range []operations.Status{operations.StatusPending, operations.StatusSucceeded, operations.StatusFailed, operations.StatusCancelled} {
		n, err := s.DAL.Operations().Count(ctx, operations.Filter{Kinds: []string{OperationKindRun}, Statuses: []operations.Status{status}})
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out = append(out, StorageRunCount{Name: OperationKindRun, Status: string(status), Count: n})
		}
	}
	return out, nil
}

// count converts a repository count to a status counter.
func count(n int64, err error) (int, error) { return int(n), err }
