package dms

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"papergo/internal/database"
	"papergo/internal/testutil"
	"strconv"
	"testing"
	"time"
)

// benchCollection seeds PAPERGO_BENCH_ITEMS items (default 2000) with an
// indexed integer n, an indexed status (absent on every tenth item), every
// other item published, and every fiftieth item under its own ACL scope that
// lets "reader" see drafts, so auto-surface reads merge both surfaces.
func benchCollection(b *testing.B) (*Service, *database.Database, string, string) {
	n := 2000
	if v, err := strconv.Atoi(os.Getenv("PAPERGO_BENCH_ITEMS")); err == nil && v > 0 {
		n = v
	}
	ctx := context.Background()
	db := testutil.Database(b)
	s := NewService(db.Client)
	w, err := s.Create(ctx, "alice", "", CreateResource{Kind: "workspace", Name: "Benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"reader", "read", "allow"}}}); err != nil {
		b.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, CreateResource{Kind: "list", Name: "Records", PublishingEnabled: true})
	if err != nil {
		b.Fatal(err)
	}
	var field string
	for _, f := range []CreateField{{Key: "n", Label: "N", Type: "integer", Indexed: true}, {Key: "status", Label: "Status", Type: "text", Indexed: true}} {
		d, err := s.CreateField(ctx, "alice", l.ID, f)
		if err != nil {
			b.Fatal(err)
		}
		if f.Key == "n" {
			field = d.ID
		}
	}
	for i := 0; i < n; i += MaxBulkOperations {
		ops := []BulkOperation{}
		for j := i; j < i+MaxBulkOperations && j < n; j++ {
			values := map[string]any{"n": num((j * 7919) % n)}
			if j%10 != 0 {
				values["status"] = []string{"open", "closed", "held"}[j%3]
			}
			ops = append(ops, BulkOperation{Action: "create", Create: &BulkCreate{Name: fmt.Sprintf("Item %06d", j), Values: values}})
		}
		created, err := s.Bulk(ctx, "alice", l.ID, BulkRequest{Operations: ops})
		if err != nil {
			b.Fatal(err)
		}
		publish := []BulkOperation{}
		for k, r := range created.Data {
			if (i+k)%2 == 0 {
				publish = append(publish, BulkOperation{Action: "publish", ID: r.ID, Version: r.Version})
			}
		}
		published, err := s.Bulk(ctx, "alice", l.ID, BulkRequest{Operations: publish})
		if err != nil {
			b.Fatal(err)
		}
		for k, r := range published.Data {
			if (i+2*k)%50 == 0 {
				if _, err = s.SetPermissions(ctx, "alice", r.ID, r.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"reader", "read_draft", "allow"}}}); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	return s, db, l.ID, field
}

// BenchmarkCollectionQuery reads 50-row pages of typed queries: system and
// custom sorts, broad and selective filters, the second page via its cursor,
// and auto-surface reads that merge draft and published scans.
func BenchmarkCollectionQuery(b *testing.B) {
	s, _, list, _ := benchCollection(b)
	ctx := context.Background()
	cases := []struct {
		name, subject, surface string
		spec                   QuerySpec
		second                 bool
	}{
		{"id/head", "alice", "head", QuerySpec{}, false},
		{"id/page2", "alice", "head", QuerySpec{}, true},
		{"name/head", "alice", "head", QuerySpec{Sort: SortSpec{Field: "$name"}}, false},
		{"modified-desc/auto", "alice", "auto", QuerySpec{Sort: SortSpec{Field: "$modified_at", Direction: "desc"}}, false},
		{"n/auto", "alice", "auto", QuerySpec{Sort: SortSpec{Field: "n"}}, false},
		{"n-desc/page2", "alice", "head", QuerySpec{Sort: SortSpec{Field: "n", Direction: "desc"}}, true},
		{"n/reader-fragmented", "reader", "auto", QuerySpec{Sort: SortSpec{Field: "n"}}, false},
		{"status-sort/missing-last", "alice", "head", QuerySpec{Sort: SortSpec{Field: "status", Direction: "desc"}}, true},
		{"broad-filter/n", "alice", "auto", QuerySpec{Sort: SortSpec{Field: "n"}, Filter: &FilterExpr{Field: "status", Value: []byte(`"open"`)}}, false},
		{"selective-filter/modified", "alice", "head", QuerySpec{Sort: SortSpec{Field: "$modified_at"}, Filter: &FilterExpr{Field: "n", Value: []byte(`77`)}}, false},
		{"search/name", "reader", "auto", QuerySpec{Sort: SortSpec{Field: "$name"}, Search: "Item"}, false},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			in := QueryRequest{Query: c.spec, Surface: c.surface}
			if c.second {
				first, err := s.Query(ctx, c.subject, list, in)
				if err != nil {
					b.Fatal(err)
				}
				in.After = first.NextCursor
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Query(ctx, c.subject, list, in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("total", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := s.Query(ctx, "alice", list, QueryRequest{IncludeTotal: true}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkWriteDuringIndexBuild times single-item edits on an idle writer and
// while background index builds of the whole collection run, reporting the
// slowest wait for the writer and the WAL size afterwards.
func BenchmarkWriteDuringIndexBuild(b *testing.B) {
	for _, building := range []bool{false, true} {
		b.Run(map[bool]string{false: "idle", true: "building"}[building], func(b *testing.B) { benchWrites(b, building) })
	}
}

func benchWrites(b *testing.B, building bool) {
	s, db, list, field := benchCollection(b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunOperations(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	items, err := s.Query(ctx, "alice", list, QueryRequest{Surface: "head"})
	if err != nil {
		b.Fatal(err)
	}
	var worst, total time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		collection, err := s.Get(ctx, "alice", list)
		if err != nil {
			b.Fatal(err)
		}
		indexed := i%2 == 1
		if building {
			if _, err = s.UpdateField(ctx, "alice", list, field, collection.Version, UpdateField{Indexed: &indexed}); err != nil {
				b.Fatal(err)
			}
		}
		r, err := s.Get(ctx, "alice", items.Data[i%len(items.Data)].ID)
		if err != nil {
			b.Fatal(err)
		}
		name := fmt.Sprintf("Edited %d", i)
		b.StartTimer()
		start := time.Now()
		if _, err = s.Update(ctx, "alice", r.ID, r.Version, UpdateResource{Name: &name}); err != nil {
			b.Fatal(err)
		}
		took := time.Since(start)
		total += took
		worst = max(worst, took)
	}
	b.StopTimer()
	b.ReportMetric(float64(worst.Microseconds())/1000, "max-write-ms")
	b.ReportMetric(float64(total.Microseconds())/1000/float64(b.N), "mean-write-ms")
	var seq int
	var name, file string
	if err = db.SQL.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		b.Fatal(err)
	}
	if info, err := os.Stat(file + "-wal"); err == nil {
		b.ReportMetric(float64(info.Size())/(1<<20), "wal-MiB")
	}
}
