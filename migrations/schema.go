package migrations

import (
	_ "embed"
	"strings"
)

//go:embed atlas.sum
var checksum string

// SchemaHash identifies this build's single, complete Atlas schema.
func SchemaHash() string {
	return strings.TrimPrefix(strings.Fields(strings.Split(strings.TrimSpace(checksum), "\n")[1])[1], "h1:")
}
