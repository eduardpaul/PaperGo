// Package nullable converts between domain values and the optional column
// values of generated models. Domains store an empty identifier, a nil time
// and a nil JSON document as NULL. The conversions are the same for every
// driver's models.
package nullable

import (
	"encoding/json"
	"time"

	"github.com/aarondl/opt/null"
	"github.com/aarondl/opt/omitnull"
	"github.com/stephenafamo/bob/types"
)

// String sets a nullable text column; empty is NULL.
func String(s string) omitnull.Val[string] {
	if s == "" {
		return omitnull.FromPtr[string](nil)
	}
	return omitnull.From(s)
}

// Time sets a nullable timestamp column.
func Time(t *time.Time) omitnull.Val[time.Time] {
	return omitnull.FromPtr(t)
}

// Object sets a nullable JSON object column.
func Object(m map[string]any) omitnull.Val[types.JSON[map[string]any]] {
	if m == nil {
		return omitnull.FromPtr[types.JSON[map[string]any]](nil)
	}
	return omitnull.From(types.NewJSON(m))
}

// JSON sets a nullable JSON document column.
func JSON(raw json.RawMessage) omitnull.Val[types.JSON[json.RawMessage]] {
	if len(raw) == 0 {
		return omitnull.FromPtr[types.JSON[json.RawMessage]](nil)
	}
	return omitnull.From(types.NewJSON(raw))
}

// UTC reads a nullable timestamp column.
func UTC(v null.Val[time.Time]) *time.Time {
	t, ok := v.Get()
	if !ok {
		return nil
	}
	t = t.UTC()
	return &t
}
