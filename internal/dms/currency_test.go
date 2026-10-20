package dms

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"papergo/internal/model"
)

func TestExactNumberDecimalDomain(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{decimalMaxCoefficient, decimalMaxCoefficient}, {"-" + decimalMaxCoefficient, "-" + decimalMaxCoefficient},
		{"9007199254740993", "9007199254740993"}, {"1e-28", "0.0000000000000000000000000001"},
		{"2.5e-28", "0.0000000000000000000000000002"}, {"3.5e-28", "0.0000000000000000000000000004"},
		{"3.0500000000000000000001e-27", "0.0000000000000000000000000031"},
		{"7922816251426433759354395033.5", "7922816251426433759354395033.5"},
		{"7922816251426433759354395033.55", "7922816251426433759354395034"},
		{"79228162514264337593543950334.5", "79228162514264337593543950334"},
		{"79228162514264337593543950335.4", decimalMaxCoefficient},
		{"-0.000", "0"}, {"1e-999999999", "0"}, {"12.3000", "12.3"}, {"1.234e2", "123.4"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := number(json.Number(tc.input))
			if err != nil || got.String() != tc.want {
				t.Fatal(got, tc.want, err)
			}
		})
	}
	for _, input := range []any{json.Number("79228162514264337593543950336"), json.Number("79228162514264337593543950335.5"), json.Number("1e29"), json.Number("1e999999999"), "12.3", json.Number("NaN"), json.Number("01"), true} {
		if _, err := number(input); err == nil {
			t.Fatal("invalid number accepted", input)
		}
	}
}

