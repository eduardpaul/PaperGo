package decision

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestTypedQuestionsAndAnswers(t *testing.T) {
	q := []Question{{Key: "route", Instructions: "Route?", Options: []Option{{"a", "Billing"}, {"b", "Technical"}}}, {Key: "valid", Type: "noul", Instructions: "Valid?", Options: []Option{{"false", "No"}, {"true", "Yes"}}}, {Key: "priority", Type: "score", Instructions: "Priority?", Options: []Option{{"low", "Low"}, {"high", "High"}}}}
	r, err := Requests(q, "input", 32)
	if err != nil {
		t.Fatal(err)
	}
	if r[0].Options[0] != "Billing" || r[1].Options[1] != "true: Yes" || r[2].Options[0] != "level 0: Low" {
		t.Fatal(r)
	}
	a, err := Answers(q, []Result{{Probabilities: []float64{.5, .5}}, {Probabilities: []float64{.3, .7}}, {Probabilities: []float64{.25, .75}}})
	if err != nil {
		t.Fatal(err)
	}
	if a["route"].Selected != "a" || *a["valid"].Noul != .7 || *a["priority"].Score != .75 || a["route"].Score != nil {
		t.Fatal(a)
	}
	for _, p := range [][]float64{{1}, {math.NaN(), 0}, {math.Inf(1), 0}, {.1, .1}, {-.1, 1.1}} {
		if CheckResults(r[:1], []Result{{Probabilities: p}}) == nil {
			t.Fatal(p)
		}
	}
	q[1].Options[0].Key = "no"
	if _, err = Requests(q, "x", 32); err == nil {
		t.Fatal("accepted invalid noul")
	}
}
func TestCanonicalPayload(t *testing.T) {
	var v map[string]any
	d := json.NewDecoder(strings.NewReader(`{"z":false,"n":9007199254740993,"a":[0,"<&>"]}`))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	b, err := Payload(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":[0,"<&>"],"n":9007199254740993,"z":false}` {
		t.Fatal(string(b))
	}
	for i := 0; i < 10; i++ {
		v = map[string]any{"nested": v}
	}
	if _, err = Payload(v); err == nil {
		t.Fatal("accepted deep payload")
	}
}

func TestCanonicalUnicodeAndLiteralEscapes(t *testing.T) {
	value := map[string]any{"unicode": "line\u2028next\u2029end", "literal": `\u2028 \\u2029`}
	raw, err := Canonical(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["unicode"] != value["unicode"] || decoded["literal"] != value["literal"] || !strings.Contains(string(raw), "line\u2028next\u2029end") {
		t.Fatal(string(raw))
	}
	for _, text := range []string{"bad\x00", string([]byte{255})} {
		if _, err = Payload(map[string]any{"text": text}); err == nil {
			t.Fatal("invalid JSON string accepted")
		}
	}
}
