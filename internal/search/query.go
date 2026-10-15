package search

import (
	"errors"
	"strings"
	"unicode"
)

// Term is one search term: a token, or a phrase of several; with Prefix the
// last token matches as a prefix.
type Term struct {
	Tokens []string
	Prefix bool
}

// FullTextQuery is a parsed search: every group must match (AND), a group
// matches when any of its terms does (OR), and no excluded term may match.
// Tokens are lower-case runs of letters and digits, so stores render them
// without escaping.
type FullTextQuery struct {
	Groups   [][]Term
	Excluded []Term
}

// MaxTerms bounds the terms of a query.
const MaxTerms = 32

// MaxQueryBytes bounds the text of a query.
const MaxQueryBytes = 256

// ErrNoTerms is returned when a query has nothing positive to search for.
var ErrNoTerms = errors.New("enter at least one word to search for (exclusions alone are not enough)")

// Parse reads search syntax: words (all must match), "exact phrases",
// a OR b, -word or NOT word, and prefix*.
func Parse(text string) (*FullTextQuery, error) {
	if len(text) > MaxQueryBytes {
		return nil, errors.New("a search is at most 256 bytes")
	}
	q := &FullTextQuery{}
	joinOr, negateNext, count := false, false, 0
	for _, part := range split(text) {
		if !part.quoted && part.value == "OR" {
			joinOr = len(q.Groups) > 0
			continue
		}
		if !part.quoted && part.value == "NOT" {
			negateNext = true
			continue
		}
		value, negate := part.value, negateNext
		negateNext = false
		if !part.quoted && strings.HasPrefix(value, "-") {
			negate, value = true, value[1:]
		}
		tokens := Tokenize(value)
		if count++; len(tokens) == 0 || count > MaxTerms {
			joinOr = false
			continue
		}
		term := Term{Tokens: tokens, Prefix: strings.HasSuffix(value, "*")}
		switch {
		case negate:
			q.Excluded = append(q.Excluded, term)
		case joinOr:
			q.Groups[len(q.Groups)-1] = append(q.Groups[len(q.Groups)-1], term)
		default:
			q.Groups = append(q.Groups, []Term{term})
		}
		joinOr = false
	}
	if len(q.Groups) == 0 {
		return nil, ErrNoTerms
	}
	return q, nil
}

// Literal reads text as one phrase whose last word matches as a prefix, as
// people type into a list's filter box: nothing in it is query syntax.
func Literal(text string) *FullTextQuery {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	if len(tokens) > MaxTerms {
		tokens = tokens[:MaxTerms]
	}
	return &FullTextQuery{Groups: [][]Term{{{Tokens: tokens, Prefix: true}}}}
}

// Positive lists the tokens that must or may match, for highlighting.
func (q *FullTextQuery) Positive() []string {
	var out []string
	for _, group := range q.Groups {
		for _, term := range group {
			out = append(out, term.Tokens...)
		}
	}
	return out
}

// Tokenize splits text into lower-case runs of letters and digits, as the
// stores' tokenizers do.
func Tokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(unicode.ToLower(r))
			continue
		}
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

type queryPart struct {
	value  string
	quoted bool
}

// split cuts text into words and "quoted phrases"; a * right after a closing
// quote makes the phrase a prefix.
func split(text string) []queryPart {
	var parts []queryPart
	runes := []rune(text)
	for i := 0; i < len(runes); {
		switch {
		case unicode.IsSpace(runes[i]):
			i++
		case runes[i] == '"':
			end := i + 1
			for end < len(runes) && runes[end] != '"' {
				end++
			}
			phrase := string(runes[i+1 : min(end, len(runes))])
			next := end + 1
			if next < len(runes) && runes[next] == '*' {
				phrase += "*"
				next++
			}
			parts = append(parts, queryPart{value: phrase, quoted: true})
			i = next
		default:
			start := i
			for i < len(runes) && !unicode.IsSpace(runes[i]) && runes[i] != '"' {
				i++
			}
			parts = append(parts, queryPart{value: string(runes[start:i])})
		}
	}
	return parts
}

// SemanticText is a search as meaning, for its embedding: operators, quotes,
// prefix stars and excluded words removed, as PaperDotNet's SemanticText.
// Text without any other word is kept as it is.
func SemanticText(q string) string {
	var words []string
	skipNext := false
	for _, word := range strings.Fields(q) {
		switch {
		case skipNext:
			skipNext = false
		case word == "OR":
		case word == "NOT":
			skipNext = true
		case strings.HasPrefix(word, "-"):
		default:
			if w := strings.Trim(word, `"*`); w != "" {
				words = append(words, w)
			}
		}
	}
	if len(words) == 0 {
		return strings.TrimSpace(q)
	}
	return strings.Join(words, " ")
}
