package migrations

// SchemaHash identifies this build's authoritative SQLite schema.
func SchemaHash() string { return Hash(SchemaSQL) }
