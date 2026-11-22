package dms

import (
	"context"
	"time"
)

// auditPurgePage is how many audit events a retention pass deletes at a time.
const auditPurgePage = 500

// CollectExpiredAudit deletes the audit events older than the run retention
// (RUN_RETENTION), a page per transaction, so a large backlog releases the
// writer between pages. It is a maintenance task.
func (s *Service) CollectExpiredAudit(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-s.RunRetention())
	for {
		var n int64
		err := s.write(ctx, func(t *Service) error {
			var err error
			n, err = t.DAL.Audit().DeleteExpired(ctx, cutoff, auditPurgePage)
			return err
		})
		if err != nil || n < auditPurgePage {
			return err
		}
	}
}
