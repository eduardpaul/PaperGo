package dms

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"papergo/internal/model"
)

// aggregateRequest asks for figures of a collection's items.
func aggregateRequest(groups []AggregateGroupBy, figures ...Aggregate) AggregateRequest {
	return AggregateRequest{AggregateSpec: AggregateSpec{Groups: groups, Aggregates: figures}}
}

func groupedBy(fields ...string) []AggregateGroupBy {
	out := make([]AggregateGroupBy, len(fields))
	for i, field := range fields {
		out[i].Field = field
	}
	return out
}

// group is a group of a response: its count, its figures and its keys.
func group(count int, values map[string]any, keys ...any) AggregateGroup {
	out := AggregateGroup{Keys: []AggregateKey{}, Count: count, Values: values}
	for _, key := range keys {
		out.Keys = append(out.Keys, AggregateKey{Value: key})
	}
	return out
}

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
	// A folder holding values is not an item: no figure, count or group sees it.
	create(t, s, l.ID, "folder", "Archive", map[string]any{"temp": json.Number("1000.0"), "kind": "a"})
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

	all := run("reader", aggregateRequest(nil,
		Aggregate{Op: AggregateAvg, Field: "temp"}, Aggregate{Op: AggregateSum, Field: "temp"}, Aggregate{Op: AggregateMin, Field: "temp"}, Aggregate{Op: AggregateMax, Field: "temp"},
		Aggregate{Op: AggregateCount, Field: "price"}, Aggregate{Op: AggregateSum, Field: "price"}, Aggregate{Op: AggregateAvg, Field: "price"}, Aggregate{Op: AggregateAvg, Field: "units", As: "units_on_average"},
		Aggregate{Op: AggregateMin, Field: "taken"}, Aggregate{Op: AggregateMax, Field: "day"},
	))
	same("every item", all, []AggregateGroup{group(4, map[string]any{
		"avg_temp": "5.75", "sum_temp": "23.0", "min_temp": "-3.5", "max_temp": "11.0",
		"count_price": 2, "sum_price": "3.75", "avg_price": "1.875", "units_on_average": "1.6667",
		"min_taken": "2026-03-28T22:30:00Z", "max_day": "2026-03-29",
	})})

	madrid := run("reader", aggregateRequest([]AggregateGroupBy{{Field: "taken", Interval: IntervalDay, TimeZone: "Europe/Madrid"}}, Aggregate{Op: AggregateAvg, Field: "temp"}, Aggregate{Op: AggregateMax, Field: "temp"}))
	same("days in Madrid, the untaken item last", madrid, []AggregateGroup{
		group(1, map[string]any{"avg_temp": "10", "max_temp": "10.0"}, "2026-03-28"),
		group(2, map[string]any{"avg_temp": "3.75", "max_temp": "11.0"}, "2026-03-29"),
		group(1, map[string]any{"avg_temp": "5.5", "max_temp": "5.5"}, nil),
	})
	utc := run("reader", aggregateRequest([]AggregateGroupBy{{Field: "taken", Interval: IntervalDay}}, Aggregate{Op: AggregateCount, Field: "temp"}))
	same("days in UTC", utc, []AggregateGroup{
		group(2, map[string]any{"count_temp": 2}, "2026-03-28"),
		group(1, map[string]any{"count_temp": 1}, "2026-03-29"),
		group(1, map[string]any{"count_temp": 1}, nil),
	})
	for interval, key := range map[AggregateInterval]string{IntervalWeek: "2026-03-23", IntervalMonth: "2026-03-01", IntervalYear: "2026-01-01"} {
		for _, zone := range []string{"Europe/Madrid", ""} {
			got := run("reader", aggregateRequest([]AggregateGroupBy{{Field: "taken", Interval: interval, TimeZone: zone}}, Aggregate{Op: AggregateSum, Field: "units"}))
			same(string(interval)+" in "+zone, got, []AggregateGroup{group(3, map[string]any{"sum_units": "5"}, key), group(1, map[string]any{"sum_units": nil}, nil)})
		}
	}
	dates := run("reader", aggregateRequest([]AggregateGroupBy{{Field: "day", Interval: IntervalDay, TimeZone: "Pacific/Auckland"}}, Aggregate{Op: AggregateCount, Field: "day"}))
	same("dates have no time zone", dates, []AggregateGroup{
		group(1, map[string]any{"count_day": 1}, "2026-03-28"),
		group(2, map[string]any{"count_day": 2}, "2026-03-29"),
		group(1, map[string]any{"count_day": 0}, nil),
	})
	today := time.Now().UTC()
	for interval, key := range map[AggregateInterval]string{IntervalYear: today.Format("2006") + "-01-01", IntervalDay: today.Format("2006-01-02")} {
		created := run("reader", aggregateRequest([]AggregateGroupBy{{Field: "$created_at", Interval: interval}}, Aggregate{Op: AggregateMax, Field: "$created_at"}))
		if len(created) != 1 || created[0].Keys[0].Value != key || created[0].Count != 4 || created[0].Values["max_created_at"] == nil {
			t.Fatalf("a built-in date-time by %s: %+v", interval, created)
		}
	}
	byTemp := run("reader", aggregateRequest(groupedBy("temp"), Aggregate{Op: AggregateCount, Field: "kind"}))
	keys := []any{}
	for _, g := range byTemp {
		keys = append(keys, g.Keys[0].Value)
	}
	same("decimal keys in numeric order", keys, []any{"-3.5", "5.5", "10.0", "11.0"})
	filtered := aggregateRequest(groupedBy("kind"), Aggregate{Op: AggregateSum, Field: "temp"})
	filtered.Query.Filter = &FilterExpr{Field: "temp", Op: "gt", Value: json.RawMessage(`"0"`)}
	same("a filter and a choice group", run("reader", filtered), []AggregateGroup{
		group(2, map[string]any{"sum_temp": "21.0"}, "a"),
		group(1, map[string]any{"sum_temp": "5.5"}, "b"),
	})
	same("groups by several fields, each period merging its rows", run("reader", aggregateRequest([]AggregateGroupBy{{Field: "taken", Interval: IntervalMonth, TimeZone: "Europe/Madrid"}, {Field: "kind"}}, Aggregate{Op: AggregateSum, Field: "temp"})), []AggregateGroup{
		group(2, map[string]any{"sum_temp": "21.0"}, "2026-03-01", "a"),
		group(1, map[string]any{"sum_temp": "-3.5"}, "2026-03-01", "b"),
		group(1, map[string]any{"sum_temp": "5.5"}, nil, "b"),
	})

	// A draft change counts for those who read drafts only.
	values := map[string]any{"temp": json.Number("100.0"), "taken": "2026-03-28T22:30:00Z", "kind": "a"}
	if _, err := s.Update(testContext, "alice", ids[0], latest(t, s, ids[0]).Version, UpdateResource{Values: &values}); err != nil {
		t.Fatal(err)
	}
	sum := []Aggregate{{Op: AggregateSum, Field: "temp"}}
	same("the reader reads what is published", run("reader", aggregateRequest(nil, sum...))[0].Values, map[string]any{"sum_temp": "23.0"})
	same("the author reads the draft", run("alice", aggregateRequest(nil, sum...))[0].Values, map[string]any{"sum_temp": "113.0"})
	published := aggregateRequest(nil, sum...)
	published.Surface = "published"
	same("the published surface on request", run("alice", published)[0].Values, map[string]any{"sum_temp": "23.0"})
	if _, err := s.QueryAggregates(testContext, "stranger", l.ID, aggregateRequest(nil, sum...)); err == nil {
		t.Fatal("someone outside the workspace aggregated its list")
	}

	interval := func(field string, interval AggregateInterval, zone string) []AggregateGroupBy {
		return []AggregateGroupBy{{Field: field, Interval: interval, TimeZone: zone}}
	}
	query := func(q QuerySpec) AggregateRequest {
		in := aggregateRequest(nil, sum...)
		in.Query = q
		return in
	}
	for _, tc := range []struct {
		in   AggregateRequest
		want string
	}{
		{AggregateRequest{}, "1 to 20 figures"},
		{aggregateRequest(nil, make([]Aggregate, 21)...), "1 to 20 figures"},
		{aggregateRequest(nil, Aggregate{Op: "median", Field: "temp"}), "count, sum, avg, min or max"},
		{aggregateRequest(nil, Aggregate{Op: AggregateCount}), "names its field"},
		{aggregateRequest(nil, Aggregate{Op: AggregateSum, Field: "kind"}), "adds up"},
		{aggregateRequest(nil, Aggregate{Op: AggregateMax, Field: "kind"}), "compares numeric"},
		{aggregateRequest(nil, Aggregate{Op: AggregateSum, Field: "loose"}), "loose"},
		{aggregateRequest(nil, Aggregate{Op: AggregateSum, Field: "temp"}, Aggregate{Op: AggregateSum, Field: "temp"}), "unique"},
		{aggregateRequest(nil, Aggregate{Op: AggregateSum, Field: "temp", As: "Bad name"}), "lowercase"},
		{aggregateRequest(interval("kind", IntervalDay, ""), sum...), "date or date-time"},
		{aggregateRequest(interval("taken", "hour", ""), sum...), "day, week, month or year"},
		{aggregateRequest(interval("taken", IntervalDay, "Mars/Olympus"), sum...), "IANA"},
		{aggregateRequest(interval("taken", "", "UTC"), sum...), "belongs to an interval"},
		{aggregateRequest(groupedBy("kind", "kind"), sum...), "unique fields"},
		{aggregateRequest(groupedBy(""), sum...), "unique fields"},
		{aggregateRequest(groupedBy("a", "b", "c", "d", "e"), sum...), "at most 4 fields"},
		{query(QuerySpec{GroupBy: "kind"}), "query.group_by"},
		{query(QuerySpec{Sort: []SortSpec{{Field: "temp"}}}), "query.sort"},
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
	sunday, _ := time.Parse(time.RFC3339, "2026-03-29T14:00:00+02:00")
	if !reflect.DeepEqual(period(sunday, IntervalWeek), "2026-03-23") {
		t.Fatal("weeks start on Monday")
	}
}

