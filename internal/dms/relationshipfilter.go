package dms

import (
	"encoding/json"
	"github.com/google/uuid"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// The edge query accepts a bounded subset of OData: scalar comparisons,
// and/or/not and parentheses. Values and JSON paths are always SQL parameters.
type edgeToken struct {
	text   string
	quoted bool
}
type edgeFilterParser struct {
	tokens    []edgeToken
	at, nodes int
}
type edgePredicate struct {
	sql  string
	args []any
}

func relationshipFilter(text string) (string, []any, error) {
	if len(text) > 4096 {
		return "", nil, invalid("relationship filters are limited to 4096 bytes")
	}
	tokens, err := edgeTokens(text)
	if err != nil {
		return "", nil, err
	}
	p := edgeFilterParser{tokens: tokens}
	out, err := p.expression(0, 0)
	if err == nil && p.at != len(tokens) {
		err = invalid("unexpected relationship filter token")
	}
	return out.sql, out.args, err
}
func edgeTokens(text string) ([]edgeToken, error) {
	out := []edgeToken{}
	for i := 0; i < len(text); {
		if unicode.IsSpace(rune(text[i])) {
			i++
			continue
		}
		if text[i] == '(' || text[i] == ')' {
			out = append(out, edgeToken{text[i : i+1], false})
			i++
			continue
		}
		if text[i] == '\'' {
			i++
			var b strings.Builder
			closed := false
			for i < len(text) {
				if text[i] == '\'' {
					if i+1 < len(text) && text[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				b.WriteByte(text[i])
				i++
			}
			if !closed {
				return nil, invalid("unterminated filter string")
			}
			out = append(out, edgeToken{b.String(), true})
			continue
		}
		start := i
		for i < len(text) && !unicode.IsSpace(rune(text[i])) && text[i] != '(' && text[i] != ')' && text[i] != '\'' {
			i++
		}
		if start == i {
			return nil, invalid("invalid filter token")
		}
		out = append(out, edgeToken{text[start:i], false})
	}
	return out, nil
}
func (p *edgeFilterParser) is(value string) bool {
	return p.at < len(p.tokens) && !p.tokens[p.at].quoted && p.tokens[p.at].text == value
}
func edgePrecedence(token edgeToken) int {
	if token.quoted {
		return -1
	}
	if token.text == "or" {
		return 0
	}
	if token.text == "and" {
		return 1
	}
	return -1
}
func (p *edgeFilterParser) expression(min, depth int) (edgePredicate, error) {
	if depth > 6 {
		return edgePredicate{}, invalid("filter depth exceeds 6")
	}
	left, err := p.primary(depth)
	if err != nil {
		return left, err
	}
	for p.at < len(p.tokens) {
		precedence := edgePrecedence(p.tokens[p.at])
		if precedence < min {
			break
		}
		op := strings.ToUpper(p.tokens[p.at].text)
		p.at++
		right, err := p.expression(precedence+1, depth+1)
		if err != nil {
			return edgePredicate{}, err
		}
		left = edgePredicate{"(" + left.sql + ") " + op + " (" + right.sql + ")", append(left.args, right.args...)}
	}
	return left, nil
}
func (p *edgeFilterParser) primary(depth int) (edgePredicate, error) {
	p.nodes++
	if p.nodes > 32 || depth > 6 {
		return edgePredicate{}, invalid("filter is limited to 32 nodes and depth 6")
	}
	if p.is("not") {
		p.at++
		v, err := p.primary(depth + 1)
		v.sql = "NOT (" + v.sql + ")"
		return v, err
	}
	if p.is("(") {
		p.at++
		v, err := p.expression(0, depth+1)
		if err != nil {
			return v, err
		}
		if !p.is(")") {
			return v, invalid("expected closing parenthesis")
		}
		p.at++
		return v, nil
	}
	if p.at >= len(p.tokens) {
		return edgePredicate{}, invalid("expected filter comparison")
	}
	if (p.is("true") || p.is("false")) && (p.at+1 == len(p.tokens) || p.tokens[p.at+1].text == ")" || edgePrecedence(p.tokens[p.at+1]) >= 0) {
		v := "0"
		if p.is("true") {
			v = "1"
		}
		p.at++
		return edgePredicate{sql: v}, nil
	}
	if p.at+2 >= len(p.tokens) {
		return edgePredicate{}, invalid("expected property, operator and literal")
	}
	left, op, right := p.tokens[p.at], p.tokens[p.at+1], p.tokens[p.at+2]
	p.at += 3
	if op.quoted {
		return edgePredicate{}, invalid("invalid comparison operator")
	}
	if edgeProperty(right) && !edgeProperty(left) {
		left, right = right, left
		op.text = map[string]string{"gt": "lt", "ge": "le", "lt": "gt", "le": "ge", "eq": "eq", "ne": "ne"}[op.text]
	}
	return edgeComparison(left, op.text, right)
}
func edgeProperty(t edgeToken) bool {
	return !t.quoted && (t.text == "id" || t.text == "directed" || strings.HasPrefix(t.text, "attributes/"))
}
func edgeComparison(property edgeToken, op string, literal edgeToken) (edgePredicate, error) {
	operator := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[op]
	if operator == "" || !edgeProperty(property) {
		return edgePredicate{}, invalid("filter attributes/key, id or directed using scalar comparisons")
	}
	var value any
	kind := ""
	switch {
	case literal.quoted:
		value = literal.text
		kind = "text"
	case literal.text == "true" || literal.text == "false":
		value = literal.text == "true"
		kind = "boolean"
	case literal.text == "null":
		kind = "null"
	default:
		if _, err := uuid.Parse(literal.text); err == nil {
			value = literal.text
			kind = "uuid"
		} else {
			n, err := strconv.ParseFloat(literal.text, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return edgePredicate{}, invalid("expected string, number, boolean, UUID or null literal")
			}
			value = n
			kind = "number"
		}
	}
	if property.text == "id" {
		if kind != "uuid" || op != "eq" && op != "ne" {
			return edgePredicate{}, invalid("id supports UUID equality")
		}
		return edgePredicate{"id" + operator + "?", []any{value}}, nil
	}
	if property.text == "directed" {
		if kind != "boolean" || op != "eq" && op != "ne" {
			return edgePredicate{}, invalid("directed supports boolean equality")
		}
		return edgePredicate{"directed" + operator + "?", []any{value}}, nil
	}
	key := strings.TrimPrefix(property.text, "attributes/")
	if key == "" || strings.ContainsAny(key, "/\"\\") || len(key) > 128 {
		return edgePredicate{}, invalid("nested attributes are not supported")
	}
	encoded, _ := json.Marshal(key)
	path := "$." + string(encoded)
	if kind == "null" {
		text := "json_type(metadata,?) IS NULL"
		if op == "ne" {
			text = "json_type(metadata,?) IS NOT NULL"
		} else if op != "eq" {
			return edgePredicate{}, invalid("null supports eq/ne only")
		}
		return edgePredicate{text, []any{path}}, nil
	}
	if kind != "number" && op != "eq" && op != "ne" {
		return edgePredicate{}, invalid("ordering requires numeric attributes")
	}
	guard := "json_type(metadata,?)='text'"
	if kind == "number" {
		return edgePredicate{"((json_type(metadata,?) IN ('integer','real') OR (json_type(metadata,?)='text' AND CASE WHEN json_valid(json_extract(metadata,?)) THEN json_type(json_extract(metadata,?)) IN ('integer','real') ELSE 0 END)) AND CAST(json_extract(metadata,?) AS REAL)" + operator + "?)", []any{path, path, path, path, path, value}}, nil
	} else if kind == "boolean" {
		guard = "json_type(metadata,?) IN ('true','false')"
	} else if kind == "uuid" {
		return edgePredicate{}, invalid("attributes support strings, numbers and booleans")
	}
	return edgePredicate{"(" + guard + " AND json_extract(metadata,?)" + operator + "?)", []any{path, path, value}}, nil
}
