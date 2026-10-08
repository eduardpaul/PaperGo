package model

import "encoding/json"

// FieldOptions is retained in immutable effective schema snapshots.
// String bounds preserve exact integer and decimal precision.
type FieldOptions struct {
	Unique            bool            `json:"unique,omitempty" doc:"Collection-wide exact, case-sensitive business key covering both head and published values. Requires indexed=true and a scalar type other than approximate number. Nulls do not reserve a key. Collisions return 409 and roll back the entire mutation or bulk batch."`
	Description       string          `json:"description,omitempty" maxLength:"4096"`
	DefaultValue      json.RawMessage `json:"default_value,omitempty" doc:"Value applied when an item omits the field; validated like item values."`
	MaxLength         *int            `json:"max_length,omitempty" minimum:"1" maximum:"65536"`
	Minimum           *string         `json:"minimum,omitempty" doc:"Exact numeric lower bound."`
	Maximum           *string         `json:"maximum,omitempty" doc:"Exact numeric upper bound."`
	Multiple          bool            `json:"multiple,omitempty" doc:"Immutable. Allows an ordered list of values."`
	LookupContainerID string          `json:"lookup_container_id,omitempty" format:"uuid" doc:"Immutable. Lookup fields: list or library whose items are referenced."`
	TermSetID         string          `json:"term_set_id,omitempty" format:"uuid" doc:"Immutable. Term fields: term set whose terms are referenced."`
}
