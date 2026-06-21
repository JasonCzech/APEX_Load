package jms

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// selector is a compiled JMS message selector. JMS selectors are a SQL-92
// conditional-expression subset evaluated against a message's headers and
// properties. This implementation supports: identifiers, string ('...') and
// numeric and boolean (TRUE/FALSE) literals; the comparison operators
// = <> > < >= <=; AND / OR / NOT and parentheses; [NOT] BETWEEN x AND y;
// [NOT] LIKE 'pattern' [ESCAPE 'c'] with % and _ wildcards; [NOT] IN (...);
// and IS [NOT] NULL. Comparisons follow JMS three-valued logic — a comparison
// involving a missing property is UNKNOWN, and a selector that evaluates to
// UNKNOWN at the top level does not select the message.
type selector struct {
	expr selNode
	src  string
}

// compileSelector parses a selector expression. An empty expression compiles to
// a nil *selector that matches every message.
func compileSelector(expr string) (*selector, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, nil
	}
	toks, err := lexSelector(expr)
	if err != nil {
		return nil, err
	}
	p := &selParser{tokens: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if !p.atEnd() {
		return nil, fmt.Errorf("selector: unexpected token %q", p.peek().text)
	}
	return &selector{expr: node, src: expr}, nil
}

// matches reports whether the message properties satisfy the selector. A nil
// selector matches everything. Property lookups consult Properties first, then
// Headers, so a selector can reference either.
func (s *selector) matches(headers, properties map[string]string) bool {
	if s == nil {
		return true
	}
	resolve := func(name string) (string, bool) {
		if properties != nil {
			if v, ok := properties[name]; ok {
				return v, true
			}
		}
		if headers != nil {
			if v, ok := headers[name]; ok {
				return v, true
			}
		}
		return "", false
	}
	return s.expr.eval(resolve) == triTrue
}

// --- tri-state logic ---

type tri int

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

func triNot(a tri) tri {
	switch a {
	case triTrue:
		return triFalse
	case triFalse:
		return triTrue
	default:
		return triUnknown
	}
}

func triFromBool(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

// --- AST ---

type resolver func(name string) (string, bool)

type selNode interface {
	eval(resolver) tri
}

type orNode struct{ left, right selNode }

func (n orNode) eval(r resolver) tri {
	l, rr := n.left.eval(r), n.right.eval(r)
	if l == triTrue || rr == triTrue {
		return triTrue
	}
	if l == triUnknown || rr == triUnknown {
		return triUnknown
	}
	return triFalse
}

type andNode struct{ left, right selNode }

func (n andNode) eval(r resolver) tri {
	l, rr := n.left.eval(r), n.right.eval(r)
	if l == triFalse || rr == triFalse {
		return triFalse
	}
	if l == triUnknown || rr == triUnknown {
		return triUnknown
	}
	return triTrue
}

type notNode struct{ inner selNode }

func (n notNode) eval(r resolver) tri { return triNot(n.inner.eval(r)) }

// operand kinds.
type operandKind int

const (
	opIdent operandKind = iota
	opString
	opNumber
	opBool
)

type operand struct {
	kind operandKind
	text string  // identifier name or string literal
	num  float64 // numeric literal
	b    bool    // boolean literal
}

// resolved returns the operand's value as (string, isNumber, numeric, isNull).
func (o operand) resolved(r resolver) (str string, isNum bool, num float64, isNull bool) {
	switch o.kind {
	case opString:
		return o.text, false, 0, false
	case opNumber:
		return strconv.FormatFloat(o.num, 'g', -1, 64), true, o.num, false
	case opBool:
		return strconv.FormatBool(o.b), false, 0, false
	default: // identifier
		v, ok := r(o.text)
		if !ok {
			return "", false, 0, true
		}
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return v, true, n, false
		}
		return v, false, 0, false
	}
}

type compareNode struct {
	op          string
	left, right operand
}

func (n compareNode) eval(r resolver) tri {
	ls, lIsNum, ln, lNull := n.left.resolved(r)
	rs, rIsNum, rn, rNull := n.right.resolved(r)
	if lNull || rNull {
		return triUnknown
	}
	// Numeric comparison when both sides are numeric; otherwise string.
	if lIsNum && rIsNum {
		switch n.op {
		case "=":
			return triFromBool(ln == rn)
		case "<>":
			return triFromBool(ln != rn)
		case ">":
			return triFromBool(ln > rn)
		case "<":
			return triFromBool(ln < rn)
		case ">=":
			return triFromBool(ln >= rn)
		case "<=":
			return triFromBool(ln <= rn)
		}
	}
	switch n.op {
	case "=":
		return triFromBool(ls == rs)
	case "<>":
		return triFromBool(ls != rs)
	case ">":
		return triFromBool(ls > rs)
	case "<":
		return triFromBool(ls < rs)
	case ">=":
		return triFromBool(ls >= rs)
	case "<=":
		return triFromBool(ls <= rs)
	}
	return triUnknown
}

type betweenNode struct {
	value, lo, hi operand
	negate        bool
}

func (n betweenNode) eval(r resolver) tri {
	_, vNum, v, vNull := n.value.resolved(r)
	_, loNum, lo, loNull := n.lo.resolved(r)
	_, hiNum, hi, hiNull := n.hi.resolved(r)
	if vNull || loNull || hiNull {
		return triUnknown
	}
	if !vNum || !loNum || !hiNum {
		return triUnknown
	}
	in := v >= lo && v <= hi
	if n.negate {
		in = !in
	}
	return triFromBool(in)
}

type likeNode struct {
	value   operand
	pattern *regexp.Regexp
	negate  bool
}

func (n likeNode) eval(r resolver) tri {
	s, _, _, isNull := n.value.resolved(r)
	if isNull {
		return triUnknown
	}
	m := n.pattern.MatchString(s)
	if n.negate {
		m = !m
	}
	return triFromBool(m)
}

type inNode struct {
	value  operand
	list   []operand
	negate bool
}

func (n inNode) eval(r resolver) tri {
	vs, vNum, vn, vNull := n.value.resolved(r)
	if vNull {
		return triUnknown
	}
	found := false
	for _, item := range n.list {
		is, iNum, in, iNull := item.resolved(r)
		if iNull {
			continue
		}
		if vNum && iNum {
			if vn == in {
				found = true
				break
			}
			continue
		}
		if vs == is {
			found = true
			break
		}
	}
	if n.negate {
		return triFromBool(!found)
	}
	return triFromBool(found)
}

type isNullNode struct {
	ident  string
	negate bool
}

func (n isNullNode) eval(r resolver) tri {
	_, ok := r(n.ident)
	isNull := !ok
	if n.negate {
		return triFromBool(!isNull)
	}
	return triFromBool(isNull)
}
