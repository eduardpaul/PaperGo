package scriptrun

import (
	"context"
	"log/slog"
	"time"

	"papergo/internal/dms"
)

// ReleaseWaits returns the hook that completes the waits of runs when the
// operations they wait for end, and lets those runs continue; wire it to
// dms.Service.OnOperationsFinished. A wait released late, because the
// process stopped before the hook ran, is completed by ReconcileWaits.
func ReleaseWaits(s *dms.Service, log *slog.Logger) func(operationIDs []string) {
	return func(operationIDs []string) {
		err := s.Write(context.Background(), func(t *dms.Service) error {
			return release(context.Background(), t, operationIDs)
		})
		if err != nil {
			log.Error("release script waits", "operations", operationIDs, "error", err)
		}
	}
}

// ReconcileWaits completes every pending wait whose operations have all
// ended and lets their runs continue. It covers waits the hook missed:
// an operation that ended while the process stopped, or one that ended while
// the wait was being recorded.
func ReconcileWaits(ctx context.Context, s *dms.Service) error {
	return s.Write(ctx, func(t *dms.Service) error { return release(ctx, t, nil) })
}

// release completes the waits for operationIDs, or every wait whose
// operations ended when there are none, and unparks their runs.
func release(ctx context.Context, t *dms.Service, operationIDs []string) error {
	runs, err := t.DAL.Runs().ReleaseWaits(ctx, operationIDs)
	if err != nil || len(runs) == 0 {
		return err
	}
	return t.UnparkOperations(ctx, waitExecution, runs, time.Now())
}
