package zvecstore

import (
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"papergo/internal/search"
)

func words(text string) []string {
	text = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(text))
	return search.Tokenize(text)
}
func wordToken(prefix, word string) string { return prefix + hex.EncodeToString([]byte(word)) }

// Whitespace FTS indexes encoded exact tokens, language-specific stems and
// distinct prefix tokens. Native FTS therefore supports exact word prefixes,
// including one/two letters, without trigram substring false positives.
// Original token streams stay contiguous, preserving phrase positions.
func (s *Store) indexText(text string) string {
	tokens := words(text)
	out := make([]string, 0, len(tokens)*3)
	prefixes := map[string]bool{}
	for _, word := range tokens {
		out = append(out, wordToken("w", word))
		runes := []rune(word)
		for i := 1; i <= len(runes); i++ {
			prefix := string(runes[:i])
			if len(prefix) > search.MaxQueryBytes {
				break
			}
			prefixes[wordToken("p", prefix)] = true
		}
	}
	stemmed := strings.Split(s.stem.Text(strings.Join(tokens, " ")), "\n")
	for language, text := range stemmed {
		for _, word := range strings.Fields(text) {
			out = append(out, wordToken("s"+strconv.Itoa(language)+"z", word))
		}
	}
	keys := make([]string, 0, len(prefixes))
	for key := range prefixes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out = append(out, keys...)
	return strings.Join(out, " ")
}

func renderTerm(term search.Term, prefix string) string {
	tokens := make([]string, len(term.Tokens))
	for i, t := range term.Tokens {
		tokens[i] = wordToken(prefix, strings.Join(words(t), ""))
	}
	if term.Prefix {
		tokens[len(tokens)-1] = wordToken("p", strings.Join(words(term.Tokens[len(tokens)-1]), ""))
		return "(" + strings.Join(tokens, " AND ") + ")"
	}
	if len(tokens) > 1 {
		return `"` + strings.Join(tokens, " ") + `"`
	}
	return tokens[0]
}

func (s *Store) render(q *search.FullTextQuery, stems bool) string {
	if q == nil {
		return "pgallrows"
	}
	groups := []string{}
	for _, group := range q.Groups {
		terms := []string{}
		for _, term := range group {
			terms = append(terms, renderTerm(term, "w"))
			if !stems || term.Prefix {
				continue
			}
			for i := range s.stem.Languages() {
				variant := search.Term{}
				for _, word := range term.Tokens {
					variant.Tokens = append(variant.Tokens, s.stem.Stem(strings.Join(words(word), ""))[i])
				}
				terms = append(terms, renderTerm(variant, "s"+strconv.Itoa(i)+"z"))
			}
		}
		groups = append(groups, "("+strings.Join(terms, " OR ")+")")
	}
	return strings.Join(groups, " AND ")
}

func termIn(tokens []string, term search.Term) bool {
	if len(term.Tokens) == 0 {
		return false
	}
	for i := 0; i+len(term.Tokens) <= len(tokens); i++ {
		ok := true
		for j, word := range term.Tokens {
			word = strings.Join(words(word), "")
			if term.Prefix && j == len(term.Tokens)-1 {
				ok = ok && strings.HasPrefix(tokens[i+j], word)
			} else {
				ok = ok && tokens[i+j] == word
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (s *Store) termMatches(text string, term search.Term, stems bool) bool {
	tokens := words(text)
	if termIn(tokens, term) {
		return true
	}
	if !stems || term.Prefix {
		return false
	}
	for i, line := range strings.Split(s.stem.Text(strings.Join(tokens, " ")), "\n") {
		if i >= len(s.stem.Languages()) {
			break
		}
		variant := search.Term{}
		for _, word := range term.Tokens {
			variant.Tokens = append(variant.Tokens, s.stem.Stem(strings.Join(words(word), ""))[i])
		}
		if termIn(strings.Fields(line), variant) {
			return true
		}
	}
	return false
}

func (s *Store) matches(text string, q *search.FullTextQuery, stems bool) bool {
	if q == nil {
		return true
	}
	for _, group := range q.Groups {
		found := false
		for _, term := range group {
			found = found || s.termMatches(text, term, stems)
		}
		if !found {
			return false
		}
	}
	for _, term := range q.Excluded {
		if s.termMatches(text, term, stems) {
			return false
		}
	}
	return true
}
