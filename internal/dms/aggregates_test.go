package dms

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal/catalog"
	"papergo/internal/model"
)

// Aggregates work out exact sums, averages, minimums, maximums and counts
// of a query's items, in groups of a field or of the days, weeks, months
// or years of a date-time in a time zone, as the reader may read them.
func TestQueryAggregates(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	fieldOK(t, s, l.ID, CreateField{Key: "temp", Label: "Temperature", Type: "decimal", Scale: 1, Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "taken", Label: "Taken", Type: "datetime", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "day", Label: "Day", Type: "date", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "price", Label: "Price", Type: "currency", Indexed: true, Options: model.FieldOptions{CurrencyCode: "EUR"}})
	fieldOK(t, s, l.ID, CreateField{Key: "units", Label: "Units", Type: "integer", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "kind", Label: "Kind", Type: "choice", Choices: []string{"a", "b"}, Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "loose", Label: "Not indexed", Type: "decimal", Scale: 1})
	items := []map[string]any{
		// 23:30 on 28 March in Madrid.
		{"temp": json.Number("10.0"), "taken": "2026-03-28T22:30:00Z", "day": "2026-03-28", "price": json.Number("1.5"), "units": json.Number("1"), "kind": "a"},
		// 00:30 on 29 March in Madrid, still the 28th in UTC.
		{"temp": json.Number("11.0"), "taken": "2026-03-28T23:30:00Z", "day": "2026-03-29", "price": json.Number("2.25"), "units": json.Number("2"), "kind": "a"},
		// 14:00 on 29 March in Madrid, after the clocks went forward.
		{"temp": json.Number("-3.5"), "taken": "2026-03-29T12:00:00Z", "day": "2026-03-29", "units": json.Number("2"), "kind": "b"},
		// Not taken.
		{"temp": json.Number("5.5"), "kind": "b"},
	}
	ids := []string{}
	for _, values := range items {
		item := create(t, s, l.ID, "item", "Reading", values)
		publishItem(t, s, item)
		ids = append(ids, item.ID)
	}
	run := func(subject string, in AggregateRequest) []AggregateGroup {
		t.Helper()
		out, err := s.QueryAggregates(testContext, subject, l.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.Data
	}
	same := func(what string, got, want any) {
		t.Helper()
		g, _ := json.Marshal(got)
		x, _ := json.Marshal(want)
		if string(g) != string(x) {
			t.Fatalf("%s:\n got %s\nwant %s", what, g, x)
		}
	}

	all := run("reader", AggregateRequest{Aggregates: []Aggregate{
		{Op: AggregateAvg, Field: "temp"}, {Op: AggregateSum, Field: "temp"}, {Op: AggregateMin, Field: "temp"}, {Op: AggregateMax, Field: "temp"},
		{Op: AggregateCount, Field: "price"}, {Op: AggregateSum, Field: "price"}, {Op: AggregateAvg, Field: "price"}, {Op: AggregateAvg, Field: "units", As: "units_on_average"},
		{Op: AggregateMin, Field: "taken"}, {Op: AggregateMax, Field: "day"},
	}})
	same("every item", all, []AggregateGroup{{Key: nil, Count: 4, Values: map[string]any{
		"avg_temp": "5.75", "sum_temp": "23.0", "min_temp": "-3.5", "max_temp": "11.0",
		"count_price": 2, "sum_price": "3.75", "avg_price": "1.875", "units_on_average": "1.6667",
		"min_taken": "2026-03-28T22:30:00Z", "max_day": "2026-03-29",
	}}})

	madrid := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "taken"}, Interval: IntervalDay, TimeZone: "Europe/Madrid", Aggregates: []Aggregate{{Op: AggregateAvg, Field: "temp"}, {Op: AggregateMax, Field: "temp"}}})
	same("days in Madrid, the untaken item last", madrid, []AggregateGroup{
		{Key: "2026-03-28", Count: 1, Values: map[string]any{"avg_temp": "10", "max_temp": "10.0"}},
		{Key: "2026-03-29", Count: 2, Values: map[string]any{"avg_temp": "3.75", "max_temp": "11.0"}},
		{Key: nil, Count: 1, Values: map[string]any{"avg_temp": "5.5", "max_temp": "5.5"}},
	})
	utc := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "taken"}, Interval: IntervalDay, Aggregates: []Aggregate{{Op: AggregateCount, Field: "temp"}}})
	same("days in UTC", utc, []AggregateGroup{
		{Key: "2026-03-28", Count: 2, Values: map[string]any{"count_temp": 2}},
		{Key: "2026-03-29", Count: 1, Values: map[string]any{"count_temp": 1}},
		{Key: nil, Count: 1, Values: map[string]any{"count_temp": 1}},
	})
	for interval, key := range map[AggregateInterval]string{IntervalWeek: "2026-03-23", IntervalMonth: "2026-03-01", IntervalYear: "2026-01-01"} {
		got := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "taken"}, Interval: interval, TimeZone: "Europe/Madrid", Aggregates: []Aggregate{{Op: AggregateSum, Field: "units"}}})
		same(string(interval), got, []AggregateGroup{{Key: key, Count: 3, Values: map[string]any{"sum_units": "5"}}, {Key: nil, Count: 1, Values: map[string]any{"sum_units": nil}}})
	}
	dates := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "day"}, Interval: IntervalDay, TimeZone: "Pacific/Auckland", Aggregates: []Aggregate{{Op: AggregateCount, Field: "day"}}})
	same("dates have no time zone", dates, []AggregateGroup{
		{Key: "2026-03-28", Count: 1, Values: map[string]any{"count_day": 1}},
		{Key: "2026-03-29", Count: 2, Values: map[string]any{"count_day": 2}},
		{Key: nil, Count: 1, Values: map[string]any{"count_day": 0}},
	})
	created := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "$created_at"}, Interval: IntervalYear, Aggregates: []Aggregate{{Op: AggregateMax, Field: "$created_at"}}})
	if len(created) != 1 || created[0].Key != time.Now().UTC().Format("2006")+"-01-01" || created[0].Count != 4 || created[0].Values["max_created_at"] == nil {
		t.Fatalf("a built-in date-time by year: %+v", created)
	}
	byTemp := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "temp"}, Aggregates: []Aggregate{{Op: AggregateCount, Field: "kind"}}})
	keys := []any{}
	for _, g := range byTemp {
		keys = append(keys, g.Key)
	}
	same("decimal keys in numeric order", keys, []any{"-3.5", "5.5", "10.0", "11.0"})
	byKind := run("reader", AggregateRequest{Query: QuerySpec{GroupBy: "kind", Filter: &FilterExpr{Field: "temp", Op: "gt", Value: json.RawMessage(`"0"`)}}, Aggregates: []Aggregate{{Op: AggregateSum, Field: "temp"}}})
	same("a filter and a choice group", byKind, []AggregateGroup{
		{Key: "a", Count: 2, Values: map[string]any{"sum_temp": "21.0"}},
		{Key: "b", Count: 1, Values: map[string]any{"sum_temp": "5.5"}},
	})

	// A draft change counts for those who read drafts only.
	values := map[string]any{"temp": json.Number("100.0"), "taken": "2026-03-28T22:30:00Z", "kind": "a"}
	if _, err := s.Update(testContext, "alice", ids[0], latest(t, s, ids[0]).Version, UpdateResource{Values: &values}); err != nil {
		t.Fatal(err)
	}
	sum := []Aggregate{{Op: AggregateSum, Field: "temp"}}
	same("the reader reads what is published", run("reader", AggregateRequest{Aggregates: sum})[0].Values, map[string]any{"sum_temp": "23.0"})
	same("the author reads the draft", run("alice", AggregateRequest{Aggregates: sum})[0].Values, map[string]any{"sum_temp": "113.0"})
	same("the published surface on request", run("alice", AggregateRequest{Surface: "published", Aggregates: sum})[0].Values, map[string]any{"sum_temp": "23.0"})
	if _, err := s.QueryAggregates(testContext, "stranger", l.ID, AggregateRequest{Aggregates: sum}); err == nil {
		t.Fatal("someone outside the workspace aggregated its list")
	}

	for _, tc := range []struct {
		in   AggregateRequest
		want string
	}{
		{AggregateRequest{}, "1 to 20 figures"},
		{AggregateRequest{Aggregates: make([]Aggregate, 21)}, "1 to 20 figures"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: "median", Field: "temp"}}}, "count, sum, avg, min or max"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateCount}}}, "names its field"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateSum, Field: "kind"}}}, "adds up"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateMax, Field: "kind"}}}, "compares numeric"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateSum, Field: "loose"}}}, "loose"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateSum, Field: "temp"}, {Op: AggregateSum, Field: "temp"}}}, "unique"},
		{AggregateRequest{Aggregates: []Aggregate{{Op: AggregateSum, Field: "temp", As: "Bad name"}}}, "lowercase"},
		{AggregateRequest{Interval: IntervalDay, Aggregates: sum}, "name one"},
		{AggregateRequest{Query: QuerySpec{GroupBy: "kind"}, Interval: IntervalDay, Aggregates: sum}, "date or date-time"},
		{AggregateRequest{Query: QuerySpec{GroupBy: "taken"}, Interval: "hour", Aggregates: sum}, "day, week, month or year"},
		{AggregateRequest{Query: QuerySpec{GroupBy: "taken"}, Interval: IntervalDay, TimeZone: "Mars/Olympus", Aggregates: sum}, "IANA"},
		{AggregateRequest{TimeZone: "UTC", Aggregates: sum}, "belongs to an interval"},
	} {
		_, err := s.QueryAggregates(testContext, "reader", l.ID, tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.in, err, tc.want)
		}
	}
}

