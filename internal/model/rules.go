package model

import "encoding/json"

// ValidationRule is a bounded declarative constraint, never executable code.
type ValidationRule struct {
	Key        string          `json:"key" pattern:"^[a-z][a-z0-9_]{0,63}$"`
	Field      string          `json:"field"`
	Op         string          `json:"op" enum:"eq,ne,gt,gte,lt,lte,required_if"`
	OtherField string          `json:"other_field,omitempty" doc:"Comparisons: field of the same scalar type and decimal scale."`
	WhenField  string          `json:"when_field,omitempty" doc:"required_if: field whose value triggers the requirement."`
	WhenValue  json.RawMessage `json:"when_value,omitempty" doc:"required_if: non-null scalar value compatible with when_field."`
	Message    string          `json:"message" minLength:"1" maxLength:"1024"`
}
