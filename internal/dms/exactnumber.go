package dms

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var jsonNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

const decimalMaxCoefficient = "79228162514264337593543950335"

// number matches the System.Decimal domain used by number and currency fields:
// a 96-bit unsigned coefficient, a sign and scale 0..28. Parsing rounds to the
// nearest representable value, with midpoint ties to even. Exponents never
// cause an allocation proportional to their numeric value.
func number(v any) (json.Number, error) {
	var raw string
	switch n := v.(type) {
	case json.Number:
		raw = n.String()
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", invalid("expected finite JSON number")
		}
		raw = strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return "", invalid("expected JSON number")
	}
	if len(raw) > 64*1024 || !jsonNumberPattern.MatchString(raw) {
		return "", invalid("invalid JSON number")
	}
	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	exponent := 0
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		e, err := strconv.ParseInt(raw[i+1:], 10, 32)
		if err != nil {
			if strings.HasPrefix(raw[i+1:], "-") {
				e = -1000000
			} else {
				e = 1000000
			}
		}
		exponent = int(max(-1000000, min(1000000, e)))
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		exponent -= len(raw) - i - 1
		raw = raw[:i] + raw[i+1:]
	}
	digits := strings.TrimLeft(raw, "0")
	if digits == "" {
		return json.Number("0"), nil
	}
	wholeDigits := len(digits) + exponent
	if wholeDigits > 29 {
		return "", invalid("number exceeds decimal range")
	}
	scale := min(28, max(0, -exponent), 29-wholeDigits)
	for ; scale >= 0; scale-- {
		coefficient := roundDecimalDigits(digits, exponent+scale)
		if len(coefficient) > 29 || len(coefficient) == 29 && coefficient > decimalMaxCoefficient {
			continue
		}
		if coefficient == "0" {
			return json.Number("0"), nil
		}
		for len(coefficient) <= scale {
			coefficient = "0" + coefficient
		}
		if scale > 0 {
			coefficient = coefficient[:len(coefficient)-scale] + "." + coefficient[len(coefficient)-scale:]
			coefficient = strings.TrimRight(strings.TrimRight(coefficient, "0"), ".")
		}
		if negative {
			coefficient = "-" + coefficient
		}
		return json.Number(coefficient), nil
	}
	return "", invalid("number exceeds decimal range")
}

func roundDecimalDigits(digits string, shift int) string {
	if shift >= 0 {
		return digits + strings.Repeat("0", shift)
	}
	end := len(digits) + shift
	if end < 0 {
		return "0"
	}
	head := "0"
	if end > 0 {
		head = digits[:end]
	}
	tail := digits[end:]
	up := tail[0] > '5' || tail[0] == '5' && (strings.Trim(tail[1:], "0") != "" || (head[len(head)-1]-'0')%2 != 0)
	if up {
		n, _ := new(big.Int).SetString(head, 10)
		head = n.Add(n, big.NewInt(1)).String()
	}
	return head
}

// A fixed 29+28 digit magnitude, complemented for negatives, preserves numeric
// ordering and equality under SQLite BINARY text indexes without float coercion.
func numberKey(v any) (string, error) {
	n, err := number(v)
	if err != nil {
		return "", err
	}
	negative := strings.HasPrefix(n.String(), "-")
	parts := strings.SplitN(strings.TrimPrefix(n.String(), "-"), ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := strings.Repeat("0", 29-len(parts[0])) + parts[0] + fraction + strings.Repeat("0", 28-len(fraction))
	if !negative {
		return "1" + digits, nil
	}
	return "0" + complementDigits(digits), nil
}

func complementDigits(digits string) string {
	b := []byte(digits)
	for i, d := range b {
		b[i] = '9' - (d - '0')
	}
	return string(b)
}

func numberFromKey(key string) (json.Number, error) {
	if len(key) != 58 || key[0] != '0' && key[0] != '1' {
		return "", invalid("invalid exact numeric rank")
	}
	for _, d := range key[1:] {
		if d < '0' || d > '9' {
			return "", invalid("invalid exact numeric rank")
		}
	}
	digits := key[1:]
	if key[0] == '0' {
		digits = complementDigits(digits)
	}
	raw := strings.TrimLeft(digits[:29], "0")
	if raw == "" {
		raw = "0"
	}
	raw += "." + digits[29:]
	if key[0] == '0' {
		raw = "-" + raw
	}
	n, err := number(json.Number(raw))
	if err != nil {
		return "", err
	}
	canonical, err := numberKey(n)
	if err != nil || canonical != key {
		return "", invalid("invalid exact numeric rank")
	}
	return n, nil
}