// Averages round half to even, and decimals keep their sign and zeros.
func TestAggregateArithmetic(t *testing.T) {
	for _, tc := range []struct {
		num, den int64
		want     int64
	}{{5, 2, 2}, {7, 2, 4}, {-5, 2, -2}, {-7, 2, -4}, {10, 3, 3}, {-10, 3, -3}, {2, 3, 1}, {-2, 3, -1}} {
		if got := divide(big.NewInt(tc.num), big.NewInt(tc.den)); got.Int64() != tc.want {
			t.Errorf("%d/%d = %d, want %d", tc.num, tc.den, got.Int64(), tc.want)
		}
	}
	for _, tc := range []struct {
		n     int64
		scale int
		trim  bool
		want  string
	}{{230, 1, false, "23.0"}, {-35, 1, false, "-3.5"}, {5, 2, false, "0.05"}, {-5, 2, true, "-0.05"}, {18750, 4, true, "1.875"}, {0, 2, true, "0"}, {-0, 1, false, "0.0"}, {120, 1, true, "12"}} {
		if got := decimalText(big.NewInt(tc.n), tc.scale, tc.trim); got != tc.want {
			t.Errorf("decimalText(%d, %d, %v) = %q, want %q", tc.n, tc.scale, tc.trim, got, tc.want)
		}
	}
	key, _ := numberKey(json.Number("-12.5"))
	if x, err := exactOf(&catalog.FieldDefinition{Type: catalog.FieldCurrency}, key); err != nil || decimalText(x.n, x.scale, true) != "-12.5" {
		t.Fatal("an exact number from its key", x, err)
	}
	sunday, _ := time.Parse(time.RFC3339, "2026-03-29T14:00:00+02:00")
	if !reflect.DeepEqual(period(sunday, IntervalWeek), "2026-03-23") {
		t.Fatal("weeks start on Monday")
	}
}
