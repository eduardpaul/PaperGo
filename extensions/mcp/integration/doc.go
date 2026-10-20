// Package integration tests papergo-mcp with the official MCP client
// against the real PaperGo API on a fixture database: SQLite, or
// PostgreSQL when PAPERGO_TEST_POSTGRES_DSN is set. It lives apart from the
// extension so the extension's own build needs none of the API's packages.
package integration
