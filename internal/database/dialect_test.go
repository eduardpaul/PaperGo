package database

import (
	"testing"
)

func TestDialectExpressions(t *testing.T) {
	sql := SQLite
	pg := NewPostgresDialect(nil)

	// JSONExtract
	if got := sql.JSONExtract("opts", "term_set_id"); got != "json_extract(opts, '$.term_set_id')" {
		t.Fatalf("unexpected sqlite JSONExtract: %s", got)
	}
	if got := pg.JSONExtract("opts", "term_set_id"); got != "(opts->>'term_set_id')" {
		t.Fatalf("unexpected postgres JSONExtract: %s", got)
	}

	// JSONEach
	if got := sql.JSONEach("tags"); got != "json_each(tags)" {
		t.Fatalf("unexpected sqlite JSONEach: %s", got)
	}
	if got := pg.JSONEach("tags"); got != "jsonb_array_elements_text((tags)::jsonb)" {
		t.Fatalf("unexpected postgres JSONEach: %s", got)
	}

	// JSONType
	if got := sql.JSONType("metadata"); got != "json_type(metadata)" {
		t.Fatalf("unexpected sqlite JSONType: %s", got)
	}
	if got := pg.JSONType("metadata"); got != "jsonb_typeof((metadata)::jsonb)" {
		t.Fatalf("unexpected postgres JSONType: %s", got)
	}

	// Instr
	if got := sql.Instr("name", "?"); got != "(instr(name, ?) > 0)" {
		t.Fatalf("unexpected sqlite Instr: %s", got)
	}
	if got := pg.Instr("name", "$1"); got != "(STRPOS(name, $1) > 0)" {
		t.Fatalf("unexpected postgres Instr: %s", got)
	}

	// Lower
	if got := sql.Lower("name"); got != "unicode_lower(name)" {
		t.Fatalf("unexpected sqlite Lower: %s", got)
	}
	if got := pg.Lower("name"); got != "LOWER(name)" {
		t.Fatalf("unexpected postgres Lower: %s", got)
	}

	// CollateNoCase
	if got := sql.CollateNoCase("name"); got != "name COLLATE NOCASE" {
		t.Fatalf("unexpected sqlite CollateNoCase: %s", got)
	}
	if got := pg.CollateNoCase("name"); got != "LOWER(name)" {
		t.Fatalf("unexpected postgres CollateNoCase: %s", got)
	}

	// BooleanLiteral
	if got := sql.BooleanLiteral(true); got != "1" {
		t.Fatalf("unexpected sqlite BooleanLiteral: %s", got)
	}
	if got := pg.BooleanLiteral(true); got != "TRUE" {
		t.Fatalf("unexpected postgres BooleanLiteral: %s", got)
	}

	// Rebind
	query := "SELECT * FROM t WHERE a = ? AND b = ?"
	if got := sql.Rebind(query); got != query {
		t.Fatalf("sqlite rebind should be identity, got: %s", got)
	}
	if got := pg.Rebind(query); got != "SELECT * FROM t WHERE a = $1 AND b = $2" {
		t.Fatalf("postgres rebind failed, got: %s", got)
	}
}