func TestCurrencyExactQueriesBoundsDefaultsHistoryAndUniqueness(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	field := fieldOK(t, s, l.ID, CreateField{Key: "price", Label: "Price", Type: "currency", Indexed: true, Options: model.FieldOptions{CurrencyCode: "EUR", DefaultValue: json.RawMessage("12.30")}})
	fieldOK(t, s, l.ID, CreateField{Key: "quantity", Label: "Quantity", Type: "number", Indexed: true})
	prices := []string{"-" + decimalMaxCoefficient, "-9007199254740993", "-0.1", "0", "0.0000000000000000000000000001", "0.1", "9007199254740992", "9007199254740993", "7922816251426433759354395033.5", decimalMaxCoefficient}
	ids := []string{}
	for _, price := range prices {
		item := create(t, s, l.ID, "item", "Price", map[string]any{"price": json.Number(price), "quantity": json.Number(price)})
		publishItem(t, s, item)
		ids = append(ids, item.ID)
	}
	for _, key := range []string{"price", "quantity"} {
		for _, sorts := range [][]SortSpec{{{Field: key}}, {{Field: key}, {Field: "$name", Direction: "desc"}}} {
			in := QueryRequest{Query: QuerySpec{Sort: sorts}, Limit: 1}
			got := []string{}
			for i := 0; ; i++ {
				if i > len(ids) {
					t.Fatal("pagination did not terminate")
				}
				page, err := s.Query(testContext, "reader", l.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Data {
					got = append(got, item.ID)
				}
				if page.NextCursor == "" {
					break
				}
				in.After = page.NextCursor
			}
			if !reflect.DeepEqual(got, ids) {
				t.Fatal("exact order lost", key, got, ids)
			}
		}
		page, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: key, Op: "gt", Value: json.RawMessage("9007199254740992")}}, IncludeTotal: true})
		if err != nil || page.Total == nil || *page.Total != 3 {
			t.Fatal("exact comparison", page, err)
		}
		browse, err := s.Browse(testContext, "alice", Browse{ParentID: l.ID, FilterField: key, FilterOp: "eq", FilterValue: "9007199254740993"})
		if err != nil || len(browse.Data) != 1 || browse.Data[0].ID != ids[7] {
			t.Fatal("browse equality", browse, err)
		}
		in := QueryRequest{Query: QuerySpec{GroupBy: key}, Limit: 1}
		values := []string{}
		for i := 0; ; i++ {
			if i > len(ids) {
				t.Fatal("group paging did not terminate")
			}
			groups, err := s.QueryGroups(testContext, "alice", l.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			for _, group := range groups.Data {
				values = append(values, group.Value.(json.Number).String())
			}
			if groups.NextCursor == "" {
				break
			}
			in.After = groups.NextCursor
		}
		if !reflect.DeepEqual(values, prices) {
			t.Fatal("exact group values", key, values, prices)
		}
	}
	folder := smartOK(t, s, SmartFolderInput{Name: "Amounts", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Mode: "query", Collections: []string{l.Name}, GroupBy: []SmartFolderGroupBy{{Field: "price"}}}})
	groups, err := s.SmartFolderGroups(testContext, "reader", folder.ID, SmartFolderQueryRequest{Limit: 1})
	if err != nil || len(groups.Data) != 1 || groups.Data[0].Value != json.Number(prices[0]) || groups.NextCursor == "" {
		t.Fatal("currency navigation groups", groups, err)
	}
	price := prices[7]
	entries, err := s.QuerySmartFolder(testContext, "reader", folder.ID, SmartFolderQueryRequest{Path: []*string{&price}})
	if err != nil || len(entries.Data) != 1 || entries.Data[0].Item.ID != ids[7] {
		t.Fatal("currency navigation path", entries, err)
	}
	item := create(t, s, l.ID, "item", "Default", nil)
	if item.Values["price"] != json.Number("12.3") {
		t.Fatal(item.Values)
	}
	options := field.Options
	options.CurrencyCode = "USD"
	options.Minimum, options.Maximum = ptr("0"), ptr("9007199254740992")
	_, err = s.UpdateField(testContext, "alice", l.ID, field.ID, latest(t, s, l.ID).Version, UpdateField{Options: &options})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Revision(testContext, "alice", item.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	defs, _, err := typedDefinitions(old.SchemaRevision.Definition, old.ContentTypeID)
	if err != nil || definitionMap(defs)["price"].Options.CurrencyCode != "EUR" {
		t.Fatal("history currency changed", defs, err)
	}
	if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Above max", Values: map[string]any{"price": json.Number("9007199254740993")}}); err == nil {
		t.Fatal("precise bound ignored")
	}
	other := create(t, s, w.ID, "list", "Unique amounts", nil)
	fieldOK(t, s, other.ID, CreateField{Key: "price", Label: "Price", Type: "currency", Indexed: true, Options: model.FieldOptions{CurrencyCode: "ZZZ", Unique: true}})
	create(t, s, other.ID, "item", "First", map[string]any{"price": json.Number("12.3")})
	if _, err = s.Create(testContext, "alice", other.ID, CreateResource{Kind: "item", Name: "Duplicate", Values: map[string]any{"price": json.Number("123e-1")}}); !errors.Is(err, ErrConflict) {
		t.Fatal("equivalent monetary value was not unique", err)
	}
}

func TestCurrencyDefinitionRejectsInvalidMetadata(t *testing.T) {
	for _, input := range []CreateField{
		{Type: "currency"}, {Type: "currency", Options: model.FieldOptions{CurrencyCode: "eur"}}, {Type: "currency", Options: model.FieldOptions{CurrencyCode: "EURO"}},
		{Type: "number", Options: model.FieldOptions{CurrencyCode: "EUR"}}, {Type: "currency", Options: model.FieldOptions{CurrencyCode: "EUR", Multiple: true}},
		{Type: "currency", Options: model.FieldOptions{CurrencyCode: "EUR", DefaultValue: json.RawMessage(`"12.3"`)}},
		{Type: "number", Options: model.FieldOptions{Maximum: ptr("79228162514264337593543950336")}},
	} {
		input.Key, input.Label = "price", "Price"
		if err := validateFieldDefinition(fieldFromInput(input)); err == nil {
			t.Fatal("invalid metadata accepted", input)
		}
	}
}
