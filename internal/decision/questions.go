package decision

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const Rendering = "typed-options-v1"
const JSONSerialization = "canonical-json-v1"
const TextSerialization = "text-v1"
const DocumentContextSerialization = "document-context-json-v1"

var questionKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var optionKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Option struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}
type Question struct {
	Key          string   `json:"key"`
	Type         string   `json:"type,omitempty"`
	Instructions string   `json:"instructions"`
	Options      []Option `json:"options"`
}
type Answer struct {
	Type          string             `json:"type"`
	Selected      string             `json:"selected"`
	Probability   float64            `json:"probability"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          *float64           `json:"noul,omitempty"`
	Score         *float64           `json:"score,omitempty"`
}

// Requests validates ordered typed questions and renders prefixes exactly once.
func Requests(questions []Question, state string, limit int) ([]Request, error) {
	maxQuestions, maxOptions := limit, 256
	if limit < 1 || limit > 50 {
		return nil, errors.New("question limit must be 1–50")
	}
	if len(questions) < 1 || len(questions) > maxQuestions {
		return nil, fmt.Errorf("questions needs 1–%d entries", maxQuestions)
	}
	raw, err := json.Marshal(questions)
	if err != nil {
		return nil, err
	}
	if len(raw) > 24*1024 {
		return nil, errors.New("questions exceeds 24 KiB")
	}
	seen := map[string]bool{}
	total := 0
	requests := make([]Request, len(questions))
	for i, q := range questions {
		if !questionKey.MatchString(q.Key) || seen[q.Key] {
			return nil, fmt.Errorf("question %q: keys must be unique lowercase identifiers", q.Key)
		}
		seen[q.Key] = true
		kind := q.Type
		if kind == "" {
			kind = "choice"
		}
		r := Request{State: state, Question: q.Instructions, Type: kind}
		keys, texts := map[string]bool{}, map[string]bool{}
		for j, o := range q.Options {
			if !optionKey.MatchString(o.Key) || keys[o.Key] {
				return nil, fmt.Errorf("question %s: invalid or duplicate option key %q", q.Key, o.Key)
			}
			keys[o.Key] = true
			text := o.Text
			if kind == "noul" {
				if len(q.Options) != 2 || (j == 0 && o.Key != "false") || (j == 1 && o.Key != "true") {
					return nil, fmt.Errorf("question %s: noul requires false then true", q.Key)
				}
				text = o.Key + ": " + text
			}
			if kind == "score" {
				text = fmt.Sprintf("level %d: %s", j, text)
			}
			if strings.TrimSpace(o.Text) == "" || texts[text] {
				return nil, fmt.Errorf("question %s: empty or duplicate rendered option %q", q.Key, o.Key)
			}
			texts[text] = true
			r.Options = append(r.Options, text)
		}
		if err := validate(r); err != nil {
			return nil, fmt.Errorf("question %s: %w", q.Key, err)
		}
		total += len(q.Options)
		requests[i] = r
	}
	if total > maxOptions {
		return nil, fmt.Errorf("questions exceeds %d option entries", maxOptions)
	}
	return requests, nil
}

// CheckResults validates engine and cached distributions. Selection and typed values
// are derived from probabilities so alternate implementations cannot supply mismatched indexes.
func CheckResults(requests []Request, results []Result) error {
	if len(results) != len(requests) {
		return errors.New("decision result count mismatch")
	}
	for i, r := range results {
		if len(r.Probabilities) != len(requests[i].Options) {
			return fmt.Errorf("question %d: probability count mismatch", i)
		}
		sum := 0.0
		for _, p := range r.Probabilities {
			if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
				return fmt.Errorf("question %d: invalid probability", i)
			}
			sum += p
		}
		if math.Abs(sum-1) > 1e-6 {
			return fmt.Errorf("question %d: invalid probability distribution", i)
		}
	}
	return nil
}

func Answers(questions []Question, results []Result) (map[string]Answer, error) {
	requests, err := Requests(questions, "validation", 32)
	if err != nil {
		return nil, err
	}
	if err = CheckResults(requests, results); err != nil {
		return nil, err
	}
	out := map[string]Answer{}
	for i, q := range questions {
		a := Answer{Type: requests[i].Type, Probabilities: map[string]float64{}}
		winner := 0
		score := 0.0
		for j, p := range results[i].Probabilities {
			a.Probabilities[q.Options[j].Key] = p
			score += float64(j) * p
			if p > results[i].Probabilities[winner] {
				winner = j
			}
		}
		a.Selected = q.Options[winner].Key
		a.Probability = results[i].Probabilities[winner]
		if a.Type == "noul" {
			p := results[i].Probabilities[1]
			a.Noul = &p
		}
		if a.Type == "score" {
			a.Score = &score
		}
		out[q.Key] = a
	}
	return out, nil
}

// Canonical uses encoding/json's sorted object keys, preserved json.Number values,
// array ordering and UTF-8 encoding without HTML escaping. No float conversion occurs.
func Canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	// JSON permits literal Unicode line separators; retain their UTF-8 bytes
	// consistently with the official reference serializer. Respect escaped
	// backslashes so a literal "\\u2028" remains literal data.
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			out = append(out, raw[i])
			i++
			continue
		}
		end := i
		for end < len(raw) && raw[end] == '\\' {
			end++
		}
		count := end - i
		if count%2 == 1 && end+5 <= len(raw) && (string(raw[end:end+5]) == "u2028" || string(raw[end:end+5]) == "u2029") {
			out = append(out, raw[i:end-1]...)
			if raw[end+4] == '8' {
				out = append(out, []byte("\u2028")...)
			} else {
				out = append(out, []byte("\u2029")...)
			}
			i = end + 5
		} else {
			out = append(out, raw[i:end]...)
			i = end
		}
	}
	return out, nil
}

func Payload(v any) ([]byte, error) {
	var depth func(any, int) error
	depth = func(v any, n int) error {
		if n > 8 {
			return errors.New("decision payload nests deeper than eight levels")
		}
		switch t := v.(type) {
		case string:
			if !utf8.ValidString(t) || strings.ContainsRune(t, 0) {
				return errors.New("decision JSON strings must be UTF-8 without NUL")
			}
		case map[string]any:
			for key, c := range t {
				if !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
					return errors.New("decision JSON keys must be UTF-8 without NUL")
				}
				if err := depth(c, n+1); err != nil {
					return err
				}
			}
		case []any:
			for _, c := range t {
				if err := depth(c, n+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := depth(v, 0); err != nil {
		return nil, err
	}
	raw, err := Canonical(v)
	if err != nil {
		return nil, err
	}
	if len(raw) > 16*1024 {
		return nil, errors.New("decision payload exceeds 16 KiB")
	}
	return raw, nil
}