// Sums stay exact at the edges of what fields hold, negative values and
// values of many digits included, and a query that matches nothing still
// has its one group.
func TestAggregateExactSums(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "amount", Label: "Amount", Type: "number", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "big", Label: "Big", Type: "integer", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "cents", Label: "Cents", Type: "decimal", Scale: 2, Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "kind", Label: "Kind", Type: "text", Indexed: true})
	for _, values := range []map[string]any{
		{"amount": json.Number("79228162514264337593543950335"), "big": json.Number("9007199254740993"), "cents": json.Number("-0.05"), "kind": "a"},
		{"amount": json.Number("79228162514264337593543950335"), "big": json.Number("9007199254740993"), "cents": json.Number("-1999999999.99"), "kind": "a"},
		{"amount": json.Number("-0.0000000000000000000000000001"), "big": json.Number("-1000000001"), "cents": json.Number("0.01"), "kind": "a"},
		{"amount": json.Number("-12.5"), "big": json.Number("-1"), "kind": "b"},
		{"amount": json.Number("12.5"), "big": json.Number("1"), "kind": "b"},
	} {
		publishItem(t, s, create(t, s, l.ID, "item", "Row", values))
	}
	figures := []Aggregate{
		{Op: AggregateSum, Field: "amount"}, {Op: AggregateSum, Field: "big"}, {Op: AggregateSum, Field: "cents"},
		{Op: AggregateAvg, Field: "amount"}, {Op: AggregateAvg, Field: "big"}, {Op: AggregateAvg, Field: "cents"},
		{Op: AggregateMin, Field: "amount"}, {Op: AggregateMax, Field: "amount"}, {Op: AggregateMin, Field: "big"}, {Op: AggregateMax, Field: "cents"},
		{Op: AggregateCount, Field: "cents"},
	}
	out, err := s.QueryAggregates(testContext, "alice", l.ID, aggregateRequest(groupedBy("kind"), figures...))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(out.Data)
	want := `[{"keys":[{"value":"a"}],"count":3,"values":{"avg_amount":"52818775009509558395695966890","avg_big":"6004799169827328.3333","avg_cents":"-666666666.676667","count_cents":3,"max_amount":"79228162514264337593543950335","max_cents":"0.01","min_amount":"-0.0000000000000000000000000001","min_big":"-1000000001","sum_amount":"158456325028528675187087900669.9999999999999999999999999999","sum_big":"18014397509481985","sum_cents":"-2000000000.03"}},` +
		`{"keys":[{"value":"b"}],"count":2,"values":{"avg_amount":"0","avg_big":"0","avg_cents":null,"count_cents":0,"max_amount":"12.5","max_cents":null,"min_amount":"-12.5","min_big":"-1","sum_amount":"0","sum_big":"0","sum_cents":null}}]`
	if string(got) != want {
		t.Fatalf("exact figures:\n got %s\nwant %s", got, want)
	}
	nothing := aggregateRequest(nil, figures[0])
	nothing.Query.Filter = &FilterExpr{Field: "kind", Op: "eq", Value: json.RawMessage(`"z"`)}
	none, err := s.QueryAggregates(testContext, "alice", l.ID, nothing)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ = json.Marshal(none.Data); string(got) != `[{"keys":[],"count":0,"values":{"sum_amount":null}}]` {
		t.Fatalf("no match: %s", got)
	}
}

