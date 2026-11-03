package server

import (
	"slices"

	"papergo/extensions/mcp"
)

// Caller is a resolved caller.
type Caller struct {
	subject string
	scopes  []string
	backend mcp.Backend
}

func (c *Caller) Subject() string            { return c.subject }
func (c *Caller) HasScope(scope string) bool { return slices.Contains(c.scopes, scope) }
func (c *Caller) Backend() mcp.Backend       { return c.backend }
