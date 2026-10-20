// Package dialect writes the SQL expressions that differ between the
// engines, for statements DAL builds as text: access checks and content
// queries. Statements use ? placeholders; Rebind numbers them for
// PostgreSQL.
package dialect

import (
	"strconv"
	"strings"
)

// Dialect is the engine a statement runs on.
type Dialect struct{ Postgres bool }

var (
	SQLite   = Dialect{}
	Postgres = Dialect{Postgres: true}
)

// Elements is a table expression of the text elements of a JSON array,
// named alias with one column, value.
func (d Dialect) Elements(array, alias string) string {
	if d.Postgres {
		return `jsonb_array_elements_text(` + array + `) AS ` + alias + `(value)`
	}
	return `json_each(` + array + `) ` + alias
}

// ObjectValues is a table expression of the text values of a JSON object,
// named alias with a column value.
func (d Dialect) ObjectValues(object, alias string) string {
	if d.Postgres {
		return `jsonb_each_text(` + object + `) AS ` + alias
	}
	return `json_each(` + object + `) ` + alias
}

// OrderedJoin joins a table in the order written: SQLite's CROSS JOIN keeps
// its planner from reordering the tables, PostgreSQL plans an inner join.
func (d Dialect) OrderedJoin() string {
	if d.Postgres {
		return "JOIN"
	}
	return "CROSS JOIN"
}

// JSONArg is a placeholder for a JSON document passed as text.
func (d Dialect) JSONArg() string {
	if d.Postgres {
		return `CAST(? AS jsonb)`
	}
	return `?`
}

// Contains tests that text contains needle.
func (d Dialect) Contains(text, needle string) string {
	if d.Postgres {
		return `strpos(` + text + `, ` + needle + `) > 0`
	}
	return `instr(` + text + `, ` + needle + `) > 0`
}

// Text reads a JSON document column as its text.
func (d Dialect) Text(column string) string {
	if d.Postgres {
		return `CAST(` + column + ` AS TEXT)`
	}
	return column
}

// HasPrefix tests that text starts with prefix.
func (d Dialect) HasPrefix(text, prefix string) string {
	if d.Postgres {
		return `starts_with(` + text + `, ` + prefix + `)`
	}
	return `instr(` + text + `, ` + prefix + `) = 1`
}

// Fold lowers text for every script, as Go's strings.ToLower does.
func (d Dialect) Fold(text string) string {
	if d.Postgres {
		// pg_c_utf8 lowers every script whatever the database locale, and
		// orders by code point, as the C sort columns and SQLite do.
		return `lower(` + text + ` COLLATE "pg_c_utf8")`
	}
	return `unicode_lower(` + text + `)`
}

// Same tests that two values are equal or both NULL.
func (d Dialect) Same(a, b string) string {
	if d.Postgres {
		return a + ` IS NOT DISTINCT FROM ` + b
	}
	return a + ` IS ` + b
}

// AnyTrue is true when a boolean expression holds for any row of a group,
// and false for an empty group.
func (d Dialect) AnyTrue(condition string) string {
	if d.Postgres {
		return `coalesce(bool_or(` + condition + `), FALSE)`
	}
	return `coalesce(max(` + condition + `), 0)`
}

// JSONText reads a top-level key of a JSON object column as text.
func (d Dialect) JSONText(column, key string) string {
	if d.Postgres {
		return column + `->>'` + key + `'`
	}
	return `json_extract(` + column + `,'$.` + key + `')`
}

// Below tests that the materialized path child lies in the subtree rooted
// at path root, the root included. Paths end with a separator.
func (d Dialect) Below(child, root string) string {
	if d.Postgres {
		return `starts_with(` + child + `, ` + root + `)`
	}
	return child + `>=` + root + ` AND ` + child + `<substr(` + root + `,1,length(` + root + `)-1)||'0'`
}

// Prefix is the leading size characters of text; a timestamp is read as
// UTC text first.
func (d Dialect) Prefix(text string, size int, timestamp bool) string {
	if d.Postgres && timestamp {
		layout := "YYYY"
		if size == 7 {
			layout = "YYYY-MM"
		}
		return `to_char(` + text + ` AT TIME ZONE 'UTC', '` + layout + `')`
	}
	return `substr(` + text + `,1,` + strconv.Itoa(size) + `)`
}

// Rebind numbers the ? placeholders of query for PostgreSQL. Placeholders
// inside quoted literals and identifiers stay as they are.
func (d Dialect) Rebind(query string) string {
	if !d.Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	var quote byte
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '?':
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
