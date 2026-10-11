package dms

import (
	"papergo/ent"
	"papergo/ent/resource"
	"papergo/internal/model"
)

func contentKind(kind resource.Kind) bool {
	return kind == resource.KindItem || kind == resource.KindFolder
}

// Folders use collection content types, but do not require item fields or
// receive creation defaults. Other field constraints and comparison rules apply.
func resourceDefinitions(kind resource.Kind, defs []*ent.FieldDefinition, rules []model.ValidationRule) ([]*ent.FieldDefinition, []model.ValidationRule) {
	if kind != resource.KindFolder {
		return defs, rules
	}
	optional := make([]*ent.FieldDefinition, 0, len(defs))
	for _, d := range defs {
		copy := *d
		copy.Required = false
		optional = append(optional, &copy)
	}
	comparisons := make([]model.ValidationRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Op != "required_if" {
			comparisons = append(comparisons, rule)
		}
	}
	return optional, comparisons
}
