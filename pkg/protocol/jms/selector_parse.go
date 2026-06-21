package jms

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// selToken is one lexical token of a JMS selector expression.
type selTokenKind int

const (
	tkIdent selTokenKind = iota
	tkString
	tkNumber
	tkOp     // = <> > < >= <= ( ) ,
	tkKeybrd // AND OR NOT LIKE IN IS NULL BETWEEN ESCAPE TRUE FALSE
)

type selToken struct {
	kind selTokenKind
	text string // uppercased for keywords/ops; raw value for ident/string/number
}

var selKeywords = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "LIKE": true, "IN": true,
	"IS": true, "NULL": true, "BETWEEN": true, "ESCAPE": true, "TRUE": true, "FALSE": true,
}

// lexSelector tokenizes a selector expression.
func lexSelector(input string) ([]selToken, error) {
	var toks []selToken
	runes := []rune(input)
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			// String literal; '' is an escaped quote.
			i++
			var sb strings.Builder
			closed := false
			for i < len(runes) {
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						sb.WriteRune('\'')
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				sb.WriteRune(runes[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("selector: unterminated string literal")
			}
			toks = append(toks, selToken{kind: tkString, text: sb.String()})
		case c == '(' || c == ')' || c == ',':
			toks = append(toks, selToken{kind: tkOp, text: string(c)})
			i++
		case c == '=':
			toks = append(toks, selToken{kind: tkOp, text: "="})
			i++
		case c == '>' || c == '<':
			op := string(c)
			if i+1 < len(runes) && (runes[i+1] == '=' || (c == '<' && runes[i+1] == '>')) {
				op += string(runes[i+1])
				i++
			}
			toks = append(toks, selToken{kind: tkOp, text: op})
			i++
		case c == '+' || c == '-':
			// Unary sign on a numeric literal (e.g. x < -5). A bare number that
			// already carries an exponent sign (1e-5) is handled in the number
			// branch and never reaches here.
			toks = append(toks, selToken{kind: tkOp, text: string(c)})
			i++
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9'):
			start := i
			for i < len(runes) && (runes[i] >= '0' && runes[i] <= '9' || runes[i] == '.' || runes[i] == 'e' || runes[i] == 'E' || runes[i] == '+' || runes[i] == '-') {
				// Only consume +/- as part of an exponent.
				if (runes[i] == '+' || runes[i] == '-') && !(i > start && (runes[i-1] == 'e' || runes[i-1] == 'E')) {
					break
				}
				i++
			}
			num := string(runes[start:i])
			if _, err := strconv.ParseFloat(num, 64); err != nil {
				return nil, fmt.Errorf("selector: invalid number %q", num)
			}
			toks = append(toks, selToken{kind: tkNumber, text: num})
		case isIdentStart(c):
			start := i
			for i < len(runes) && isIdentPart(runes[i]) {
				i++
			}
			word := string(runes[start:i])
			upper := strings.ToUpper(word)
			if selKeywords[upper] {
				toks = append(toks, selToken{kind: tkKeybrd, text: upper})
			} else {
				toks = append(toks, selToken{kind: tkIdent, text: word})
			}
		default:
			return nil, fmt.Errorf("selector: unexpected character %q", string(c))
		}
	}
	return toks, nil
}

func isIdentStart(c rune) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c rune) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}

// selParser is a recursive-descent parser over the token stream.
type selParser struct {
	tokens []selToken
	pos    int
}

func (p *selParser) atEnd() bool { return p.pos >= len(p.tokens) }
func (p *selParser) peek() selToken {
	if p.atEnd() {
		return selToken{kind: tkOp, text: ""}
	}
	return p.tokens[p.pos]
}
func (p *selParser) next() selToken { t := p.peek(); p.pos++; return t }

func (p *selParser) acceptKeyword(kw string) bool {
	if t := p.peek(); t.kind == tkKeybrd && t.text == kw {
		p.pos++
		return true
	}
	return false
}

func (p *selParser) acceptOp(op string) bool {
	if t := p.peek(); t.kind == tkOp && t.text == op {
		p.pos++
		return true
	}
	return false
}

func (p *selParser) parseOr() (selNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("OR") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orNode{left: left, right: right}
	}
	return left, nil
}

func (p *selParser) parseAnd() (selNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.acceptKeyword("AND") {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andNode{left: left, right: right}
	}
	return left, nil
}

func (p *selParser) parseNot() (selNode, error) {
	if p.acceptKeyword("NOT") {
		inner, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return notNode{inner: inner}, nil
	}
	return p.parsePrimary()
}

func (p *selParser) parsePrimary() (selNode, error) {
	if p.acceptOp("(") {
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.acceptOp(")") {
			return nil, fmt.Errorf("selector: expected ')'")
		}
		return node, nil
	}
	return p.parseComparison()
}

