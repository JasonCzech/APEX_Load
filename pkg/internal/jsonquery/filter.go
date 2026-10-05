package jsonquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Filters ([?( ... )]) select the array elements, or object member values, for
// which a predicate over the current node (@) holds. The supported predicate
// language covers what recorded scripts use: @-relative paths (.name,
// ['name'], [n]), comparisons (== != < <= > >=) against string, number,
// boolean and null literals, bare paths as existence tests, and ! && || with
// parentheses. Script expressions and functions remain unsupported.

const maxFilterDepth = 32

type filter interface {
	matches(node any) bool
}

type filterOr struct{ left, right filter }
type filterAnd struct{ left, right filter }
type filterNot struct{ operand filter }
type filterExists struct{ path []step }
type filterCompare struct {
	left, right filterOperand
	op          string
}

// filterOperand is either an @-relative path or a literal value.
type filterOperand struct {
	path    []step
	isPath  bool
	literal any
}

func (f filterOr) matches(node any) bool  { return f.left.matches(node) || f.right.matches(node) }
func (f filterAnd) matches(node any) bool { return f.left.matches(node) && f.right.matches(node) }
func (f filterNot) matches(node any) bool { return !f.operand.matches(node) }

func (f filterExists) matches(node any) bool {
	_, found := resolveRelative(node, f.path)
	return found
}

func (f filterCompare) matches(node any) bool {
	left, leftFound := f.left.resolve(node)
	right, rightFound := f.right.resolve(node)
	if !leftFound || !rightFound {
		return false // an element without the field matches no comparison
	}
	if leftNumber, ok := filterNumber(left); ok {
		if rightNumber, ok := filterNumber(right); ok {
			return compareOrdered(leftNumber, rightNumber, f.op)
		}
	}
	if leftText, ok := left.(string); ok {
		if rightText, ok := right.(string); ok {
			return compareOrdered(leftText, rightText, f.op)
		}
	}
	equal := filterScalarEqual(left, right)
	switch f.op {
	case "==":
		return equal
	case "!=":
		return !equal
	default:
		return false
	}
}

func (operand filterOperand) resolve(node any) (any, bool) {
	if operand.isPath {
		return resolveRelative(node, operand.path)
	}
	return operand.literal, true
}

func resolveRelative(node any, path []step) (any, bool) {
	current := node
	for _, s := range path {
		matches := applyStep(s, current, nil)
		if len(matches) == 0 {
			return nil, false
		}
		current = matches[0]
	}
	return current, true
}

func filterNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil
}

func filterScalarEqual(left, right any) bool {
	switch typed := left.(type) {
	case bool:
		other, ok := right.(bool)
		return ok && typed == other
	case nil:
		return right == nil
	default:
		return false
	}
}

func compareOrdered[T float64 | string](left, right T, op string) bool {
	switch op {
	case "==":
		return left == right
	case "!=":
		return left != right
	case "<":
		return left < right
	case "<=":
		return left <= right
	case ">":
		return left > right
	case ">=":
		return left >= right
	default:
		return false
	}
}

// parseFilter parses the text after "[?(" and returns the predicate and the
// remainder after the closing ")]".
func parseFilter(rest, expr string) (filter, string, error) {
	parser := &filterParser{text: rest, expr: expr}
	predicate, err := parser.parseOr(0)
	if err != nil {
		return nil, "", err
	}
	parser.skipSpace()
	if !strings.HasPrefix(parser.text[parser.position:], ")]") {
		return nil, "", fmt.Errorf("%w: %q", ErrUnsupported, expr)
	}
	return predicate, parser.text[parser.position+2:], nil
}

type filterParser struct {
	text     string
	position int
	expr     string
}

func (p *filterParser) unsupported() error {
	return fmt.Errorf("%w: %q", ErrUnsupported, p.expr)
}

func (p *filterParser) skipSpace() {
	for p.position < len(p.text) && (p.text[p.position] == ' ' || p.text[p.position] == '\t') {
		p.position++
	}
}

func (p *filterParser) consume(token string) bool {
	p.skipSpace()
	if strings.HasPrefix(p.text[p.position:], token) {
		p.position += len(token)
		return true
	}
	return false
}

func (p *filterParser) parseOr(depth int) (filter, error) {
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for p.consume("||") {
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		left = filterOr{left, right}
	}
	return left, nil
}

func (p *filterParser) parseAnd(depth int) (filter, error) {
	left, err := p.parseUnary(depth)
	if err != nil {
		return nil, err
	}
	for p.consume("&&") {
		right, err := p.parseUnary(depth)
		if err != nil {
			return nil, err
		}
		left = filterAnd{left, right}
	}
	return left, nil
}

