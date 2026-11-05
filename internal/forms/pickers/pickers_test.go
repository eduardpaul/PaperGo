package pickers

import (
	"encoding/json"
	"strings"
	"testing"
)

func parse(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Map replaces exactly the IDs Read reports, whatever the properties are
// called, an empty string in an array among them, and leaves every other
// value as it is.
func TestMapReplacesOnlyPickedIDs(t *testing.T) {
	s := parse(t, `{"type":"object","properties":{
		"search":{"type":"string","x-papergo":{"kind":"collection"}},
		"lists":{"type":"array","items":{"type":"string"},"x-papergo":{"kind":"collection"}},
		"rows":{"type":"array","items":{"type":"object","properties":{"item":{"type":"string","x-papergo":{"kind":"item"}},"note":{"type":"string"}}}},
		"note":{"type":"string"}},
		"additionalProperties":{"type":"string","x-papergo":{"kind":"terms"}}}`)
	values := parse(t, `{"search":"a","lists":["b","","c"],"rows":[{"item":"d","note":"a"}],"note":"a","extra":"e"}`)
	got := Map(s, values, func(kind, id string) string { return kind + ":" + strings.ToUpper(id) })
	raw, _ := json.Marshal(got)
	want := `{"extra":"terms:E","lists":["collection:B","collection:","collection:C"],"note":"a","rows":[{"item":"item:D","note":"a"}],"search":"collection:A"}`
	if string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
	var ids []string
	for _, p := range Read(s, values, "v") {
		ids = append(ids, p.Path+"="+strings.Join(p.IDs, ","))
	}
	if strings.Join(ids, " ") != "v.lists=b,,c v.rows[0].item=d v.search=a v.extra=e" {
		t.Fatal(ids)
	}
}