func (p *selParser) parseComparison() (selNode, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	// IS [NOT] NULL — only valid on an identifier.
	if p.acceptKeyword("IS") {
		negate := p.acceptKeyword("NOT")
		if !p.acceptKeyword("NULL") {
			return nil, fmt.Errorf("selector: expected NULL after IS")
		}
		if left.kind != opIdent {
			return nil, fmt.Errorf("selector: IS NULL requires a property identifier")
		}
		return isNullNode{ident: left.text, negate: negate}, nil
	}

	negate := p.acceptKeyword("NOT")
	switch {
	case p.acceptKeyword("BETWEEN"):
		lo, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		if !p.acceptKeyword("AND") {
			return nil, fmt.Errorf("selector: expected AND in BETWEEN")
		}
		hi, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		return betweenNode{value: left, lo: lo, hi: hi, negate: negate}, nil
	case p.acceptKeyword("LIKE"):
		patTok := p.next()
		if patTok.kind != tkString {
			return nil, fmt.Errorf("selector: LIKE requires a string pattern")
		}
		escape := ""
		if p.acceptKeyword("ESCAPE") {
			escTok := p.next()
			if escTok.kind != tkString || len([]rune(escTok.text)) != 1 {
				return nil, fmt.Errorf("selector: ESCAPE requires a single-character string")
			}
			escape = escTok.text
		}
		re, err := likeToRegexp(patTok.text, escape)
		if err != nil {
			return nil, err
		}
		return likeNode{value: left, pattern: re, negate: negate}, nil
	case p.acceptKeyword("IN"):
		if !p.acceptOp("(") {
			return nil, fmt.Errorf("selector: expected '(' after IN")
		}
		var list []operand
		for {
			item, err := p.parseOperand()
			if err != nil {
				return nil, err
			}
			list = append(list, item)
			if p.acceptOp(",") {
				continue
			}
			break
		}
		if !p.acceptOp(")") {
			return nil, fmt.Errorf("selector: expected ')' to close IN list")
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("selector: empty IN list")
		}
		return inNode{value: left, list: list, negate: negate}, nil
	}

	if negate {
		return nil, fmt.Errorf("selector: NOT must be followed by BETWEEN, LIKE, or IN")
	}

	// Plain comparison operator.
	opTok := p.peek()
	if opTok.kind != tkOp || !isComparisonOp(opTok.text) {
		return nil, fmt.Errorf("selector: expected a comparison operator, got %q", opTok.text)
	}
	p.pos++
	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	return compareNode{op: opTok.text, left: left, right: right}, nil
}

func (p *selParser) parseOperand() (operand, error) {
	// A leading +/- applies to the following numeric literal.
	if t := p.peek(); t.kind == tkOp && (t.text == "-" || t.text == "+") {
		p.pos++
		nt := p.next()
		if nt.kind != tkNumber {
			return operand{}, fmt.Errorf("selector: %q must precede a number", t.text)
		}
		n, err := strconv.ParseFloat(nt.text, 64)
		if err != nil {
			return operand{}, fmt.Errorf("selector: invalid number %q", nt.text)
		}
		if t.text == "-" {
			n = -n
		}
		return operand{kind: opNumber, num: n}, nil
	}
	t := p.next()
	switch t.kind {
	case tkIdent:
		return operand{kind: opIdent, text: t.text}, nil
	case tkString:
		return operand{kind: opString, text: t.text}, nil
	case tkNumber:
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return operand{}, fmt.Errorf("selector: invalid number %q", t.text)
		}
		return operand{kind: opNumber, num: n}, nil
	case tkKeybrd:
		if t.text == "TRUE" {
			return operand{kind: opBool, b: true}, nil
		}
		if t.text == "FALSE" {
			return operand{kind: opBool, b: false}, nil
		}
	}
	return operand{}, fmt.Errorf("selector: expected an operand, got %q", t.text)
}

func isComparisonOp(s string) bool {
	switch s {
	case "=", "<>", ">", "<", ">=", "<=":
		return true
	}
	return false
}

// likeToRegexp converts a JMS LIKE pattern (with % and _ wildcards and an
// optional escape character) into an anchored regular expression.
func likeToRegexp(pattern, escape string) (*regexp.Regexp, error) {
	var esc rune
	hasEsc := false
	if escape != "" {
		esc = []rune(escape)[0]
		hasEsc = true
	}
	var sb strings.Builder
	// (?s) so '.' (from _) and '.*' (from %) match newlines too — JMS LIKE
	// wildcards span any character, including embedded newlines.
	sb.WriteString("(?s)^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if hasEsc && c == esc && i+1 < len(runes) {
			sb.WriteString(regexp.QuoteMeta(string(runes[i+1])))
			i++
			continue
		}
		switch c {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
