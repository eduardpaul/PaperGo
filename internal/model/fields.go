package model

import "encoding/json"

// FieldOptions is retained in immutable effective schema snapshots.
// String bounds preserve exact integer and decimal precision.
type FieldOptions struct {
	Description       string          `json:"description,omitempty"`
	DefaultValue      json.RawMessage `json:"default_value,omitempty"`
	MaxLength         *int            `json:"max_length,omitempty"`
	Minimum           *string         `json:"minimum,omitempty"`
	Maximum           *string         `json:"maximum,omitempty"`
	Multiple          bool            `json:"multiple,omitempty"`
	LookupContainerID string          `json:"lookup_container_id,omitempty"`
	TermSetID         string          `json:"term_set_id,omitempty"`
}
