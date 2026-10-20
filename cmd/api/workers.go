package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"papergo/internal/config"
	"papergo/internal/dms"
	"papergo/internal/documenttext"
	"papergo/internal/scriptrun"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

// workers returns the runtime of the operations this executable runs: one
// worker per kind of work, so long OCR never holds up index builds or bulk
// edits, and the maintenance that keeps retention and cleanup going even
// with empty queues.
func workers(service *dms.Service, store storage.Store, log *slog.Logger) *worker.Runtime {
	rt := worker.New(service, log)
	rt.Add(worker.Definition{Name: "maintenance", Kinds: []string{dms.OperationKindFieldIndex, dms.OperationKindBulkUpdate, dms.OperationKindSearchReindex, dms.OperationKindExport, dms.OperationKindImport}})
	rt.Add(worker.Definition{Name: "integrations", Kinds: []string{dms.OperationKindIntegrationRefresh}, Concurrency: 2})
	rt.Add(worker.Definition{Name: "documents", Kinds: []string{documenttext.Kind}})
	for _, m := range []worker.Maintenance{
		{Name: "operation retention", Every: time.Hour, Run: service.CollectExpiredOperations},
		{Name: "operation deadlines", Every: time.Minute, Run: service.ExpireOperations},
		{Name: "delta retention", Every: time.Hour, Run: service.CollectExpiredDeltas},
		{Name: "live change retention", Every: time.Hour, Run: service.CollectExpiredLiveChanges},
		{Name: "purged blobs", Every: time.Minute, Run: func(ctx context.Context, _ time.Time) error { return service.CleanupPurgedBlobs(ctx, store) }},
		{Name: "unreferenced objects", Every: time.Minute, Run: func(ctx context.Context, now time.Time) error { return service.CollectObjects(ctx, store, now) }},
		{Name: "reclaimed files", Every: time.Minute, Run: func(ctx context.Context, now time.Time) error { return service.SweepReclaims(ctx, store, now) }},
		{Name: "script waits", Every: time.Minute, Run: func(ctx context.Context, _ time.Time) error {
			_, err := scriptrun.ReconcileWaits(ctx, service)
			return err
		}},
	} {
		rt.Maintain(m)
	}
	return rt
}

// configure applies config.yaml's worker settings to the runtime's
// workers. A setting for a worker the executable does not run is an error.
func configure(rt *worker.Runtime, settings map[string]config.Worker) error {
	for name, s := range settings {
		d := rt.Worker(name)
		if d == nil {
			return fmt.Errorf("config.yaml workers: no worker is named %s", name)
		}
		if s.Concurrency != nil {
			d.Concurrency = *s.Concurrency
		}
		if a := s.Admission; a != nil {
			d.Admission = worker.Admission{Policy: worker.Policy(a.Policy), Threshold: a.Threshold, Batch: a.Batch}
			if a.MaxWait != "" {
				wait, err := time.ParseDuration(a.MaxWait)
				if err != nil {
					return fmt.Errorf("config.yaml workers %s max_wait: %w", name, err)
				}
				d.Admission.MaxWait = wait
			}
			if f := a.Flush; f != nil {
				flush, err := worker.ParseSchedule(f.Cron, f.TimeZone, worker.Missed(f.Missed))
				if err != nil {
					return fmt.Errorf("config.yaml workers %s flush: %w", name, err)
				}
				d.Admission.Flush = flush
			}
		}
		if w := s.Window; w != nil {
			window, err := worker.ParseWindow(w.TimeZone, w.From, w.To)
			if err != nil {
				return fmt.Errorf("config.yaml workers %s window: %w", name, err)
			}
			d.Window = window
		}
	}
	return rt.Validate()
}
