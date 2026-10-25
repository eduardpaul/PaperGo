package workflow

import (
	"strings"
	"testing"
)

func TestCallTriggersAreValidated(t *testing.T) {
	form := map[string]any{"type": "object", "properties": map[string]any{"factor": map[string]any{"type": "integer"}}}
	for _, tc := range []struct {
		name string
		def  Definition
		want string
	}{
		{"called with a form", Definition{Triggers: []Trigger{{Type: TriggerCall, CollectionID: "lib"}}, InputSchema: form}, ""},
		{"called and manual", Definition{Triggers: []Trigger{{Type: TriggerCall}, {Type: TriggerManual}}, InputSchema: form}, ""},
		{"two call triggers", Definition{Triggers: []Trigger{{Type: TriggerCall}, {Type: TriggerCall}}}, "at most one manual trigger and one call trigger"},
		{"scheduled call", Definition{Triggers: []Trigger{{Type: TriggerCall, Cron: "0 * * * *"}}}, "call triggers take only collection_id"},
		{"form without a launch", Definition{Triggers: []Trigger{{Type: TriggerSchedule, Cron: "0 * * * *"}}, InputSchema: form}, "manual or call trigger"},
	} {
		err := tc.def.validate()
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}
