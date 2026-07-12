// This file contains a pure RFC 7644 §3.4.2.2 SCIM filter parser and
// evaluator. No I/O: ParseSCIMFilter builds an AST from the filter string and
// Matches evaluates it against a SCIM resource represented as a generic JSON
// map. Attribute names are matched case-insensitively per RFC 7643 §2.1, and
// string comparisons are case-insensitive (caseExact=false default).
//
// SECURITY NOTE: the eval methods below are structural AST evaluation —
// comparing parsed filter literals against JSON map values. Nothing here
// executes code, templates, or expressions from the input; a malformed or
// hostile filter can only ever produce a parse error or a boolean.
//
// Supported grammar:
//
//	FILTER    = expr
//	expr      = andExpr *( "or" andExpr )
//	andExpr   = notExpr *( "and" notExpr )
//	notExpr   = ["not"] ( "(" expr ")" / valuePath / attrExp )
//	valuePath = attrPath "[" expr "]"
//	attrExp   = attrPath "pr" / attrPath compareOp compValue
//	compareOp = "eq"/"ne"/"co"/"sw"/"ew"/"gt"/"ge"/"lt"/"le"
//	compValue = JSON string / number / true / false / null
package sso

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// SCIMFilter is a parsed filter expression.
type SCIMFilter struct {
	root filterNode
}

// Matches evaluates the filter against a SCIM resource JSON map.
func (f *SCIMFilter) Matches(resource map[string]any) bool {
	if f == nil || f.root == nil {
		return true
	}
	return f.root.eval(resource)
}

type filterNode interface {
	eval(resource map[string]any) bool
}

type logicalNode struct {
	op       string // "and" | "or"
	children []filterNode
}

func (n *logicalNode) eval(resource map[string]any) bool {
	for _, child := range n.children {
		matched := child.eval(resource)
		if n.op == "and" && !matched {
			return false
		}
		if n.op == "or" && matched {
			return true
		}
	}
	return n.op == "and"
}

type notNode struct{ child filterNode }

func (n *notNode) eval(resource map[string]any) bool { return !n.child.eval(resource) }

type presentNode struct{ path []string }

func (n *presentNode) eval(resource map[string]any) bool {
	val, ok := resolveSCIMPath(resource, n.path)
	if !ok || val == nil {
		return false
	}
	switch t := val.(type) {
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	default:
		return true
	}
}

type compareNode struct {
	path  []string
	op    string
	value any
}

func (n *compareNode) eval(resource map[string]any) bool {
	val, ok := resolveSCIMPath(resource, n.path)
	if !ok {
		// ne on a missing attribute is true per "not equal" semantics.
		return n.op == "ne"
	}
	// A filter on a multi-valued attribute matches if ANY element matches.
	if list, isList := val.([]any); isList {
		for _, elem := range list {
			if compareValues(elem, n.op, n.value) {
				return true
			}
		}
		return false
	}
	return compareValues(val, n.op, n.value)
}

type valuePathNode struct {
	path   []string
	filter filterNode
}

func (n *valuePathNode) eval(resource map[string]any) bool {
	val, ok := resolveSCIMPath(resource, n.path)
	if !ok {
		return false
	}
	list, isList := val.([]any)
	if !isList {
		return false
	}
	for _, elem := range list {
		if m, isMap := elem.(map[string]any); isMap && n.filter.eval(m) {
			return true
		}
	}
	return false
}