// The groups come in pages, in the order a request sorts them by, missing
// values last whichever the direction, and a cursor belongs to its request.
func TestAggregateSortAndPages(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "kind", Label: "Kind", Type: "text", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "units", Label: "Units", Type: "integer", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "cost", Label: "Cost", Type: "currency", Indexed: true, Options: model.FieldOptions{CurrencyCode: "EUR"}})
	for _, row := range []struct {
		kind string
		units,
		cost any
	}{{"a", json.Number("1"), json.Number("9")}, {"a", json.Number("2"), json.Number("10")}, {"b", json.Number("100"), json.Number("2.5")}, {"c", nil, nil}, {"c", nil, nil}, {"c", nil, nil}, {"", json.Number("7"), json.Number("-1")}} {
		values := map[string]any{}
		if row.kind != "" {
			values["kind"] = row.kind
		}
		if row.units != nil {
			values["units"], values["cost"] = row.units, row.cost
		}
		publishItem(t, s, create(t, s, l.ID, "item", "Row", values))
	}
	figures := []Aggregate{{Op: AggregateSum, Field: "units"}, {Op: AggregateAvg, Field: "cost"}, {Op: AggregateMax, Field: "cost"}, {Op: AggregateCount, Field: "units"}}
	order := func(sort ...SortSpec) []any {
		t.Helper()
		in := aggregateRequest(groupedBy("kind"), figures...)
		in.Sort = sort
		out, err := s.QueryAggregates(testContext, "alice", l.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		keys := []any{}
		for _, g := range out.Data {
			keys = append(keys, g.Keys[0].Value)
		}
		return keys
	}
	for _, tc := range []struct {
		sort []SortSpec
		want []any
	}{
		{nil, []any{"a", "b", "c", nil}},
		{[]SortSpec{{Field: "kind", Direction: "desc"}}, []any{"c", "b", "a", nil}},
		{[]SortSpec{{Field: "$count", Direction: "desc"}}, []any{"c", "a", "b", nil}},
		{[]SortSpec{{Field: "$count"}, {Field: "sum_units", Direction: "desc"}}, []any{"b", nil, "a", "c"}},
		{[]SortSpec{{Field: "sum_units", Direction: "desc"}}, []any{"b", nil, "a", "c"}},
		{[]SortSpec{{Field: "sum_units"}}, []any{"a", nil, "b", "c"}},
		// Averages 9.5, 2.5 and -1 in exact numbers, not by their text.
		{[]SortSpec{{Field: "avg_cost", Direction: "desc"}}, []any{"a", "b", nil, "c"}},
		{[]SortSpec{{Field: "max_cost"}}, []any{nil, "b", "a", "c"}},
		{[]SortSpec{{Field: "count_units", Direction: "desc"}}, []any{"a", "b", nil, "c"}},
	} {
		if got := order(tc.sort...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("sort %+v: %v, want %v", tc.sort, got, tc.want)
		}
	}

	in := aggregateRequest(groupedBy("kind"), figures...)
	in.Sort = []SortSpec{{Field: "sum_units", Direction: "desc"}}
	in.Limit = 3
	first, err := s.QueryAggregates(testContext, "alice", l.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 3 || first.Total != 4 || first.NextCursor == "" {
		t.Fatalf("the first page: %+v", first)
	}
	in.After = first.NextCursor
	last, err := s.QueryAggregates(testContext, "alice", l.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Data) != 1 || last.Data[0].Keys[0].Value != "c" || last.Total != 4 || last.NextCursor != "" {
		t.Fatalf("the last page: %+v", last)
	}
	other := aggregateRequest(groupedBy("kind"), figures[:1]...)
	other.After = first.NextCursor
	if _, err = s.QueryAggregates(testContext, "alice", l.ID, other); err == nil || !strings.Contains(err.Error(), "cursor does not match") {
		t.Fatal("another request's cursor:", err)
	}
	if _, err = s.QueryAggregates(testContext, "bob", l.ID, in); err == nil {
		t.Fatal("another caller's cursor")
	}
	in.After = "a1.nonsense"
	if _, err = s.QueryAggregates(testContext, "alice", l.ID, in); err == nil || !strings.Contains(err.Error(), "invalid query cursor") {
		t.Fatal("a broken cursor:", err)
	}
	for _, tc := range []struct {
		sort []SortSpec
		want string
	}{
		{[]SortSpec{{Field: "nothing"}}, "sort names a figure"},
		{[]SortSpec{{Field: "$count"}, {Field: "$count"}}, "unique"},
		{[]SortSpec{{Field: "$count", Direction: "up"}}, "asc or desc"},
		{make([]SortSpec, 9), "at most 8"},
	} {
		in := aggregateRequest(groupedBy("kind"), figures...)
		in.Sort = tc.sort
		if _, err := s.QueryAggregates(testContext, "alice", l.ID, in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("sort %+v: %v, want %q", tc.sort, err, tc.want)
		}
	}
	ambiguous := aggregateRequest(groupedBy("kind"), Aggregate{Op: AggregateCount, Field: "units", As: "kind"})
	ambiguous.Sort = []SortSpec{{Field: "kind"}}
	if _, err = s.QueryAggregates(testContext, "alice", l.ID, ambiguous); err == nil || !strings.Contains(err.Error(), "both a group and a figure") {
		t.Fatal("an ambiguous sort:", err)
	}
}

// Past its limits a request fails rather than answering about part of the
// items, and a group of items without a value counts as a group.
func TestAggregateLimits(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "kind", Label: "Kind", Type: "text", Indexed: true})
	for _, values := range []map[string]any{{"kind": "a"}, {"kind": "b"}, {}} {
		publishItem(t, s, create(t, s, l.ID, "item", "Row", values))
	}
	run := func(items, groups int) error {
		spec, err := aggregation(AggregateSpec{Groups: groupedBy("kind"), Aggregates: []Aggregate{{Op: AggregateCount, Field: "kind"}}})
		if err != nil {
			t.Fatal(err)
		}
		spec.maxItems, spec.maxGroups = items, groups
		_, err = snapshot(testContext, s, func(tx *Service) (AggregateResult, error) {
			q, e := tx.compileQuery(testContext, "alice", l.ID, QueryRequest{Surface: "auto", itemsOnly: true, aggregation: spec}, false)
			if e != nil {
				return AggregateResult{}, e
			}
			return tx.aggregates(testContext, q, spec)
		})
		return err
	}
	if err := run(3, 3); err != nil {
		t.Fatal("within the limits:", err)
	}
	if err := run(2, 3); err == nil || !strings.Contains(err.Error(), "more than 2 items") {
		t.Fatal("past the items:", err)
	}
	if err := run(3, 2); err == nil || !strings.Contains(err.Error(), "more than 2 groups") {
		t.Fatal("past the groups, the group of no value included:", err)
	}
}

