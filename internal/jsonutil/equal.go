// Package jsonutil provides database-independent JSON value comparisons.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strings"
)

// Equal compares JSON values without depending on object order, whitespace or
// numeric spelling. Numbers retain their precision rather than using float64.
func Equal(a, b []byte) bool {
	decode := func(raw []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var value any
		if d.Decode(&value) != nil {
			return nil, false
		}
		var extra any
		return value, d.Decode(&extra) == io.EOF
	}
	av, ok := decode(a)
	if !ok {
		return false
	}
	bv, ok := decode(b)
	return ok && equalValue(av, bv)
}

func equalValue(a, b any) bool {
	switch a := a.(type) {
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok || len(a) != len(bm) {
			return false
		}
		for key, value := range a {
			other, exists := bm[key]
			if !exists || !equalValue(value, other) {
				return false
			}
		}
		return true
	case []any:
		bs, ok := b.([]any)
		if !ok || len(a) != len(bs) {
			return false
		}
		for i := range a {
			if !equalValue(a[i], bs[i]) {
				return false
			}
		}
		return true
	case json.Number:
		bn, ok := b.(json.Number)
		if !ok {
			return false
		}
		ac, ae := numberParts(string(a))
		bc, be := numberParts(string(bn))
		return ac == bc && ae.Cmp(be) == 0
	default:
		return a == b
	}
}

// numberParts normalizes a valid JSON number to coefficient * 10^exponent.
// Keeping the exponent separate avoids allocating huge powers for inputs such
// as 1e1000000000, while retaining all significant digits.
func numberParts(value string) (string, *big.Int) {
	exponent := new(big.Int)
	if at := strings.IndexAny(value, "eE"); at >= 0 {
		exponent.SetString(value[at+1:], 10)
		value = value[:at]
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	if at := strings.IndexByte(value, '.'); at >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(value)-at-1)))
		value = value[:at] + value[at+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0", new(big.Int)
	}
	coefficient := strings.TrimRight(value, "0")
	exponent.Add(exponent, big.NewInt(int64(len(value)-len(coefficient))))
	if negative {
		coefficient = "-" + coefficient
	}
	return coefficient, exponent
}
