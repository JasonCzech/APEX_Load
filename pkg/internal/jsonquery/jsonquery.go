// Package jsonquery evaluates the JSONPath subset that LoadRunner scripts use
// with lr_eval_json, lr_json_get_values and lr_json_stringify. Documents keep
// object member order and number spelling, so "first match" and re-encoding
// behave like the recorded document rather than like a Go map.
package jsonquery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// MaxDocumentBytes bounds one parsed document.
	MaxDocumentBytes = 8 << 20
	// MaxDepth bounds nesting during parsing, matching and encoding.
	MaxDepth = 128
	// MaxMatches bounds one query's result set.
	MaxMatches = 10000
	// maxVisits bounds nodes visited by one query. An 8 MiB document holds at
	// most a few million nodes, so a single recursive descent always fits;
	// chained descents over overlapping subtrees are what this stops.
	maxVisits = 1 << 23
)

// ErrUnsupported marks valid JSONPath syntax outside the supported subset
// (filters, slices, unions, script expressions).
var ErrUnsupported = errors.New("unsupported JSONPath syntax")

// Member is one object member in document order.
type Member struct {
	Key   string
	Value any
}

// Object is a JSON object with its members in document order. Values are
// Object, []any, string, json.Number, bool or nil.
type Object []Member

// Decode parses one JSON document, preserving member order and number text.
func Decode(data []byte) (any, error) {
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("JSON document exceeds %d bytes", MaxDocumentBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("invalid JSON: trailing data after the document")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := Object{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("invalid JSON: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid JSON: object key is not a string")
			}
			value, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object = append(object, Member{Key: key, Value: value})
		}
		if _, err := decoder.Token(); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return object, nil
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("invalid JSON: unexpected %q", delimiter)
	}
}

// Encode writes value as JSON. indent "" produces compact output.
func Encode(value any, indent string) (string, error) {
	var out bytes.Buffer
	if err := encodeValue(&out, value, indent, 0); err != nil {
		return "", err
	}
	return out.String(), nil
}

func encodeValue(out *bytes.Buffer, value any, indent string, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", MaxDepth)
	}
	newline := func(level int) {
		if indent != "" {
			out.WriteByte('\n')
			out.WriteString(strings.Repeat(indent, level))
		}
	}
	switch typed := value.(type) {
	case Object:
		out.WriteByte('{')
		for index, member := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			encodeString(out, member.Key)
			out.WriteByte(':')
			if indent != "" {
				out.WriteByte(' ')
			}
			if err := encodeValue(out, member.Value, indent, depth+1); err != nil {
				return err
			}
		}
		if len(typed) > 0 {
			newline(depth)
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			if err := encodeValue(out, item, indent, depth+1); err != nil {
				return err
			}
		}
		if len(typed) > 0 {
			newline(depth)
		}
		out.WriteByte(']')
	case string:
		encodeString(out, typed)
	case json.Number:
		out.WriteString(typed.String())
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func encodeString(out *bytes.Buffer, value string) {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	out.Truncate(out.Len() - 1) // Encode appends a newline.
}

// ValueString renders a matched value the way lr_json_get_values saves it:
// strings without quotes, other scalars as their JSON text, and objects and
// arrays as compact JSON.
func ValueString(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	return Encode(value, "")
}

type stepKind int

const (
	stepField stepKind = iota
	stepIndex
	stepWildcard
)

type step struct {
	kind      stepKind
	name      string
	index     int
	recursive bool
}

// Path is a parsed query.
type Path struct {
	steps []step
}

// Parse accepts $, .name, ['name'] / ["name"], [n] (negative counts from the
// end), [*] and .* wildcards, and ..name / ..* / ..[n] recursive descent.
func Parse(expr string) (Path, error) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "$") {
		return Path{}, errors.New("JSONPath must start with $")
	}
	rest := expr[1:]
	var steps []step
	for rest != "" {
		recursive := false
		switch {
		case strings.HasPrefix(rest, ".."):
			recursive = true
			rest = rest[2:]
			if strings.HasPrefix(rest, "[") {
				break
			}
			name, remaining, err := dotSegment(rest, expr)
			if err != nil {
				return Path{}, err
			}
			steps = append(steps, segmentStep(name, true))
			rest = remaining
			continue
		case strings.HasPrefix(rest, "."):
			name, remaining, err := dotSegment(rest[1:], expr)
			if err != nil {
				return Path{}, err
			}
			steps = append(steps, segmentStep(name, false))
			rest = remaining
			continue
		}
		if !strings.HasPrefix(rest, "[") {
			return Path{}, fmt.Errorf("unexpected %q in JSONPath %q", rest[:1], expr)
		}
		parsed, remaining, err := bracketSegment(rest, expr)
		if err != nil {
			return Path{}, err
		}
		parsed.recursive = recursive
		steps = append(steps, parsed)
		rest = remaining
	}
	return Path{steps: steps}, nil
}

func segmentStep(name string, recursive bool) step {
	if name == "*" {
		return step{kind: stepWildcard, recursive: recursive}
	}
	return step{kind: stepField, name: name, recursive: recursive}
}

