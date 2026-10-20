// Package sdk embeds the bundled @papergo/workflow SDK that the workflow
// engine runs before each script, and the API scope scripts may call.
// Regenerate both with npm --prefix sdk run generate && npm --prefix sdk run build.
package sdk

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// Bundle defines the global __papergoSdk, which require("@papergo/workflow")
// returns inside the engine.
//
//go:embed dist/sdk.js
var Bundle string

//go:embed scope.json
var scopeJSON []byte

var excluded = func() [][]string {
	var scope struct {
		Exclude []string `json:"exclude"`
	}
	if err := json.Unmarshal(scopeJSON, &scope); err != nil {
		panic(err)
	}
	patterns := make([][]string, len(scope.Exclude))
	for i, pattern := range scope.Exclude {
		patterns[i] = strings.Split(strings.Trim(pattern, "/"), "/")
	}
	return patterns
}()

// Excluded reports whether scripts are refused the API path, which has no
// query string. It applies scope.json as the client generator does: * is one
// segment and ** one or more.
func Excluded(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for _, pattern := range excluded {
		if match(pattern, segments) {
			return true
		}
	}
	return false
}

func match(pattern, segments []string) bool {
	if len(pattern) == 0 {
		return len(segments) == 0
	}
	if pattern[0] == "**" {
		for n := 1; n <= len(segments); n++ {
			if match(pattern[1:], segments[n:]) {
				return true
			}
		}
		return false
	}
	if len(segments) == 0 || (pattern[0] != "*" && !strings.EqualFold(pattern[0], segments[0])) {
		return false
	}
	return match(pattern[1:], segments[1:])
}
