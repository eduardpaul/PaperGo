package pgstore

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"papergo/internal/search"
)

// The words of a text are tokenized, folded and stemmed in Go, as the SQLite
// store's FTS5 tokenizer (unicode61, remove_diacritics 2) and its stem
// columns do, and stored as a tsvector of those exact lexemes: PostgreSQL
// parses nothing, so stock PostgreSQL needs no text search configuration or
// extension, and both stores match the same words.
//
// A word is its folded token. A stem is its folded Snowball stem with a
// leading stemMark, at the word's position: stems never match a word or a
// prefix of one, and phrases of stems keep their order.
const stemMark = "~"

// Lexeme weights. ts_rank weighs them {D, C, B, A} as rankWeights says: the
// name and keywords (heading) rank ten times higher than the body, and words
// that match as they are rank above their stems, as the SQLite store's bm25
// column weights.
const (
	headingWord = 'A'
	headingStem = 'B'
	bodyWord    = 'C'
	bodyStem    = 'D'
)

// rankWeights are ts_rank's weights of D, C, B and A.
const rankWeights = "'{0.05, 0.1, 0.5, 1.0}'::float4[]"

// tsvector bounds: a position is at most 16383, a lexeme keeps at most 256
// positions, and a tsvector is less than 1 MiB. Text past the size budget is
// left out of the index.
const (
	maxPosition  = 16383
	maxPositions = 256
	sizeBudget   = 900 << 10
)

// fold removes the diacritics of Latin letters, as FTS5's remove_diacritics
// option: é matches e, while letters of other scripts stay as they are.
func fold(token string) string {
	latin := false
	for _, r := range token {
		if r >= 0x00C0 && r < 0x0250 || r >= 0x1E00 && r < 0x1F00 {
			latin = true
			break
		}
	}
	if !latin {
		return token
	}
	var b strings.Builder
	for _, r := range norm.NFD.String(token) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return norm.NFC.String(b.String())
}

// vector builds a tsvector literal.
type vector struct {
	stem      *search.Stemmer
	positions map[string][]uint16
	weights   map[string][]byte
	next      int
	size      int
}

func newVector(stem *search.Stemmer) *vector {
	return &vector{stem: stem, positions: map[string][]uint16{}, weights: map[string][]byte{}, next: 1}
}

// add indexes text with the weights of its words and of their stems. A
// later text starts past a gap, so phrases never span two texts.
func (v *vector) add(text string, word, stem byte) {
	for _, token := range search.Tokenize(text) {
		position := uint16(min(v.next, maxPosition))
		v.next++
		if !v.put(fold(token), position, word) {
			return
		}
		if v.stem == nil {
			continue
		}
		for _, s := range v.stem.Stem(token) {
			if !v.put(stemMark+fold(s), position, stem) {
				return
			}
		}
	}
	v.next++
}

// put records a lexeme at a position, reporting false once the vector is
// full.
func (v *vector) put(lexeme string, position uint16, weight byte) bool {
	at, ok := v.positions[lexeme]
	if ok && len(at) > 0 && at[len(at)-1] == position {
		// A word and its stem, or stems of several languages, alike.
		return true
	}
	cost := 2
	if !ok {
		cost += len(lexeme) + 8
	}
	if v.size+cost > sizeBudget {
		return false
	}
	if len(at) >= maxPositions {
		return true
	}
	v.size += cost
	v.positions[lexeme] = append(at, position)
	v.weights[lexeme] = append(v.weights[lexeme], weight)
	return true
}

// String is the tsvector literal: lexemes in single quotes with their
// weighted positions.
func (v *vector) String() string {
	lexemes := make([]string, 0, len(v.positions))
	for l := range v.positions {
		lexemes = append(lexemes, l)
	}
	slices.Sort(lexemes)
	var b strings.Builder
	for i, l := range lexemes {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quote(l))
		for j, p := range v.positions[l] {
			if j == 0 {
				b.WriteByte(':')
			} else {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(int(p)))
			b.WriteByte(v.weights[l][j])
		}
	}
	return b.String()
}

// quote quotes a lexeme for tsvector and tsquery input. Tokens are letters
// and digits, so only a stray quote or backslash would need escaping.
func quote(lexeme string) string {
	lexeme = strings.ReplaceAll(lexeme, `\`, `\\`)
	return "'" + strings.ReplaceAll(lexeme, "'", "''") + "'"
}

// documentWords is the tsvector of a document's metadata: the heading (name
// and keywords) and the body.
func documentWords(stem *search.Stemmer, name, keywords, body string) string {
	v := newVector(stem)
	v.add(name+"\n"+keywords, headingWord, headingStem)
	v.add(body, bodyWord, bodyStem)
	return v.String()
}

// chunkWords is the tsvector of a chunk: its text and enrichment, the
// words weighted above their stems.
func chunkWords(stem *search.Stemmer, text string) string {
	v := newVector(stem)
	v.add(text, headingWord, headingStem)
	return v.String()
}

// tsquery renders a group of terms (any may match) as a tsquery literal.
// With a stemmer, a whole word or phrase also matches its stems in any of
// the stemmer's languages; prefixes match words only.
func tsquery(terms []search.Term, stem *search.Stemmer) string {
	alternatives := make([]string, 0, len(terms))
	for _, t := range terms {
		alternatives = append(alternatives, term(t, stem))
	}
	return strings.Join(alternatives, " | ")
}

func term(t search.Term, stem *search.Stemmer) string {
	words := make([]string, len(t.Tokens))
	for i, token := range t.Tokens {
		words[i] = quote(fold(token))
	}
	if t.Prefix {
		words[len(words)-1] += ":*"
	}
	exact := "(" + strings.Join(words, " <-> ") + ")"
	if t.Prefix || stem == nil || len(stem.Languages()) == 0 {
		return exact
	}
	// One phrase of stems per language, each once.
	phrases := make([][]string, len(stem.Languages()))
	for _, token := range t.Tokens {
		for l, s := range stem.Stem(token) {
			phrases[l] = append(phrases[l], quote(stemMark+fold(s)))
		}
	}
	seen := map[string]bool{}
	alternatives := []string{exact}
	for _, p := range phrases {
		phrase := "(" + strings.Join(p, " <-> ") + ")"
		if !seen[phrase] {
			seen[phrase] = true
			alternatives = append(alternatives, phrase)
		}
	}
	return "(" + strings.Join(alternatives, " | ") + ")"
}

// whole renders a parsed query as one tsquery literal: every group, and
// none of the excluded terms.
func whole(q *search.FullTextQuery, stem *search.Stemmer) string {
	groups := make([]string, len(q.Groups))
	for i, g := range q.Groups {
		groups[i] = "(" + tsquery(g, stem) + ")"
	}
	text := strings.Join(groups, " & ")
	for _, t := range q.Excluded {
		text += " & !" + term(t, stem)
	}
	return text
}

// clean makes text storable in PostgreSQL, which refuses NUL characters and
// invalid UTF-8 in text columns.
func clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.ReplaceAll(s, "\x00", "")
}
