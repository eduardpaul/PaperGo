package model

import "encoding/json"

// ValidationRule is a bounded declarative constraint, never executable code.
type ValidationRule struct {
	Key        string          `json:"key"`
	Field      string          `json:"field"`
	Op         string          `json:"op"`
	OtherField string          `json:"other_field,omitempty"`
	WhenField  string          `json:"when_field,omitempty"`
	WhenValue  json.RawMessage `json:"when_value,omitempty"`
	Message    string          `json:"message"`
}