func (p *filterParser) parseUnary(depth int) (filter, error) {
	if depth > maxFilterDepth {
		return nil, p.unsupported()
	}
	p.skipSpace()
	if strings.HasPrefix(p.text[p.position:], "!=") {
		return nil, p.unsupported()
	}
	if p.consume("!") {
		operand, err := p.parseUnary(depth + 1)
		if err != nil {
			return nil, err
		}
		return filterNot{operand}, nil
	}
	if p.consume("(") {
		inner, err := p.parseOr(depth + 1)
		if err != nil {
			return nil, err
		}
		if !p.consume(")") {
			return nil, p.unsupported()
		}
		return inner, nil
	}
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.consume(op) {
			right, err := p.parseOperand()
			if err != nil {
				return nil, err
			}
			if !left.isPath && !right.isPath {
				return nil, p.unsupported()
			}
			return filterCompare{left: left, right: right, op: op}, nil
		}
	}
	if !left.isPath {
		return nil, p.unsupported()
	}
	return filterExists{path: left.path}, nil
}

func (p *filterParser) parseOperand() (filterOperand, error) {
	p.skipSpace()
	rest := p.text[p.position:]
	switch {
	case strings.HasPrefix(rest, "@"):
		p.position++
		path, err := p.parseRelativePath()
		if err != nil {
			return filterOperand{}, err
		}
		return filterOperand{path: path, isPath: true}, nil
	case strings.HasPrefix(rest, "'") || strings.HasPrefix(rest, `"`):
		text, length, err := scanQuoted(rest)
		if err != nil {
			return filterOperand{}, p.unsupported()
		}
		p.position += length
		return filterOperand{literal: text}, nil
	case strings.HasPrefix(rest, "true"):
		p.position += 4
		return filterOperand{literal: true}, nil
	case strings.HasPrefix(rest, "false"):
		p.position += 5
		return filterOperand{literal: false}, nil
	case strings.HasPrefix(rest, "null"):
		p.position += 4
		return filterOperand{literal: nil}, nil
	}
	end := 0
	for end < len(rest) && strings.IndexByte("+-0123456789.eE", rest[end]) >= 0 {
		end++
	}
	if end == 0 {
		return filterOperand{}, p.unsupported()
	}
	if _, err := strconv.ParseFloat(rest[:end], 64); err != nil {
		return filterOperand{}, p.unsupported()
	}
	p.position += end
	return filterOperand{literal: json.Number(rest[:end])}, nil
}

// parseRelativePath reads the .name, ['name'] and [n] segments after @.
func (p *filterParser) parseRelativePath() ([]step, error) {
	var path []step
	for p.position < len(p.text) {
		rest := p.text[p.position:]
		switch {
		case strings.HasPrefix(rest, ".."):
			return nil, p.unsupported()
		case strings.HasPrefix(rest, "."):
			end := 1
			for end < len(rest) && isPathNameByte(rest[end]) {
				end++
			}
			if end == 1 {
				return nil, p.unsupported()
			}
			path = append(path, step{kind: stepField, name: rest[1:end]})
			p.position += end
		case strings.HasPrefix(rest, "['") || strings.HasPrefix(rest, `["`):
			text, length, err := scanQuoted(rest[1:])
			if err != nil || !strings.HasPrefix(rest[1+length:], "]") {
				return nil, p.unsupported()
			}
			path = append(path, step{kind: stepField, name: text})
			p.position += 1 + length + 1
		case strings.HasPrefix(rest, "["):
			close := strings.IndexByte(rest, ']')
			if close < 0 {
				return nil, p.unsupported()
			}
			index, err := strconv.Atoi(strings.TrimSpace(rest[1:close]))
			if err != nil {
				return nil, p.unsupported()
			}
			path = append(path, step{kind: stepIndex, index: index})
			p.position += close + 1
		default:
			return path, nil
		}
	}
	return path, nil
}

func isPathNameByte(c byte) bool {
	return c == '_' || c == '$' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// scanQuoted reads a single- or double-quoted string with backslash escapes
// at the start of text, returning its value and the bytes consumed.
func scanQuoted(text string) (string, int, error) {
	if text == "" || (text[0] != '\'' && text[0] != '"') {
		return "", 0, errors.New("not a quoted string")
	}
	quote := text[0]
	var value strings.Builder
	for index := 1; index < len(text); index++ {
		c := text[index]
		if c == '\\' && index+1 < len(text) {
			index++
			value.WriteByte(text[index])
			continue
		}
		if c == quote {
			return value.String(), index + 1, nil
		}
		value.WriteByte(c)
	}
	return "", 0, errors.New("unterminated quoted string")
}