func dotSegment(rest, expr string) (string, string, error) {
	end := strings.IndexAny(rest, ".[")
	if end < 0 {
		end = len(rest)
	}
	name := rest[:end]
	if name == "" {
		return "", "", fmt.Errorf("empty field name in JSONPath %q", expr)
	}
	if strings.ContainsAny(name, "()?@,: ") {
		return "", "", fmt.Errorf("%w: %q", ErrUnsupported, expr)
	}
	return name, rest[end:], nil
}

func bracketSegment(rest, expr string) (step, string, error) {
	body := rest[1:]
	if strings.HasPrefix(body, "'") || strings.HasPrefix(body, `"`) {
		quote := body[0]
		var name strings.Builder
		for index := 1; index < len(body); index++ {
			c := body[index]
			if c == '\\' && index+1 < len(body) {
				index++
				name.WriteByte(body[index])
				continue
			}
			if c == quote {
				if index+1 >= len(body) || body[index+1] != ']' {
					return step{}, "", fmt.Errorf("%w: %q", ErrUnsupported, expr)
				}
				return step{kind: stepField, name: name.String()}, body[index+2:], nil
			}
			name.WriteByte(c)
		}
		return step{}, "", fmt.Errorf("unterminated quoted name in JSONPath %q", expr)
	}
	end := strings.IndexByte(body, ']')
	if end < 0 {
		return step{}, "", fmt.Errorf("unterminated bracket in JSONPath %q", expr)
	}
	content := strings.TrimSpace(body[:end])
	remaining := body[end+1:]
	if content == "*" {
		return step{kind: stepWildcard}, remaining, nil
	}
	index, err := strconv.Atoi(content)
	if err != nil {
		return step{}, "", fmt.Errorf("%w: %q", ErrUnsupported, expr)
	}
	return step{kind: stepIndex, index: index}, remaining, nil
}

// Select returns the matches of path in document order. A positive limit
// returns at most that many and stops searching once found, so a first-match
// query never fails on a large result set. limit <= 0 asks for every match and
// fails if there are more than MaxMatches rather than truncating silently.
// Intermediate node sets are bounded by the visit budget.
func (path Path) Select(document any, limit int) ([]any, error) {
	all := limit <= 0 || limit > MaxMatches
	if all {
		limit = MaxMatches + 1
	}
	visits := 0
	current := []any{document}
	for stepIndex, s := range path.steps {
		stopAt := maxVisits
		if stepIndex == len(path.steps)-1 {
			stopAt = limit
		}
		var next []any
		for _, node := range current {
			var err error
			if s.recursive {
				next, err = applyRecursive(s, node, 0, &visits, next, stopAt)
				if err != nil {
					return nil, err
				}
			} else {
				next = applyStep(s, node, next)
			}
			if len(next) >= stopAt {
				break
			}
		}
		if stopAt == maxVisits && len(next) >= maxVisits {
			return nil, fmt.Errorf("JSONPath query visits more than %d nodes", maxVisits)
		}
		if len(next) > stopAt {
			next = next[:stopAt]
		}
		current = next
		if len(current) == 0 {
			return nil, nil
		}
	}
	if all && len(current) > MaxMatches {
		return nil, fmt.Errorf("JSONPath matches exceed %d", MaxMatches)
	}
	if len(current) > limit {
		current = current[:limit]
	}
	return current, nil
}

// applyRecursive applies s to node and every descendant in document order,
// without materializing the descendant set, until out holds stopAt matches.
func applyRecursive(s step, node any, depth int, visits *int, out []any, stopAt int) ([]any, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", MaxDepth)
	}
	*visits++
	if *visits > maxVisits {
		return nil, fmt.Errorf("JSONPath query visits more than %d nodes", maxVisits)
	}
	out = applyStep(s, node, out)
	if len(out) >= stopAt {
		return out, nil
	}
	var err error
	switch typed := node.(type) {
	case Object:
		for _, member := range typed {
			if out, err = applyRecursive(s, member.Value, depth+1, visits, out, stopAt); err != nil || len(out) >= stopAt {
				return out, err
			}
		}
	case []any:
		for _, item := range typed {
			if out, err = applyRecursive(s, item, depth+1, visits, out, stopAt); err != nil || len(out) >= stopAt {
				return out, err
			}
		}
	}
	return out, nil
}

func applyStep(s step, node any, out []any) []any {
	switch s.kind {
	case stepField:
		if object, ok := node.(Object); ok {
			for _, member := range object {
				if member.Key == s.name {
					out = append(out, member.Value)
				}
			}
		}
	case stepIndex:
		if array, ok := node.([]any); ok {
			index := s.index
			if index < 0 {
				index += len(array)
			}
			if index >= 0 && index < len(array) {
				out = append(out, array[index])
			}
		}
	case stepWildcard:
		switch typed := node.(type) {
		case Object:
			for _, member := range typed {
				out = append(out, member.Value)
			}
		case []any:
			out = append(out, typed...)
		}
	}
	return out
}
