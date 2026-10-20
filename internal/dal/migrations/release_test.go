package migrations

import (
	"encoding/json"
	"testing"
)

func TestBundleRejectsInvalidHistory(t *testing.T) {
	a := Release{Version: "v1.0.0-rc.2", Schema: `CREATE TABLE t(id INTEGER);`}
	a.Hash = Hash(a.Schema)
	b := a
	b.Version = "v1.0.0"
	valid := Bundle{Format: 1, Releases: []Release{a, b}}
	for _, test := range []struct {
		name   string
		mutate func(*Bundle)
	}{
		{"duplicate", func(b *Bundle) { b.Releases[1].Version = b.Releases[0].Version }},
		{"reordered", func(b *Bundle) { b.Releases[0], b.Releases[1] = b.Releases[1], b.Releases[0] }},
		{"hash", func(b *Bundle) { b.Releases[0].Hash = "wrong" }},
		{"invalid tag", func(b *Bundle) { b.Releases[1].Version = "v01.0.0" }},
		{"overflow", func(b *Bundle) { b.Releases[1].Version = "v999999999999999999999999.0.0" }},
		{"first upgrade", func(b *Bundle) { b.Releases[0].Statements = []string{"SELECT 1"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := Bundle{Format: valid.Format, Releases: append([]Release{}, valid.Releases...)}
			test.mutate(&bundle)
			raw, _ := json.Marshal(bundle)
			if _, err := DecodeBundle(raw); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
	raw, _ := json.Marshal(valid)
	if _, err := DecodeBundle(raw); err != nil {
		t.Fatal(err)
	}
}
