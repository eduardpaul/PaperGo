package model

import (
	"bytes"
	"encoding/json"
)

// JSONMap preserves numeric literals in arbitrary domain-event payloads, including
// workflow launch inputs. Durable dispatch must never round through float64.
type JSONMap map[string]any

func (m *JSONMap) UnmarshalJSON(raw []byte) error {
	type plain JSONMap
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode((*plain)(m))
}
