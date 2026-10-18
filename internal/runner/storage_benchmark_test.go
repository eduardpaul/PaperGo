package runner

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

// BenchmarkStorageEfficiency uses fresh databases and real durable workflows.
// scripts/compare-storage.py runs this identical source against both revisions.
// Use -benchtime=1x: each sample creates 100 items and 180 maintenance executions.
func BenchmarkStorageEfficiency(b *testing.B) {
	for _, size := range []int{0, 8192} {
		b.Run(fmt.Sprintf("definition-%d", size), func(b *testing.B) {
			if b.N != 1 {
				b.Fatal("run this fixed workload with -benchtime=1x")
			}
			b.StopTimer()
			f := setup(b)
			f.searchOff(b)
			w := f.workflow(b, "Measured workflow", `{
				"triggers":[{"type":"item.created","collection_id":"$LIST"}],
				"variables":{"bulk":"`+strings.Repeat("v", size)+`"},
				"flow":{"start":"clear","nodes":{
					"clear":{"activity":"set_variable","inputs":{"name":"bulk","value":""},"next":{"done":"read"}},
					"read":{"activity":"item.get","next":{"done":"stop"}},
					"stop":{"activity":"end"}}}}`)
			var initialPages, pageSize int64
			if err := f.db.SQL.QueryRow(`PRAGMA page_count`).Scan(&initialPages); err != nil {
				b.Fatal(err)
			}
			if err := f.db.SQL.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
				b.Fatal(err)
			}
			const items = 100
			b.StartTimer()
			started := time.Now()
			for i := range items {
				f.item(b, "alice", fmt.Sprintf("Measured item %03d", i), map[string]any{"note": "Same metadata in both revisions"})
			}
			runs := f.runs(b, w.ID, items)
			elapsed := time.Since(started)
			b.StopTimer()
			for _, run := range runs {
				if run.Status != "completed" {
					b.Fatalf("run %s: %s %s", run.ID, run.Status, run.Error)
				}
			}
			b.ReportMetric(float64(elapsed.Nanoseconds())/items, "workload-ns/item")
			started = time.Now()
			for _, run := range runs {
				details, err := f.r.Run(ctx, "alice", run.ID)
				if err != nil || len(details.Steps) != 5 {
					b.Fatalf("inspect run: %v (%d steps)", err, len(details.Steps))
				}
			}
			b.ReportMetric(float64(time.Since(started).Nanoseconds())/items, "details-ns/run")
			for _, probe := range []struct{ query, metric string }{
				{`SELECT COALESCE(SUM(length(output)),0) FROM runner_step_results`, "marker-bytes"},
				{`SELECT COALESCE(SUM(length(output)),0) FROM operation_outputs`, "dbos-step-bytes"},
				{`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name IN ('runner_step_results','sqlite_autoindex_runner_step_results_1','operation_outputs','sqlite_autoindex_operation_outputs_1')`, "step-allocation-bytes"},
			} {
				var value int64
				if err := f.db.SQL.QueryRow(probe.query).Scan(&value); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(value), probe.metric)
			}
			var busy, log, checkpointed int
			if err := f.db.SQL.QueryRow(`PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &log, &checkpointed); err != nil || busy != 0 || log != checkpointed {
				b.Fatalf("checkpoint: %d/%d/%d %v", busy, log, checkpointed, err)
			}
			for _, file := range []struct{ suffix, metric string }{{"", "database-bytes"}, {"-wal", "journal-bytes"}} {
				info, err := os.Stat(f.path + file.suffix)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(info.Size()), file.metric)
				if file.suffix == "" {
					b.ReportMetric(float64(info.Size()-initialPages*pageSize), "database-growth-bytes")
				}
			}

			// Exercise real schedule executions, then age only their completion
			// timestamps to measure retention without a three-hour wall-clock wait.
			const maintenance = 180
			for i := range maintenance {
				handle, err := dbos.RunWorkflow(f.r.ctx, f.r.tick, dbos.ScheduledWorkflowInput{ScheduledTime: time.Unix(int64(i)*60, 0)}, dbos.WithWorkflowID(fmt.Sprintf("measured-tick-%d", i)))
				if err != nil {
					b.Fatal(err)
				}
				if _, err = handle.GetResult(); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := f.db.SQL.Exec(`UPDATE workflow_status SET completed_at=? WHERE name=?`, time.Now().Add(-3*time.Hour).UnixMilli(), tickName); err != nil {
				b.Fatal(err)
			}
			started = time.Now()
			if _, err := f.r.Purge(ctx); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(time.Since(started).Nanoseconds()), "purge-ns")
			var remaining, applications int
			if err := f.db.SQL.QueryRow(`SELECT COUNT(*) FROM workflow_status WHERE name=?`, tickName).Scan(&remaining); err != nil {
				b.Fatal(err)
			}
			if err := f.db.SQL.QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE workflow_id=?`, w.ID).Scan(&applications); err != nil || applications != items {
				b.Fatalf("retained application runs: %d %v", applications, err)
			}
			b.ReportMetric(float64(remaining), "maintenance-remaining")
		})
	}
}
