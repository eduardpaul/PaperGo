package connections

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// MaxQueryBytes bounds the query string a call passes on.
const MaxQueryBytes = 2048

// checkPattern says why pattern is not an HTTP channel's path pattern: a
// path from the target's base, whose segments are literal, * (one segment)
// or, last, ** (one or more).
func checkPattern(pattern string) error {
	if !strings.HasPrefix(pattern, "/") || len(pattern) > 512 || !utf8.ValidString(pattern) {
		return errors.New("a path pattern starts with / and has at most 512 bytes")
	}
	if pattern == "/" {
		return nil
	}
	segments := strings.Split(pattern[1:], "/")
	for i, s := range segments {
		switch {
		case s == "":
			return errors.New("a path pattern has no empty segments")
		case s == "**" && i != len(segments)-1:
			return errors.New("** stands for the last segments, as in /heating/**")
		case s == "." || s == ".." || strings.ContainsAny(s, "?#\\%") || s != "*" && s != "**" && strings.Contains(s, "*"):
			return errors.New("a path pattern holds plain segments, * or a last **")
		}
	}
	return nil
}

// patternSegments are pattern's segments; the root has none.
func patternSegments(pattern string) []string {
	if pattern == "/" {
		return nil
	}
	return strings.Split(pattern[1:], "/")
}

// Path is a request's path, checked: its decoded segments, which channels
// match, and its text as it is sent, with its query.
type Path struct {
	Segments []string
	// Raw is the escaped path and, after ?, the query, as the caller gave
	// them.
	Raw string
}

// ParsePath checks what a caller asks for: a path and query, never a URL.
// Dot segments, empty segments, encoded slashes and backslashes are
// refused, so a path cannot leave the channel that matched it.
func ParsePath(raw string) (Path, error) {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "#\\ \t\r\n") || !utf8.ValidString(raw) {
		return Path{}, errors.New("a path starts with a single / and holds no fragment, backslash or spaces")
	}
	path, query, _ := strings.Cut(raw, "?")
	if len(query) > MaxQueryBytes {
		return Path{}, errors.New("a query has at most 2048 bytes")
	}
	if len(path) > 2048 {
		return Path{}, errors.New("a path has at most 2048 bytes")
	}
	lower := strings.ToLower(path)
	if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
		return Path{}, errors.New("a path holds no encoded slash, backslash or NUL")
	}
	out := Path{Raw: raw}
	if path == "/" {
		return out, nil
	}
	for _, s := range strings.Split(path[1:], "/") {
		decoded, err := url.PathUnescape(s)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." {
			return Path{}, errors.New("a path has no empty, . or .. segments")
		}
		out.Segments = append(out.Segments, decoded)
	}
	if _, err := url.ParseQuery(query); query != "" && err != nil {
		return Path{}, errors.New("the query is not valid")
	}
	return out, nil
}

// pathMatches reports whether pattern matches a path's segments.
func pathMatches(pattern string, segments []string) bool {
	p := patternSegments(pattern)
	for i, s := range p {
		if s == "**" {
			return len(segments) > i
		}
		if i >= len(segments) || s != "*" && s != segments[i] {
			return false
		}
	}
	return len(p) == len(segments)
}

// patternsOverlap reports whether some path matches both patterns.
func patternsOverlap(a, b string) bool {
	x, y := patternSegments(a), patternSegments(b)
	for i := 0; ; i++ {
		if i < len(x) && x[i] == "**" {
			return len(y) > i
		}
		if i < len(y) && y[i] == "**" {
			return len(x) > i
		}
		if i == len(x) || i == len(y) {
			return i == len(x) && i == len(y)
		}
		if x[i] != "*" && y[i] != "*" && x[i] != y[i] {
			return false
		}
	}
}
