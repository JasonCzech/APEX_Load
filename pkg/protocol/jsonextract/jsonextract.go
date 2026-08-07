// Package jsonextract provides the value-extraction engine shared by the network
// and database protocols (and available to any protocol that extracts values from
// a response body). It supports JSONPath-lite, JMESPath, regex, and boundary
// extraction. These functions previously existed as byte-identical per-protocol
// copies (extractNetwork*/extractDatabase*).
package jsonextract

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jmespath/go-jmespath"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

// ErrNotFound distinguishes a valid expression that found no response value
// from malformed configuration or an invalid response representation.
var ErrNotFound = errors.New("extraction value not found")

// JSONPath evaluates a JSONPath-lite expression (dotted keys with optional
// [n] indexing, leading "$"/"$." stripped) against a JSON body.
func JSONPath(expr string, body []byte) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", fmt.Errorf("jsonpath expression is required")
	}
	rootOnly := expr == "$"
	switch {
	case rootOnly:
		expr = ""
	case strings.HasPrefix(expr, "$."):
		expr = strings.TrimPrefix(expr, "$.")
		if expr == "" {
			return "", fmt.Errorf("invalid jsonpath: empty segment after root")
		}
		if strings.HasPrefix(expr, "[") {
			return "", fmt.Errorf("invalid jsonpath: array index must follow the root directly")
		}
	case strings.HasPrefix(expr, "$"):
		expr = strings.TrimPrefix(expr, "$")
		if !strings.HasPrefix(expr, "[") {
			return "", fmt.Errorf("invalid jsonpath: expected dot or array index after root")
		}
	case strings.HasPrefix(expr, "."):
		return "", fmt.Errorf("invalid jsonpath: leading empty segment")
	}
	if strings.HasSuffix(expr, ".") || strings.Contains(expr, "..") {
		return "", fmt.Errorf("invalid jsonpath: empty path segment")
	}
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	if expr == "" {
		return Stringify(current), nil
	}
	for index, part := range strings.Split(expr, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("invalid jsonpath: empty path segment")
		}
		key, indexes, err := splitPathToken(part)
		if err != nil {
			return "", fmt.Errorf("invalid jsonpath %q: %w", expr, err)
		}
		if key == "" && len(indexes) > 0 && index > 0 {
			return "", fmt.Errorf("invalid jsonpath %q: array index must follow its value directly", expr)
		}
		if key != "" {
			object, ok := current.(map[string]any)
			if !ok {
				return "", fmt.Errorf("%w: jsonpath %q expected object", ErrNotFound, expr)
			}
			next, ok := object[key]
			if !ok {
				return "", fmt.Errorf("%w: jsonpath %q", ErrNotFound, expr)
			}
			current = next
		}
		for _, index := range indexes {
			items, ok := current.([]any)
			if !ok || index < 0 || index >= len(items) {
				return "", fmt.Errorf("%w: jsonpath %q index out of range", ErrNotFound, expr)
			}
			current = items[index]
		}
	}
	return Stringify(current), nil
}

func splitPathToken(token string) (string, []int, error) {
	key := token
	indexes := []int{}
	if bracket := strings.Index(token, "["); bracket >= 0 {
		key = token[:bracket]
		rest := token[bracket:]
		for rest != "" {
			if !strings.HasPrefix(rest, "[") {
				return "", nil, fmt.Errorf("unexpected suffix %q", rest)
			}
			end := strings.Index(rest, "]")
			if end <= 1 {
				return "", nil, fmt.Errorf("malformed array index")
			}
			indexText := rest[1:end]
			index, err := strconv.Atoi(indexText)
			if err != nil || index < 0 {
				return "", nil, fmt.Errorf("malformed array index %q", indexText)
			}
			indexes = append(indexes, index)
			rest = rest[end+1:]
		}
	}
	return key, indexes, nil
}

// JMESPath evaluates a JMESPath expression against a JSON body.
func JMESPath(expr string, body []byte) (string, error) {
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		return "", err
	}
	value, err := jmespath.Search(expr, current)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", fmt.Errorf("%w: jmespath %q", ErrNotFound, expr)
	}
	return Stringify(value), nil
}

// Regex returns the first submatch (group 1 if present, else the whole match) of
// expr against body.
func Regex(expr string, body []byte) (string, error) {
	cached := cachedRegex(expr)
	if cached.compileErr != nil {
		return "", cached.compileErr
	}
	match := cached.re.FindSubmatch(body)
	if len(match) == 0 {
		return "", fmt.Errorf("%w: regex %q", ErrNotFound, expr)
	}
	if len(match) > 1 {
		return string(match[1]), nil
	}
	return string(match[0]), nil
}

type cachedRegexp struct {
	re         *regexp.Regexp
	compileErr error
}

// regexCompileCache memoizes compiled extraction patterns. The cache is
// process-global and outlives every run, and the expressions reaching it are
// script-supplied — parameterised, and in a hostile script arbitrarily
// shaped. Bounding entry count alone is therefore not enough: a handful of
// multi-megabyte patterns would pin their key strings (and compiled programs)
// for the life of the process. boundedcache bounds entries, per-key bytes and
// total key bytes, evicting FIFO on any of them, and declines to retain an
// oversized key at all — cachedRegex still returns a correctly compiled
// pattern for those, it just recompiles them each time.
var regexCompileCache = boundedcache.NewStringCache[cachedRegexp](0, 0, 0)

// regexCompileCount counts the regexp.Compile calls cachedRegex actually makes.
// It exists so the memoization can be observed at all: an admitted cache entry
// is immutable until eviction, so reading the cache back cannot distinguish
// "the compiled program was reused" from "it was recompiled and the fresh copy
// thrown away". Production never reads this; the increment sits on the cold
// miss path next to the compile it counts.
var regexCompileCount atomic.Int64

func cachedRegex(expr string) cachedRegexp {
	if cached, ok := regexCompileCache.Load(expr); ok {
		return cached
	}
	regexCompileCount.Add(1)
	re, err := regexp.Compile(expr)
	actual, _ := regexCompileCache.LoadOrStore(expr, cachedRegexp{re: re, compileErr: err})
	return actual
}

// Boundary extracts the text between a left and right boundary, expressed as
// "left|||right".
func Boundary(expr string, body []byte) (string, error) {
	parts := strings.Split(expr, "|||")
	if len(parts) != 2 {
		return "", fmt.Errorf("boundary expression must be left|||right")
	}
	text := string(body)
	start := strings.Index(text, parts[0])
	if start < 0 {
		return "", fmt.Errorf("%w: left boundary", ErrNotFound)
	}
	start += len(parts[0])
	end := strings.Index(text[start:], parts[1])
	if end < 0 {
		return "", fmt.Errorf("%w: right boundary", ErrNotFound)
	}
	return text[start : start+end], nil
}

// Stringify renders an extracted JSON value as a string: "" for nil, the string
// itself for strings, fmt.Sprint for scalars, and compact JSON for anything else.
func Stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64, bool, int, int64:
		return fmt.Sprint(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(data)
	}
}
