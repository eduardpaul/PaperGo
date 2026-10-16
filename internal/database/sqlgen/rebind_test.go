package sqlgen

import (
	"testing"
)

func TestRebindPostgres(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no placeholders",
			input:    "SELECT 1 FROM users WHERE active = true",
			expected: "SELECT 1 FROM users WHERE active = true",
		},
		{
			name:     "simple placeholders",
			input:    "SELECT id, name FROM users WHERE id = ? AND status = ? ORDER BY id",
			expected: "SELECT id, name FROM users WHERE id = $1 AND status = $2 ORDER BY id",
		},
		{
			name:     "string with question mark",
			input:    "SELECT * FROM items WHERE title = 'What?' AND id = ?",
			expected: "SELECT * FROM items WHERE title = 'What?' AND id = $1",
		},
		{
			name:     "string with escaped quote and question mark",
			input:    "SELECT * FROM items WHERE title = 'It''s ?' AND id = ?",
			expected: "SELECT * FROM items WHERE title = 'It''s ?' AND id = $1",
		},
		{
			name:     "quoted identifier with question mark",
			input:    `SELECT "col?" FROM items WHERE id = ?`,
			expected: `SELECT "col?" FROM items WHERE id = $1`,
		},
		{
			name:     "many placeholders",
			input:    "INSERT INTO t(a,b,c,d,e,f,g,h,i,j,k) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			expected: "INSERT INTO t(a,b,c,d,e,f,g,h,i,j,k) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RebindPostgres(tt.input)
			if got != tt.expected {
				t.Fatalf("expected:\n%s\ngot:\n%s", tt.expected, got)
			}
		})
	}
}
