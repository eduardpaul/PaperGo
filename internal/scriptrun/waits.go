package scriptrun

import (
	"context"
	"log/slog"

	"papergo/internal/dms"
)

// ReleaseWaits returns the hook that completes the waits of script runs
// when the operations they wait for end; wire it to
// dms.Service.OnOperationsFinished. A wait released late, because the
// process stopped before the hook ran, is completed by ReconcileWaits.
func ReleaseWaits(s *dms.Service, log *slog.Logger) func(operationIDs []string) {
	return func(operationIDs []string) {
		err := s.Write(context.Background(), func(t *dms.Service) error {
			_, err := t.DAL.Runs().ReleaseWaits(context.Background(), operationIDs)
			return err
		})
		if err != nil {
			log.Error("release script waits", "operations", operationIDs, "error", err)
		}
	}
}

// ReconcileWaits completes every pending wait whose operations have all
// ended, and reports how many it completed. It covers waits the hook missed:
// an operation that ended while the process stopped, or one that ended while
// the wait was being recorded.
func ReconcileWaits(ctx context.Context, s *dms.Service) (int, error) {
	var n int
	err := s.Write(ctx, func(t *dms.Service) error {
		var err error
		n, err = t.DAL.Runs().ReleaseWaits(ctx, nil)
		return err
	})
	return n, err
}
