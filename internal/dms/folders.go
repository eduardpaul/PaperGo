package dms

import (
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

func contentKind(kind refs.ResourceKind) bool {
	return kind == refs.KindItem || kind == refs.KindFolder
}

// contentKinds selects folders and items.
var contentKinds = []refs.ResourceKind{refs.KindItem, refs.KindFolder}

// Folders use collection content types, but do not require item fields or
// receive creation defaults. Other field constraints and comparison rules apply.
func resourceDefinitions(kind refs.ResourceKind, defs []*catalog.FieldDefinition, rules []model.ValidationRule) ([]*catalog.FieldDefinition, []model.ValidationRule) {
	if kind != refs.KindFolder {
		return defs, rules
	}
	optional := make([]*catalog.FieldDefinition, 0, len(defs))
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