// A view's and a smart folder's figures are of the items they list, as their
// definitions say at the time, across the collections of a smart folder.
func TestViewAndSmartFolderAggregates(t *testing.T) {
	s, w, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "units", Label: "Units", Type: "integer", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "kind", Label: "Kind", Type: "text", Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "loose", Label: "Loose", Type: "integer"})
	second := create(t, s, w.ID, "list", "Second", nil)
	fieldOK(t, s, second.ID, CreateField{Key: "units", Label: "Units", Type: "integer", Indexed: true})
	third := create(t, s, w.ID, "list", "Third", nil)
	fieldOK(t, s, third.ID, CreateField{Key: "kind", Label: "Kind", Type: "text", Indexed: true})
	for _, row := range []struct {
		list   string
		values map[string]any
	}{
		{l.ID, map[string]any{"units": json.Number("1"), "kind": "a"}},
		{l.ID, map[string]any{"units": json.Number("2"), "kind": "b"}},
		{second.ID, map[string]any{"units": json.Number("40")}},
		{third.ID, map[string]any{"kind": "a"}},
	} {
		publishItem(t, s, create(t, s, row.list, "item", "Row", row.values))
	}
	create(t, s, l.ID, "folder", "Holding", map[string]any{"units": json.Number("1000")})
	sum := Aggregate{Op: AggregateSum, Field: "units"}
	same := func(what string, out AggregateResult, err error, want string) {
		t.Helper()
		if err != nil {
			t.Fatal(what, err)
		}
		if got, _ := json.Marshal(out.Data); string(got) != want {
			t.Fatalf("%s:\n got %s\nwant %s", what, got, want)
		}
	}

	view, err := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "A only", Layout: "table", Columns: []string{"units"}, Query: QuerySpec{Sort: []SortSpec{{Field: "units", Direction: "desc"}}, GroupBy: "kind", Filter: &FilterExpr{Field: "kind", Op: "eq", Value: json.RawMessage(`"a"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.QueryViewAggregates(testContext, "alice", view.ID, ViewAggregateRequest{AggregateSpec: AggregateSpec{Aggregates: []Aggregate{sum}}})
	same("the view's filter, not its grouping or sort", out, err, `[{"keys":[],"count":1,"values":{"sum_units":"1"}}]`)
	if _, err = s.QueryViewAggregates(testContext, "stranger", view.ID, ViewAggregateRequest{AggregateSpec: AggregateSpec{Aggregates: []Aggregate{sum}}}); err == nil {
		t.Fatal("someone outside the workspace aggregated a view")
	}
	if _, err = s.QueryViewAggregates(testContext, "alice", view.ID, ViewAggregateRequest{Selected: []NavigationSelection{{Field: "kind"}}, AggregateSpec: AggregateSpec{Aggregates: []Aggregate{sum}}}); err == nil || !strings.Contains(err.Error(), "navigation views") {
		t.Fatal("selected on a table view:", err)
	}

	folder := smartOK(t, s, SmartFolderInput{Name: "All", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Mode: "query", Collections: []string{"Invoices", "Second", "Third"}}})
	run := func(spec AggregateSpec, path ...*string) (AggregateResult, error) {
		return s.QuerySmartFolderAggregates(testContext, "alice", folder.ID, SmartFolderAggregateRequest{Path: path, AggregateSpec: spec})
	}
	out, err = run(AggregateSpec{Aggregates: []Aggregate{sum, {Op: AggregateCount, Field: "kind"}}})
	same("across collections, without folders, those without a field holding none", out, err, `[{"keys":[],"count":4,"values":{"count_kind":3,"sum_units":"43"}}]`)
	out, err = run(AggregateSpec{Groups: groupedBy("kind"), Aggregates: []Aggregate{sum}, Sort: []SortSpec{{Field: "sum_units", Direction: "desc"}}})
	same("groups and sort across collections", out, err, `[{"keys":[{"value":null}],"count":1,"values":{"sum_units":"40"}},{"keys":[{"value":"b"}],"count":1,"values":{"sum_units":"2"}},{"keys":[{"value":"a"}],"count":2,"values":{"sum_units":"1"}}]`)
	if _, err = run(AggregateSpec{Aggregates: []Aggregate{{Op: AggregateSum, Field: "loose"}}}); err == nil || !strings.Contains(err.Error(), "loose") {
		t.Fatal("a collection holding the field unindexed would go missing from the figures:", err)
	}
	if _, err = run(AggregateSpec{Aggregates: []Aggregate{{Op: AggregateSum, Field: "nothing"}}}); err == nil {
		t.Fatal("a field no collection holds")
	}
	if _, err = s.QuerySmartFolderAggregates(testContext, "stranger", folder.ID, SmartFolderAggregateRequest{AggregateSpec: AggregateSpec{Aggregates: []Aggregate{sum}}}); err == nil {
		t.Fatal("someone outside the workspace aggregated a smart folder")
	}

	fieldOK(t, s, third.ID, CreateField{Key: "units", Label: "Units", Type: "decimal", Scale: 2, Indexed: true})
	if _, err = run(AggregateSpec{Aggregates: []Aggregate{sum}}); err == nil || !strings.Contains(err.Error(), "compatible") {
		t.Fatal("the same key with another type adds up nothing:", err)
	}

	nothing := smartOK(t, s, SmartFolderInput{Name: "None", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Mode: "query", Collections: []string{"No such collection"}}})
	out, err = s.QuerySmartFolderAggregates(testContext, "alice", nothing.ID, SmartFolderAggregateRequest{AggregateSpec: AggregateSpec{Aggregates: []Aggregate{sum}}})
	same("a folder over no collection has its one group", out, err, `[{"keys":[],"count":0,"values":{"sum_units":null}}]`)
	out, err = s.QuerySmartFolderAggregates(testContext, "alice", nothing.ID, SmartFolderAggregateRequest{AggregateSpec: AggregateSpec{Groups: groupedBy("kind"), Aggregates: []Aggregate{sum}}})
	same("and no others", out, err, `[]`)
}
