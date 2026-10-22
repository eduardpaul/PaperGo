package model

import "encoding/json"

// FieldOptions is retained in immutable effective schema snapshots.
// String bounds preserve exact integer and decimal precision.
type FieldOptions struct {
	Unique            bool            `json:"unique,omitempty"`
	Description       string          `json:"description,omitempty"`
	DefaultValue      json.RawMessage `json:"default_value,omitempty"`
	MaxLength         *int            `json:"max_length,omitempty"`
	Minimum           *string         `json:"minimum,omitempty"`
	Maximum           *string         `json:"maximum,omitempty"`
	CurrencyCode      string          `json:"currency_code,omitempty"`
	Multiple          bool            `json:"multiple,omitempty"`
	LookupContainerID string          `json:"lookup_container_id,omitempty"`
	TermSetID         string          `json:"term_set_id,omitempty"`
	// ApplyParents makes a term or keywords value also apply its parent
	// terms wherever values are read: filters, keyword counts and search.
	ApplyParents bool `json:"apply_parents,omitempty"`
	// SearchWeight places the field's text in search: high ranks it with
	// names and keywords, none leaves it out (the field stays filterable);
	// empty is normal.
	SearchWeight string `json:"search_weight,omitempty"`
}
