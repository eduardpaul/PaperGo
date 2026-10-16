package sqlgen

import (
	"strconv"
	"strings"
)

// RebindPostgres rewrites '?' parameter markers in a SQL query to numbered
// PostgreSQL placeholders ('$1', '$2', ...), while safely ignoring '?' characters
// that occur inside string literals ('...') or double-quoted identifiers ("...").
func RebindPostgres(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 16)

	paramIdx := 1
	n := len(query)

	for i := 0; i < n; i++ {
		ch := query[i]
		switch ch {
		case '\'':
			// Single-quoted literal
			b.WriteByte('\'')
			i++
			for i < n {
				if query[i] == '\'' {
					b.WriteByte('\'')
					// Check for escaped quote ''
					if i+1 < n && query[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					break
				}
				b.WriteByte(query[i])
				i++
			}
		case '"':
			// Double-quoted identifier
			b.WriteByte('"')
			i++
			for i < n {
				if query[i] == '"' {
					b.WriteByte('"')
					if i+1 < n && query[i+1] == '"' {
						b.WriteByte('"')
						i += 2
						continue
					}
					break
				}
				b.WriteByte(query[i])
				i++
			}
		case '?':
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(paramIdx))
			paramIdx++
		default:
			b.WriteByte(ch)
		}
	}

	return b.String()
}
