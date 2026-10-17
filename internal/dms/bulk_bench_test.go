package dms

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/testutil"
	"testing"
)

// Compare the same indexed, automatically published edits with either one
// transaction per item or one transaction per bounded batch. Fixture creation
// is excluded; revisions accumulate just as they do during real editing.
func BenchmarkItemUpdates(b *testing.B) {
	for _, size := range []int{1, 10, MaxBulkOperations} {
		for _, bulk := range []bool{false, true} {
			mode := "individual"
			if bulk {
				mode = "bulk"
			}
			b.Run(fmt.Sprintf("%s/%d", mode, size), func(b *testing.B) {
				ctx := context.Background()
				s := NewService(testutil.Database(b).SQL)
				w, err := s.Create(ctx, "writer", "", CreateResource{Kind: "workspace", Name: "Benchmark"})
				if err != nil {
					b.Fatal(err)
				}
				list, err := s.Create(ctx, "writer", w.ID, CreateResource{Kind: "list", Name: "Records"})
				if err != nil {
					b.Fatal(err)
				}
				_, err = s.CreateField(ctx, "organization-admin", list.ID, CreateField{Key: "amount", Label: "Amount", Type: "integer", Indexed: true})
				if err != nil {
					b.Fatal(err)
				}
				operations := make([]BulkOperation, size)
				name := "Updated record"
				values := map[string]any{"amount": json.Number("9007199254740993")}
				update := BulkUpdate{Name: &name, Values: &values}
				for i := range operations {
					item, err := s.Create(ctx, "writer", list.ID, CreateResource{Kind: "item", Name: fmt.Sprintf("Record %d", i), Keywords: []string{"finance"}, Values: values})
					if err != nil {
						b.Fatal(err)
					}
					operations[i] = BulkOperation{Action: "update", ID: item.ID, Version: item.Version, Update: &update}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for n := 0; n < b.N; n++ {
					if bulk {
						if _, err := s.Bulk(ctx, "writer", list.ID, BulkRequest{Operations: operations}); err != nil {
							b.Fatal(err)
						}
					} else {
						for _, op := range operations {
							if _, err := s.Update(ctx, "writer", op.ID, op.Version, UpdateResource{Name: update.Name, Values: update.Values}); err != nil {
								b.Fatal(err)
							}
						}
					}
					for i := range operations {
						operations[i].Version++
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "items/s")
			})
		}
	}
}

// Measure bulk creation of items with six indexed columns, as imports do.
// Run with -cpuprofile to see where ingest time goes.
func BenchmarkBulkCreate(b *testing.B) {
	ctx := context.Background()
	s := NewService(testutil.Database(b).SQL)
	w, err := s.Create(ctx, "writer", "", CreateResource{Kind: "workspace", Name: "Benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	list, err := s.Create(ctx, "writer", w.ID, CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		b.Fatal(err)
	}
	for _, field := range []CreateField{
		{Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true},
		{Key: "status", Label: "Status", Type: "choice", Choices: []string{"Open", "Paid"}, Indexed: true},
		{Key: "due", Label: "Due", Type: "date", Indexed: true},
		{Key: "owner", Label: "Owner", Type: "person", Indexed: true},
		{Key: "reference", Label: "Reference", Type: "text", Indexed: true},
		{Key: "count", Label: "Count", Type: "integer", Indexed: true},
	} {
		if _, err = s.CreateField(ctx, "organization-admin", list.ID, field); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	created := 0
	for n := 0; n < b.N; n++ {
		operations := make([]BulkOperation, MaxBulkOperations)
		for i := range operations {
			created++
			operations[i] = BulkOperation{Action: "create", Create: &BulkCreate{
				Name:     fmt.Sprintf("Invoice INV-%07d Acme Travel", created),
				Keywords: []string{"finance"},
				Values: map[string]any{
					"amount": json.Number(fmt.Sprintf("%d.%02d", created%10000, created%100)), "status": "Open", "due": "2026-10-13",
					"owner": "writer", "reference": fmt.Sprintf("PO-%06d", created), "count": json.Number(fmt.Sprint(created % 50)),
				},
			}}
		}
		if _, err := s.Bulk(ctx, "writer", list.ID, BulkRequest{Operations: operations}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(created)/b.Elapsed().Seconds(), "items/s")
}
