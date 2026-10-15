package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/blevesearch/snowballstem"
	"github.com/blevesearch/snowballstem/arabic"
	"github.com/blevesearch/snowballstem/danish"
	"github.com/blevesearch/snowballstem/dutch"
	"github.com/blevesearch/snowballstem/english"
	"github.com/blevesearch/snowballstem/finnish"
	"github.com/blevesearch/snowballstem/french"
	"github.com/blevesearch/snowballstem/german"
	"github.com/blevesearch/snowballstem/hungarian"
	"github.com/blevesearch/snowballstem/irish"
	"github.com/blevesearch/snowballstem/italian"
	"github.com/blevesearch/snowballstem/norwegian"
	"github.com/blevesearch/snowballstem/portuguese"
	"github.com/blevesearch/snowballstem/romanian"
	"github.com/blevesearch/snowballstem/russian"
	"github.com/blevesearch/snowballstem/spanish"
	"github.com/blevesearch/snowballstem/swedish"
	"github.com/blevesearch/snowballstem/tamil"
	"github.com/blevesearch/snowballstem/turkish"
)

// stemmers are the Snowball stemmers, by language name.
var stemmers = map[string]func(*snowballstem.Env) bool{
	"arabic": arabic.Stem, "danish": danish.Stem, "dutch": dutch.Stem, "english": english.Stem,
	"finnish": finnish.Stem, "french": french.Stem, "german": german.Stem, "hungarian": hungarian.Stem,
	"irish": irish.Stem, "italian": italian.Stem, "norwegian": norwegian.Stem, "portuguese": portuguese.Stem,
	"romanian": romanian.Stem, "russian": russian.Stem, "spanish": spanish.Stem, "swedish": swedish.Stem,
	"tamil": tamil.Stem, "turkish": turkish.Stem,
}

// Languages lists the languages search can stem.
func Languages() []string {
	out := make([]string, 0, len(stemmers))
	for name := range stemmers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Stemmer reduces words to their stems in a fixed list of languages, so
// "invoices" finds "invoice". Stores index the stems of every language
// beside the words, and a query term matches its word or any of its stems.
type Stemmer struct {
	languages []string
}

// NewStemmer stems in languages, Snowball names such as english or german;
// none stems nothing.
func NewStemmer(languages []string) (*Stemmer, error) {
	s := &Stemmer{}
	for _, l := range languages {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if stemmers[l] == nil {
			return nil, fmt.Errorf("search cannot stem %q; languages: %s", l, strings.Join(Languages(), ", "))
		}
		if !contains(s.languages, l) {
			s.languages = append(s.languages, l)
		}
	}
	return s, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Languages are the stemmer's languages, in order.
func (s *Stemmer) Languages() []string { return append([]string(nil), s.languages...) }

// Stem returns the stem of a token (lower-case, as Tokenize makes it) in
// each language, in the stemmer's order.
func (s *Stemmer) Stem(token string) []string {
	out := make([]string, len(s.languages))
	env := snowballstem.NewEnv(token)
	for i, l := range s.languages {
		env.SetCurrent(token)
		stemmers[l](env)
		out[i] = env.Current()
	}
	return out
}

// Text returns the words of text as their stems, the whole text once per
// language, so phrases of stems keep their order.
func (s *Stemmer) Text(text string) string {
	if len(s.languages) == 0 {
		return ""
	}
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	env := snowballstem.NewEnv("")
	for i, l := range s.languages {
		if i > 0 {
			b.WriteString("\n")
		}
		for j, t := range tokens {
			if j > 0 {
				b.WriteByte(' ')
			}
			env.SetCurrent(t)
			stemmers[l](env)
			b.WriteString(env.Current())
		}
	}
	return b.String()
}
