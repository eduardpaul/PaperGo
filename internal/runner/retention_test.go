package runner

import (
	"testing"
	"time"
)

func TestSystemRunRetentionFollowsConfigAndRunCeiling(t *testing.T) {
	r := &Runner{cfg: Config{Retention: 30 * 24 * time.Hour, SystemRetention: 3 * time.Hour}}
	if got := r.systemRunRetention(); got != 3*time.Hour {
		t.Fatalf("configured system retention %s", got)
	}
	r.cfg.SystemRetention = 0
	if got := r.systemRunRetention(); got != systemRetention {
		t.Fatalf("default system retention %s", got)
	}
	r.cfg.Retention = time.Hour
	r.cfg.SystemRetention = 48 * time.Hour
	if got := r.systemRunRetention(); got != time.Hour {
		t.Fatalf("run retention ceiling %s", got)
	}
}

func TestRetentionSchedulesAreHourly(t *testing.T) {
	f := setup(t)
	var retention, system string
	if err := f.db.SQL.QueryRow(`SELECT schedule FROM workflow_schedules WHERE schedule_name=?`, purgeName).Scan(&retention); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SQL.QueryRow(`SELECT schedule FROM workflow_schedules WHERE schedule_name=?`, systemPurgeName).Scan(&system); err != nil {
		t.Fatal(err)
	}
	if retention != retentionSchedule || system != systemRetentionSchedule {
		t.Fatalf("schedules retention %s system %s", retention, system)
	}
}

func TestDispatchedEventsDeleteInPages(t *testing.T) {
	saved := purgePage
	purgePage = 2
	t.Cleanup(func() { purgePage = saved })
	f := setup(t)
	f.searchOff(t)
	for i := range 5 {
		f.item(t, "alice", "Event "+string(rune('A'+i)), nil)
	}
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM domain_events WHERE dispatched_at IS NOT NULL`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before < 3 {
		t.Fatalf("dispatched events %d", before)
	}
	if _, err := f.db.SQL.Exec(`UPDATE domain_events SET dispatched_at=? WHERE dispatched_at IS NOT NULL`, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.r.cfg.Retention = time.Hour
	if _, err := f.r.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM domain_events WHERE dispatched_at IS NOT NULL`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("dispatched events left %d of %d", left, before)
	}
}

func TestPurgeLeavesAuditAndPublications(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	item := f.item(t, "alice", "Published", nil)
	if _, err := f.dms.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	var audit, publications int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&audit); err != nil || audit == 0 {
		t.Fatal(audit, err)
	}
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM publications`).Scan(&publications); err != nil || publications == 0 {
		t.Fatal(publications, err)
	}
	f.r.cfg.Retention = time.Hour
	if _, err := f.r.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	var auditAfter, publicationsAfter int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&auditAfter); err != nil || auditAfter != audit {
		t.Fatalf("audit %d to %d (%v)", audit, auditAfter, err)
	}
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM publications`).Scan(&publicationsAfter); err != nil || publicationsAfter != publications {
		t.Fatalf("publications %d to %d (%v)", publications, publicationsAfter, err)
	}
}
