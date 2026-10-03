package aurapayload

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type parser struct {
	source string
	pos    int
	budget *budget
}

func parseDocument(source string, b *budget) (*document, error) {
	if len(source) > MaxBodyBytes {
		return nil, fail(ByteLimit)
	}
	if !utf8.ValidString(source) {
		return nil, fail(InvalidUTF8)
	}
	if err := b.addWork(len(source)); err != nil {
		return nil, err
	}
	p := parser{source: source, budget: b}
	root, err := p.node(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(source) {
		return nil, fail(InvalidJSON)
	}
	return &document{source: source, root: root}, nil
}
func (p *parser) space() {
	for p.pos < len(p.source) {
		switch p.source[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *parser) node(depth int) (*node, error) {
	if depth > MaxDepth {
		return nil, fail(DepthLimit)
	}
	if err := p.budget.token(); err != nil {
		return nil, err
	}
	p.space()
	if p.pos >= len(p.source) {
		return nil, fail(InvalidJSON)
	}
	n := &node{start: p.pos}
	switch p.source[p.pos] {
	case '{':
		n.kind = "object"
		p.pos++
		p.space()
		keys := make(map[string]struct{})
		if p.take('}') {
			n.end = p.pos
			return n, nil
		}
		for {
			p.space()
			if p.pos >= len(p.source) || p.source[p.pos] != '"' {
				return nil, fail(InvalidJSON)
			}
			if err := p.budget.token(); err != nil {
				return nil, err
			}
			key, err := p.string()
			if err != nil {
				return nil, err
			}
			if _, duplicate := keys[key]; duplicate {
				return nil, fail(DuplicateKey)
			}
			keys[key] = struct{}{}
			p.space()
			if !p.take(':') {
				return nil, fail(InvalidJSON)
			}
			value, err := p.node(depth + 1)
			if err != nil {
				return nil, err
			}
			n.members = append(n.members, member{key: key, value: value})
			p.space()
			if p.take('}') {
				break
			}
			if !p.take(',') {
				return nil, fail(InvalidJSON)
			}
		}
	case '[':
		n.kind = "array"
		p.pos++
		p.space()
		if p.take(']') {
			n.end = p.pos
			return n, nil
		}
		for {
			value, err := p.node(depth + 1)
			if err != nil {
				return nil, err
			}
			n.elements = append(n.elements, value)
			p.space()
			if p.take(']') {
				break
			}
			if !p.take(',') {
				return nil, fail(InvalidJSON)
			}
		}
	case '"':
		n.kind = "string"
		value, err := p.string()
		if err != nil {
			return nil, err
		}
		n.value = value
	case 't':
		if !p.literal("true") {
			return nil, fail(InvalidJSON)
		}
		n.kind = "boolean"
		n.value = "true"
	case 'f':
		if !p.literal("false") {
			return nil, fail(InvalidJSON)
		}
		n.kind = "boolean"
		n.value = "false"
	case 'n':
		if !p.literal("null") {
			return nil, fail(InvalidJSON)
		}
		n.kind = "null"
		n.value = "null"
	default:
		n.kind = "number"
		for p.pos < len(p.source) && strings.ContainsRune("-+0123456789.eE", rune(p.source[p.pos])) {
			p.pos++
		}
		raw := p.source[n.start:p.pos]
		if len(raw) == 0 || !json.Valid([]byte(raw)) {
			return nil, fail(InvalidJSON)
		}
		n.value = raw
	}
	n.end = p.pos
	return n, nil
}
func (p *parser) take(c byte) bool {
	if p.pos < len(p.source) && p.source[p.pos] == c {
		p.pos++
		return true
	}
	return false
}
func (p *parser) literal(value string) bool {
	if strings.HasPrefix(p.source[p.pos:], value) {
		p.pos += len(value)
		return true
	}
	return false
}
func (p *parser) string() (string, error) {
	start := p.pos
	if !p.take('"') {
		return "", fail(InvalidJSON)
	}
	for p.pos < len(p.source) {
		c := p.source[p.pos]
		p.pos++
		if c == '"' {
			var value string
			if json.Unmarshal([]byte(p.source[start:p.pos]), &value) != nil {
				return "", fail(InvalidJSON)
			}
			return value, nil
		}
		if c < 0x20 {
			return "", fail(InvalidJSON)
		}
		if c != '\\' {
			continue
		}
		if p.pos >= len(p.source) {
			return "", fail(InvalidJSON)
		}
		escape := p.source[p.pos]
		p.pos++
		switch escape {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			value, ok := p.hex4()
			if !ok {
				return "", fail(InvalidJSON)
			}
			if value >= 0xD800 && value <= 0xDBFF {
				if !p.take('\\') || !p.take('u') {
					return "", fail(InvalidSurrogate)
				}
				low, ok := p.hex4()
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return "", fail(InvalidSurrogate)
				}
			} else if value >= 0xDC00 && value <= 0xDFFF {
				return "", fail(InvalidSurrogate)
			}
		default:
			return "", fail(InvalidJSON)
		}
	}
	return "", fail(InvalidJSON)
}
func (p *parser) hex4() (uint16, bool) {
	if len(p.source)-p.pos < 4 {
		return 0, false
	}
	var value uint16
	for i := 0; i < 4; i++ {
		c := p.source[p.pos+i]
		var digit byte
		switch {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			return 0, false
		}
		value = value<<4 | uint16(digit)
	}
	p.pos += 4
	return value, true
}
