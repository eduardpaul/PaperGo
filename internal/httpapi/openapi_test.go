package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIReferencesResolve(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if value, ok := node["$ref"]; ok {
				ref, ok := value.(string)
				if !ok || ref == "" {
					t.Fatalf("invalid reference: %v", value)
				}
				if strings.HasPrefix(ref, "#/") {
					var target any = document
					for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
						part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
						object, ok := target.(map[string]any)
						if !ok {
							t.Fatalf("unresolved reference: %s", ref)
						}
						target, ok = object[part]
						if !ok {
							t.Fatalf("unresolved reference: %s", ref)
						}
					}
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document)
	operations := map[string]string{}
	paths := document["paths"].(map[string]any)
	for path, value := range paths {
		for method, value := range value.(map[string]any) {
			op, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := op["operationId"].(string); ok {
				if prior, exists := operations[id]; exists {
					t.Fatalf("duplicate operation %s at %s and %s", id, prior, path)
				}
				operations[id] = path
			}
			if method == "get" && op["requestBody"] != nil {
				t.Fatalf("GET %s unexpectedly requires a body", path)
			}
		}
	}
}
