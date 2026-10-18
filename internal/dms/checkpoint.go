package dms

import (
	"context"
	"database/sql"
	"time"

	"papergo/ent/cachedresult"
	"papergo/ent/domainevent"
	"papergo/ent/livechange"
	"papergo/ent/operation"
	"papergo/internal/database"
)

// storageStatusRunRetention is the default RUN_RETENTION. The status count of
// dispatched events uses it so the figure has a documented window.
const storageStatusRunRetention = 30 * 24 * time.Hour

// StorageRunCount is how many workflow executions share a name and status.
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
	DatabaseBytes     int64                      `json:"database_bytes"`
	PageSize          int                        `json:"page_size"`
	PageCount         int                        `json:"page_count"`
	FreelistCount     int                        `json:"freelist_count"`
	FreelistBytes     int64                      `json:"freelist_bytes"`
	UnusedBytes       int64                      `json:"unused_bytes"`
	AccountedBytes    int64                      `json:"accounted_bytes"`
	Tables            []database.TableAllocation `json:"tables"`
	SearchStore       string                     `json:"search_store"`
	SearchBytes       int64                      `json:"search_bytes"`
	SearchDocuments   int64                      `json:"search_documents"`
	BlobBytes         int64                      `json:"blob_bytes"`
	OrphanStepResults int64                      `json:"orphan_step_results"`
	Runs              []StorageRunCount          `json:"runs"`
	Expired           StorageExpired             `json:"expired"`
	Retained          StorageRetained            `json:"retained"`
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
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM (SELECT sha256, MAX(size) AS size FROM blobs WHERE availability='available' GROUP BY sha256)`).Scan(&out.BlobBytes); err != nil {
		return StorageStatus{}, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runner_step_results WHERE run_id NOT IN (SELECT id FROM workflow_runs)`).Scan(&out.OrphanStepResults); err != nil {
		return StorageStatus{}, err
	}
	now := time.Now().UTC()
	if out.Expired.CachedResults, err = s.Client.CachedResult.Query().Where(cachedresult.ExpiresAtLTE(now)).Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_records WHERE expires_at<=? AND NOT EXISTS(SELECT 1 FROM activity_references WHERE record_id=activity_records.id) AND (lease_until IS NULL OR lease_until<=?)`, now, now).Scan(&out.Expired.ActivityRecords); err != nil {
		return StorageStatus{}, err
	}
	if out.Expired.LiveChanges, err = s.Client.LiveChange.Query().Where(livechange.CreatedAtLTE(now.Add(-LiveRetention))).Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	if out.Expired.Operations, err = s.Client.Operation.Query().Where(operation.StatusNEQ(operation.StatusPending), operation.UpdatedAtLTE(now.Add(-OperationRetention))).Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	if out.Expired.DispatchedEvents, err = s.Client.DomainEvent.Query().Where(domainevent.DispatchedAtNotNil(), domainevent.DispatchedAtLTE(now.Add(-storageStatusRunRetention))).Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	if out.Retained.AuditEvents, err = s.Client.AuditEvent.Query().Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	if out.Retained.Publications, err = s.Client.Publication.Query().Count(ctx); err != nil {
		return StorageStatus{}, err
	}
	out.Runs, err = storageRuns(ctx, s.db)
	return out, err
}

func storageRuns(ctx context.Context, db *sql.DB) ([]StorageRunCount, error) {
	var present int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='workflow_status'`).Scan(&present); err != nil {
		return nil, err
	}
	query := `SELECT event_type, live_status, COUNT(*) FROM workflow_runs GROUP BY event_type, live_status ORDER BY event_type, live_status`
	if present > 0 {
		query = `SELECT name, status, COUNT(*) FROM workflow_status GROUP BY name, status ORDER BY name, status`
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StorageRunCount{}
	for rows.Next() {
		var row StorageRunCount
		if err = rows.Scan(&row.Name, &row.Status, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
