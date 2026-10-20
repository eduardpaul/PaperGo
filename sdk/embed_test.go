package sdk

import "testing"

func TestExcluded(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/identity":                          true,
		"/v1/identity/me/tokens":                true,
		"/v1/$batch":                            true,
		"/v1/workspaces/w1/workflows":           true,
		"/v1/workspaces/w1/workflow-builtins":   true,
		"/v1/workspaces/w1/workflow-builtins/k": true,
		"/v1/resources/r1/workflow-roles":       true,
		"/auth/config":                          true,
		"/v1/resources/r1/children":             false,
		"/v1/workspaces":                        false,
		"/v1/workspaces/w1/members":             false,
		"/v1/items/i1/relationships":            false,
	} {
		if got := Excluded(path); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", path, got, want)
		}
	}
	if Bundle == "" {
		t.Fatal("the SDK bundle is empty")
	}
}
