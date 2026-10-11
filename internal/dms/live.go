package dms

import (
	"context"
	"errors"
	"papergo/ent"
	"papergo/ent/livechange"
	"time"
)

var ErrLiveExpired = errors.New("live cursor expired; resynchronize and reconnect")

const LiveRetention = 7 * 24 * time.Hour

// LivePosition validates a reconnect position on one consistent snapshot.
func (s *Service) LivePosition(ctx context.Context, after *int64) (int64, error) {
	return read(ctx, s, func(t *Service) (int64, error) {
		var earliest, latest int64
		if err := t.scan(ctx, []any{&earliest, &latest}, "SELECT coalesce((SELECT min(id) FROM live_changes),0),coalesce((SELECT seq FROM sqlite_sequence WHERE name='live_changes'),0)"); err != nil {
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
func (s *Service) LiveChanges(ctx context.Context, after int64) ([]*ent.LiveChange, error) {
	return s.Client.LiveChange.Query().Where(livechange.IDGT(after)).Order(ent.Asc(livechange.FieldID)).Limit(500).All(ctx)
}
func (s *Service) CollectExpiredLiveChanges(ctx context.Context, now time.Time) error {
	return s.write(ctx, func(t *Service) error {
		_, err := t.Client.LiveChange.Delete().Where(livechange.CreatedAtLTE(now.UTC().Add(-LiveRetention))).Exec(ctx)
		return err
	})
}