// resolveSCIMPath walks a dotted attribute path case-insensitively. The
// optional URN schema prefix (urn:...:User:userName) is not supported — core
// attributes only, which is what Okta/Entra send.
func resolveSCIMPath(resource map[string]any, path []string) (any, bool) {
	var cur any = resource
	for _, segment := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = lookupCaseInsensitive(m, segment)
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func lookupCaseInsensitive(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// compareValues applies a comparison operator between a resource value and a
// filter literal. Strings compare case-insensitively; numbers numerically;
// booleans and null support eq/ne only.
func compareValues(resourceVal any, op string, filterVal any) bool {
	// Numeric comparison when both sides are numbers.
	if rNum, rOK := toFloat(resourceVal); rOK {
		if fNum, fOK := toFloat(filterVal); fOK {
			switch op {
			case "eq":
				return rNum == fNum
			case "ne":
				return rNum != fNum
			case "gt":
				return rNum > fNum
			case "ge":
				return rNum >= fNum
			case "lt":
				return rNum < fNum
			case "le":
				return rNum <= fNum
			default:
				return false
			}
		}
	}

	rStr, rIsStr := resourceVal.(string)
	fStr, fIsStr := filterVal.(string)
	if rIsStr && fIsStr {
		r, f := strings.ToLower(rStr), strings.ToLower(fStr)
		switch op {
		case "eq":
			return r == f
		case "ne":
			return r != f
		case "co":
			return strings.Contains(r, f)
		case "sw":
			return strings.HasPrefix(r, f)
		case "ew":
			return strings.HasSuffix(r, f)
		case "gt":
			return r > f
		case "ge":
			return r >= f
		case "lt":
			return r < f
		case "le":
			return r <= f
		default:
			return false
		}
	}

	// Booleans / null: equality only.
	switch op {
	case "eq":
		return resourceVal == filterVal
	case "ne":
		return resourceVal != filterVal
	default:
		return false
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// ---- tokenizer + recursive descent parser ----

type filterParser struct {
	input string
	pos   int
}

// ParseSCIMFilter parses an RFC 7644 filter string.
func ParseSCIMFilter(input string) (*SCIMFilter, error) {
	p := &filterParser{input: input}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.input) {
		return nil, fmt.Errorf("scim filter: unexpected trailing input at position %d", p.pos)
	}
	return &SCIMFilter{root: node}, nil
}

func (p *filterParser) skipSpace() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

// peekWord returns the next bare word (letters/digits) without consuming it.
func (p *filterParser) peekWord() string {
	p.skipSpace()
	end := p.pos
	for end < len(p.input) {
		c := p.input[end]
		if c == '(' || c == ')' || c == '[' || c == ']' || unicode.IsSpace(rune(c)) {
			break
		}
		end++
	}
	return p.input[p.pos:end]
}

func (p *filterParser) consumeWord() string {
	w := p.peekWord()
	p.pos += len(w)
	return w
}

func (p *filterParser) parseExpr() (filterNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	children := []filterNode{left}
	for strings.EqualFold(p.peekWord(), "or") {
		p.consumeWord()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &logicalNode{op: "or", children: children}, nil
}

func (p *filterParser) parseAnd() (filterNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	children := []filterNode{left}
	for strings.EqualFold(p.peekWord(), "and") {
		p.consumeWord()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return left, nil
	}
	return &logicalNode{op: "and", children: children}, nil
}

func (p *filterParser) parseNot() (filterNode, error) {
	if strings.EqualFold(p.peekWord(), "not") {
		p.consumeWord()
		p.skipSpace()
		if p.pos >= len(p.input) || p.input[p.pos] != '(' {
			return nil, fmt.Errorf("scim filter: expected '(' after not")
		}
		child, err := p.parseGroup()
		if err != nil {
			return nil, err
		}
		return &notNode{child: child}, nil
	}
	p.skipSpace()
	if p.pos < len(p.input) && p.input[p.pos] == '(' {
		return p.parseGroup()
	}
	return p.parseAttrExpr()
}

func (p *filterParser) parseGroup() (filterNode, error) {
	p.pos++ // consume '('
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos >= len(p.input) || p.input[p.pos] != ')' {
		return nil, fmt.Errorf("scim filter: missing closing ')'")
	}
	p.pos++
	return node, nil
}

// parseAttrExpr parses `attrPath pr`, `attrPath op value`, or a valuePath
// `attrPath[expr]` (optionally followed by nothing — sub-attr-after-bracket is
// a PATCH path feature, not a filter feature).
func (p *filterParser) parseAttrExpr() (filterNode, error) {
	attr := p.consumeWord()
	if attr == "" {
		return nil, fmt.Errorf("scim filter: expected attribute at position %d", p.pos)
	}

	// valuePath: attr[...]
	if p.pos < len(p.input) && p.input[p.pos] == '[' {
		p.pos++
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.input) || p.input[p.pos] != ']' {
			return nil, fmt.Errorf("scim filter: missing closing ']'")
		}
		p.pos++
		return &valuePathNode{path: splitAttrPath(attr), filter: inner}, nil
	}

	op := strings.ToLower(p.consumeWord())
	if op == "" {
		return nil, fmt.Errorf("scim filter: expected operator after %q", attr)
	}
	if op == "pr" {
		return &presentNode{path: splitAttrPath(attr)}, nil
	}
	switch op {
	case "eq", "ne", "co", "sw", "ew", "gt", "ge", "lt", "le":
	default:
		return nil, fmt.Errorf("scim filter: unknown operator %q", op)
	}

	value, err := p.parseCompValue()
	if err != nil {
		return nil, err
	}
	return &compareNode{path: splitAttrPath(attr), op: op, value: value}, nil
}

// parseCompValue parses a JSON literal: quoted string, number, true, false,
// null.
func (p *filterParser) parseCompValue() (any, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("scim filter: expected comparison value")
	}
	if p.input[p.pos] == '"' {
		end := p.pos + 1
		for end < len(p.input) {
			if p.input[end] == '\\' {
				end += 2
				continue
			}
			if p.input[end] == '"' {
				break
			}
			end++
		}
		if end >= len(p.input) {
			return nil, fmt.Errorf("scim filter: unterminated string")
		}
		var s string
		if err := json.Unmarshal([]byte(p.input[p.pos:end+1]), &s); err != nil {
			return nil, fmt.Errorf("scim filter: invalid string literal: %w", err)
		}
		p.pos = end + 1
		return s, nil
	}
	word := p.consumeWord()
	switch strings.ToLower(word) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	if f, err := strconv.ParseFloat(word, 64); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("scim filter: invalid comparison value %q", word)
}

func splitAttrPath(attr string) []string {
	// Strip an optional URN prefix (urn:...:User:userName) down to the final
	// attribute path.
	if idx := strings.LastIndex(attr, ":"); idx >= 0 {
		attr = attr[idx+1:]
	}
	return strings.Split(attr, ".")
}
