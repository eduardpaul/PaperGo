package dms

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
)

var relationshipKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func validateRelationshipAttributes(values map[string]any) error {
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > 16*1024 || len(values) > 64 {
		return invalid("relationship metadata allows at most 64 keys and 16 KiB")
	}
	var properties map[string]json.RawMessage
	if err = json.Unmarshal(raw, &properties); err != nil {
		return err
	}
	for key, value := range properties {
		if !relationshipKey.MatchString(key) {
			return invalid("relationship attribute keys must start with a letter or underscore and contain only letters, digits or underscores; maximum 128 characters")
		}
		if len(value) == 0 {
			return invalid("invalid relationship attribute")
		}
		switch value[0] {
		case '{', '[':
			return invalid("relationship attributes must be scalar strings, numbers or booleans")
		case '"', 't', 'f', 'n':
		default:
			n, err := strconv.ParseFloat(string(value), 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return invalid("relationship numbers must be finite")
			}
		}
	}
	return nil
}
