package workflow

import (
	"encoding/json"
	"papergo/internal/dms"
	"slices"
	"strings"
)

func automaticScope(def Definition) error {
	for _, tr := range def.Triggers {
		if tr.Disabled {
			continue
		}
		if tr.CollectionID == "" && (strings.HasPrefix(tr.Type, "file.") || strings.HasPrefix(tr.Type, "item.") || strings.HasPrefix(tr.Type, "folder.") || tr.Type == dms.EventPermissionsChanged) {
			return dms.Invalid("Automatic content processing requires a specific list or library for every active rule.")
		}
	}
	return nil
}

func validState(state string) error {
	if state != "draft" && state != "published" && state != "archived" {
		return dms.Invalid("state must be draft, published or archived")
	}
	return nil
}

func disabledTriggerTypes(raw json.RawMessage) []string {
	def, _ := ParseDefinition(raw)
	out := []string{}
	for _, tr := range def.Triggers {
		if tr.Disabled && !slices.Contains(out, tr.Type) {
			out = append(out, tr.Type)
		}
	}
	return out
}

func withDisabledTriggers(raw json.RawMessage, disabled []string) (json.RawMessage, error) {
	def, err := ParseDefinition(raw)
	if err != nil {
		return nil, err
	}
	for _, typ := range disabled {
		if typ == TriggerManual || !slices.ContainsFunc(def.Triggers, func(tr Trigger) bool { return tr.Type == typ }) {
			return nil, dms.Invalid("disabled_triggers must name automatic events declared by the workflow")
		}
	}
	for i := range def.Triggers {
		def.Triggers[i].Disabled = slices.Contains(disabled, def.Triggers[i].Type)
	}
	return json.Marshal(def)
}
