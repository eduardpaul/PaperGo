package dms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/events"
)

// TestWriteCost measures what saving documents costs the database: the
// statements, rows and write-ahead log of creating items and of editing
// one field, in a document library with keywords and indexed fields. It
// asserts nothing and runs only when PAPERGO_WRITE_COST names a file to
// append its report to.
func TestWriteCost(t *testing.T) {
	out := os.Getenv("PAPERGO_WRITE_COST")
	if out == "" {
		t.Skip("PAPERGO_WRITE_COST names the report file")
	}
	const n = 40
	ctx := context.Background()
	db := daltest.Database(t)
	meter := daltest.NewWriteMeter(t, db)
	s := NewService(meter.SQL)
	w, err := s.Create(ctx, "writer", "", CreateResource{Kind: "workspace", Name: "Costs"})
	if err != nil {
		t.Fatal(err)
	}
	library, err := s.Create(ctx, "writer", w.ID, CreateResource{TemplateKey: "documents", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []CreateField{
		{Key: "status", Label: "Status", Type: "choice", Choices: []string{"new", "review", "done"}, Indexed: true},
		{Key: "category", Label: "Category", Type: "choice", Choices: []string{"utilities", "rent", "travel"}, Indexed: true},
		{Key: "vendor", Label: "Vendor", Type: "text", Indexed: true},
		{Key: "number", Label: "Number", Type: "text", Indexed: true},
		{Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true},
		{Key: "pages", Label: "Pages", Type: "integer", Indexed: true},
		{Key: "due", Label: "Due", Type: "date", Indexed: true},
		{Key: "paid", Label: "Paid", Type: "boolean", Indexed: true},
	} {
		if _, err = s.CreateField(ctx, "organization-admin", library.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	values := func(i int) map[string]any {
		return map[string]any{"description": "Monthly invoice", "status": "new", "category": "utilities", "vendor": "Power Co", "number": fmt.Sprintf("INV-%04d", i),
			"amount": json.Number("120.50"), "pages": json.Number("2"), "due": "2026-12-01", "paid": false}
	}
	keywords := []string{"finance", "2026", "energy"}
	var report strings.Builder
	fmt.Fprintf(&report, "## %s, %d operations each\n", engineName(), n)

	meter.Start(t)
	items := make([]struct {
		id      string
		version int
	}, n)
	for i := range items {
		item, err := s.Create(ctx, "writer", library.ID, CreateResource{Kind: "item", Name: fmt.Sprintf("Invoice %d", i), Keywords: keywords, Values: values(i)})
		if err != nil {
			t.Fatal(err)
		}
		items[i].id, items[i].version = item.ID, item.Version
	}
	meter.Stop(t).Write(&report, "create item", n)

	edit := func(name string, change func(i int) map[string]any) {
		meter.Start(t)
		for i := range items {
			patch := change(i)
			item, err := s.Update(ctx, "writer", items[i].id, items[i].version, UpdateResource{Values: &patch})
			if err != nil {
				t.Fatal(err)
			}
			items[i].version = item.Version
		}
		meter.Stop(t).Write(&report, name, n)
	}
	edit("edit a decimal field", func(i int) map[string]any { return map[string]any{"amount": json.Number(fmt.Sprintf("%d.25", 100+i))} })
	edit("edit a choice field", func(i int) map[string]any { return map[string]any{"status": "review"} })

	// Workflows dispatch events after the write; a backlog of item events,
	// as a bulk import leaves, waits while edits continue.
	backlog := make([]events.Event, 5000)
	for i := range backlog {
		backlog[i] = events.Event{ID: NewEventID(), Type: EventItemUpdated, WorkspaceID: w.ID, CollectionID: library.ID, ResourceID: uuid.NewString(), Actor: "writer",
			Data: map[string]any{"revision_id": uuid.NewString(), "revision_number": 2}}
	}
	if err = db.DAL.Events().Append(ctx, backlog); err != nil {
		t.Fatal(err)
	}
	edit("edit behind 5000 queued events", func(i int) map[string]any { return map[string]any{"amount": json.Number(fmt.Sprintf("%d.75", 100+i))} })

	t.Log("\n" + report.String())
	f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.WriteString(report.String() + "\n"); err != nil {
		t.Fatal(err)
	}
}

func engineName() string {
	if daltest.PostgresDSN() != "" {
		return "PostgreSQL"
	}
	return "SQLite"
}
