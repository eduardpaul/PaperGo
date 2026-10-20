package dms

import (
	"context"
	"errors"
	"time"

	"papergo/internal/dal/live"
)

var ErrLiveExpired = errors.New("live cursor expired; resynchronize and reconnect")

const LiveRetention = 7 * 24 * time.Hour

// LivePosition validates a reconnect position on one consistent snapshot.
func (s *Service) LivePosition(ctx context.Context, after *int64) (int64, error) {
	return read(ctx, s, func(t *Service) (int64, error) {
		earliest, latest, err := t.DAL.LiveChanges().Bounds(ctx)
		if err != nil {
			return 0, err
		}
		if after == nil {
			return latest, nil
		}
		if *after < 0 || *after > latest {
			return 0, invalid("invalid live cursor")
		}
		if earliest > 0 && *after < earliest-1 || earliest == 0 && *after < latest {
			return 0, ErrLiveExpired
		}
		return *after, nil
	})
}
func (s *Service) LiveChanges(ctx context.Context, after int64) ([]live.Change, error) {
	return s.DAL.LiveChanges().After(ctx, after)
}
func (s *Service) CollectExpiredLiveChanges(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-LiveRetention)
	for {
		var n int64
		err := s.write(ctx, func(t *Service) error {
			var err error
			n, err = t.DAL.LiveChanges().DeleteExpired(ctx, cutoff, livePurgePage)
			return err
		})
		if err != nil || n < livePurgePage {
			return err
		}
	}
}

const livePurgePage = 200
