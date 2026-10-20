package runner

import (
	"fmt"
	"os"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/database"
	"slices"
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
			if daltest.PostgresDSN() != "" {
				b.Skip("measures the SQLite file")
			}
			f := setup(b)
			f.searchOff(b)
			w := f.workflow(b, "Measured workflow", `{
				"triggers":[{"type":"item.created","collection_id":"$LIST"}],
				"variables":{"bulk":"`+strings.Repeat("v", size)+`"},
				"flow":{"start":"clear","nodes":{
					"clear":{"activity":"set_variable","inputs":{"name":"bulk","value":""},"next":{"done":"read"}},
					"read":{"activity":"item.get","next":{"done":"stop"}},
					"stop":{"activity":"end"}}}}`)
			initial, err := database.ReadAllocation(ctx, f.db.SQL)
			if err != nil {
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
			for _, probe := range []struct{ table, metric string }{{"runner_step_results", "marker-bytes"}, {"operation_outputs", "dbos-step-bytes"}} {
				var value int
				for _, output := range daltest.Values(b, f.db.DAL, probe.table, "output", nil) {
					value += len(fmt.Sprint(output))
				}
				b.ReportMetric(float64(value), probe.metric)
			}
			allocation, err := database.ReadAllocation(ctx, f.db.SQL)
			if err != nil {
				b.Fatal(err)
			}
			var steps int64
			for _, table := range allocation.Tables {
				if slices.Contains([]string{"runner_step_results", "sqlite_autoindex_runner_step_results_1", "operation_outputs", "sqlite_autoindex_operation_outputs_1"}, table.Name) {
					steps += table.Bytes
				}
			}
			b.ReportMetric(float64(steps), "step-allocation-bytes")
			if checkpoint, err := database.ObserveCheckpoint(ctx, f.db.SQL); err != nil || checkpoint.Incomplete {
				b.Fatalf("checkpoint: %+v %v", checkpoint, err)
			}
			for _, file := range []struct{ suffix, metric string }{{"", "database-bytes"}, {"-wal", "journal-bytes"}} {
				info, err := os.Stat(f.path + file.suffix)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(info.Size()), file.metric)
				if file.suffix == "" {
					b.ReportMetric(float64(info.Size()-int64(initial.PageCount)*int64(initial.PageSize)), "database-growth-bytes")
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
			daltest.Update(b, f.db.DAL, "workflow_status", daltest.Row{"completed_at": time.Now().Add(-3 * time.Hour).UnixMilli()}, daltest.Row{"name": tickName})
			started = time.Now()
			if _, err := f.r.Purge(ctx); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(time.Since(started).Nanoseconds()), "purge-ns")
			remaining := daltest.Count(b, f.db.DAL, "workflow_status", daltest.Row{"name": tickName})
			if applications := daltest.Count(b, f.db.DAL, "workflow_runs", daltest.Row{"workflow_id": w.ID}); applications != items {
				b.Fatalf("retained application runs: %d", applications)
			}
			b.ReportMetric(float64(remaining), "maintenance-remaining")
		})
	}
}
