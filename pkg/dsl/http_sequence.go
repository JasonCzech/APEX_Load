package dsl

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const (
	HTTPSequenceSequential            = "sequential"
	HTTPSequenceConcurrent            = "concurrent"
	MaxHTTPConcurrentChildren         = 256
	MaxHTTPConcurrency                = 16
	DefaultHTTPConcurrency            = 8
	MaxHTTPConcurrentPreparationBytes = 16 << 20
	MaxHTTPConcurrentPreparationWork  = 64 << 20
)

// HTTPSequenceExecutionSettings admits concurrency before child decoding or allocation.
// Present settings are validated even when sequential execution is selected.
func HTTPSequenceExecutionSettings(action Action) (string, int, error) {
	mode := HTTPSequenceSequential
	if raw, present := action.Params["execution_mode"]; present {
		var ok bool
		mode, ok = raw.(string)
		if !ok || (mode != HTTPSequenceSequential && mode != HTTPSequenceConcurrent) {
			return "", 0, fmt.Errorf("http_sequence execution_mode must be sequential or concurrent")
		}
	}
	count := 0
	switch requests := action.Params["requests"].(type) {
	case []any:
		count = len(requests)
	case []Action:
		count = len(requests)
	case []map[string]any:
		count = len(requests)
	}
	if mode == HTTPSequenceConcurrent && count > MaxHTTPConcurrentChildren {
		return mode, 0, fmt.Errorf("concurrent http_sequence permits at most %d child requests", MaxHTTPConcurrentChildren)
	}
	concurrency := min(DefaultHTTPConcurrency, count)
	if raw, present := action.Params["max_concurrency"]; present {
		n := float64(0)
		switch value := raw.(type) {
		case int:
			n = float64(value)
		case int64:
			n = float64(value)
		case float64:
			n = value
		case json.Number:
			if len(value) > 16 {
				return mode, 0, fmt.Errorf("http_sequence max_concurrency must be an integer from 1 to %d", MaxHTTPConcurrency)
			}
			var err error
			n, err = value.Float64()
			if err != nil {
				n = math.NaN()
			}
		default:
			n = math.NaN()
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < 1 || n > MaxHTTPConcurrency {
			return mode, 0, fmt.Errorf("http_sequence max_concurrency must be an integer from 1 to %d", MaxHTTPConcurrency)
		}
		concurrency = int(n)
	}
	return mode, concurrency, nil
}

func httpSequenceRawRequests(raw any) []any {
	switch values := raw.(type) {
	case []any:
		return values
	case []Action:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out
	case []map[string]any:
		out := make([]any, len(values))
		for i := range values {
			out[i] = values[i]
		}
		return out
	}
	return nil
}

// ValidateHTTPConcurrentChild excludes engine operations and waits from a request group.
func ValidateHTTPConcurrentChild(action Action) error {
	method := strings.ToUpper(strings.TrimSpace(action.Method))
	kind := strings.ToUpper(strings.TrimSpace(action.Type))
	if method == "" {
		method = kind
	}
	if err := httpprotocol.ValidateMethod(method); err != nil {
		return fmt.Errorf("concurrent child requires a supported HTTP method")
	}
	if kind != "" && kind != "REQUEST" && kind != method {
		return fmt.Errorf("concurrent child must be an HTTP request")
	}
	if action.Control != nil || action.BodyFile != "" {
		return fmt.Errorf("concurrent child must be an inline HTTP request")
	}
	if strings.TrimSpace(action.Target) == "" {
		return fmt.Errorf("concurrent child requires a target")
	}
	t := action.ThinkTime
	if t.MeanMs != 0 || t.StdMs != 0 || t.MinMs != 0 || t.MaxMs != 0 || t.MinPercent != 0 || t.MaxPercent != 0 {
		return fmt.Errorf("concurrent child think time must be zero; use the group think time")
	}
	return nil
}

// HTTPConcurrentPreparationSourceSize uses bounded, allocation-free admission
// before any typed-child JSON conversion or runtime interpolation.
func HTTPConcurrentPreparationSourceSize(values ...any) (int, error) {
	size, _, err := HTTPConcurrentPreparationSourceSizeWithWorkLimit(MaxHTTPConcurrentPreparationWork, values...)
	return size, err
}

// HTTPConcurrentPreparationSourceSizeWithWorkLimit charges inspection before
// scanning fields, and exposes work for callers inspecting multiple groups.
func HTTPConcurrentPreparationSourceSizeWithWorkLimit(maxWork int, values ...any) (size, work int, err error) {
	limit := min(maxWork, MaxHTTPConcurrentPreparationWork)
	if limit <= 0 {
		return 0, 0, fmt.Errorf("concurrent HTTP preparation work limit exceeded")
	}
	b := &httpConcurrentSourceBudget{remaining: MaxHTTPConcurrentPreparationBytes, nodes: 32768, workRemaining: limit}
	defer func() { work = limit - b.workRemaining }()
	for _, value := range values {
		if err := b.walk(reflect.ValueOf(value), 0, nil); err != nil {
			return 0, 0, err
		}
	}
	return MaxHTTPConcurrentPreparationBytes - b.remaining, 0, nil
}

type httpConcurrentSourceBudget struct{ remaining, nodes, workRemaining int }

func (b *httpConcurrentSourceBudget) charge(n int) error {
	if n < 0 || n > b.remaining {
		return fmt.Errorf("concurrent HTTP preparation byte limit exceeded")
	}
	if err := b.chargeWork(n); err != nil {
		return err
	}
	b.remaining -= n
	return nil
}
func (b *httpConcurrentSourceBudget) chargeWork(n int) error {
	if n < 0 || n > b.workRemaining {
		return fmt.Errorf("concurrent HTTP preparation work limit exceeded")
	}
	b.workRemaining -= n
	return nil
}

func (b *httpConcurrentSourceBudget) walk(v reflect.Value, depth int, visit func(string) error) error {
	if !v.IsValid() {
		return nil
	}
	b.nodes--
	if b.nodes < 0 || depth > 32 {
		return fmt.Errorf("concurrent HTTP preparation structure limit exceeded")
	}
	if err := b.charge(16); err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return b.walk(v.Elem(), depth+1, visit)
		}
	case reflect.String:
		value := v.String()
		// Reject oversized fields before UTF-8 validation or escaped-size scans.
		if len(value) > b.remaining || len(value) > b.workRemaining/2 {
			return fmt.Errorf("concurrent HTTP preparation byte/work limit exceeded")
		}
		if err := b.chargeWork(2 * len(value)); err != nil {
			return err
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("concurrent HTTP preparation requires UTF-8 source fields")
		}
		cost := 2
		for i := 0; i < len(value); i++ {
			switch c := value[i]; {
			case c < 0x20 || c == '<' || c == '>' || c == '&':
				cost += 6
			case c == '"' || c == '\\':
				cost += 2
			case c == 0xe2 && i+2 < len(value) && value[i+1] == 0x80 && (value[i+2] == 0xa8 || value[i+2] == 0xa9):
				// encoding/json escapes JavaScript line/paragraph separators.
				cost += 6
				i += 2
			default:
				cost++
			}
			if cost > b.remaining {
				return fmt.Errorf("concurrent HTTP preparation byte limit exceeded")
			}
		}
		if err := b.charge(cost); err != nil {
			return err
		}
		if visit != nil {
			return visit(value)
		}
	case reflect.Array, reflect.Slice:
		if v.Len() > b.nodes {
			return fmt.Errorf("concurrent HTTP preparation structure limit exceeded")
		}
		for i := 0; i < v.Len(); i++ {
			if err := b.walk(v.Index(i), depth+1, visit); err != nil {
				return err
			}
		}
	case reflect.Map:
		if v.Len() > b.nodes/2 || v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("concurrent HTTP preparation structure limit exceeded")
		}
		it := v.MapRange()
		for it.Next() {
			if err := b.walk(it.Key(), depth+1, visit); err != nil {
				return err
			}
			if err := b.walk(it.Value(), depth+1, visit); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				if err := b.walk(v.Field(i), depth+1, visit); err != nil {
					return err
				}
			}
		}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
	default:
		return fmt.Errorf("concurrent HTTP preparation requires JSON-shaped fields")
	}
	return nil
}

var httpConcurrentTemplate = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// HTTPConcurrentExtractorReference includes expanded all-mode outputs, whose
// previous-iteration values must not disguise a same-group dependency.
func HTTPConcurrentExtractorReference(name string, extractorName string, mode string) bool {
	if name == extractorName {
		return true
	}
	if mode != "all" {
		return false
	}
	if name == extractorName+"_count" {
		return true
	}
	suffix, ok := strings.CutPrefix(name, extractorName+"_")
	if !ok || suffix == "" {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return false
		}
	}
	return true
}

// ValidateHTTPConcurrentChildren is shared by source import and runtime.
// Dynamic names and values are checked again after runtime preparation.
// HTTPConcurrentExtractorOutputSource identifies only actual extractor names
// or all-mode positive numeric/count suffixes, without quadratic pair scans.
func HTTPConcurrentExtractorOutputSource(name string, outputs map[string]string) (string, bool) {
	if _, ok := outputs[name]; ok {
		return name, true
	}
	index := strings.LastIndexByte(name, '_')
	if index < 0 {
		return "", false
	}
	base := name[:index]
	mode, ok := outputs[base]
	if !ok || mode != "all" || !HTTPConcurrentExtractorReference(name, base, mode) {
		return "", false
	}
	return base, true
}

// HTTPConcurrentTemplateDependency scans a bounded decoded scalar once. It is
// usable by codec callbacks without importing the codec into the DSL package.
func HTTPConcurrentTemplateDependency(value string, outputs map[string]string, remaining *int, maxWork int) (int, error) {
	work := 2 * len(value)
	if work > maxWork || maxWork < 0 {
		return 0, fmt.Errorf("concurrent HTTP preparation work limit exceeded")
	}
	if remaining == nil || *remaining < 0 {
		return 0, fmt.Errorf("concurrent HTTP template occurrence limit exceeded")
	}
	matches := httpConcurrentTemplate.FindAllStringSubmatch(value, *remaining+1)
	if len(matches) > *remaining {
		return work, fmt.Errorf("concurrent HTTP template occurrence limit exceeded")
	}
	*remaining -= len(matches)
	for _, match := range matches {
		expr := strings.TrimSpace(match[1])
		if key, ok := strings.CutPrefix(expr, "var."); ok {
			if _, found := HTTPConcurrentExtractorOutputSource(strings.TrimSpace(key), outputs); found {
				return work, fmt.Errorf("concurrent HTTP requests cannot depend on group extractor outputs")
			}
		}
	}
	return work, nil
}

func ValidateHTTPConcurrentChildren(children []Action) error {
	_, err := ValidateHTTPConcurrentChildrenWithWorkLimit(children, MaxHTTPConcurrentPreparationWork)
	return err
}

// ValidateHTTPConcurrentChildrenWithWorkLimit shares the caller's cumulative
// admission-work budget and returns charged work even when admission fails.
func ValidateHTTPConcurrentChildrenWithWorkLimit(children []Action, maxWork int) (work int, err error) {
	limit := min(maxWork, MaxHTTPConcurrentPreparationWork)
	if limit <= 0 {
		return 0, fmt.Errorf("concurrent HTTP preparation work limit exceeded")
	}
	b := &httpConcurrentSourceBudget{remaining: MaxHTTPConcurrentPreparationBytes, nodes: 32768, workRemaining: limit}
	defer func() { work = limit - b.workRemaining }()
	if len(children) == 0 || len(children) > MaxHTTPConcurrentChildren {
		return 0, fmt.Errorf("concurrent HTTP child count is unsupported")
	}
	if err := b.walk(reflect.ValueOf(children), 0, nil); err != nil {
		return 0, err
	}
	outputs := make(map[string]string)
	for _, child := range children {
		if err := ValidateHTTPConcurrentChild(child); err != nil {
			return 0, err
		}
		for _, extractor := range child.Extractors {
			if _, exists := outputs[extractor.Name]; extractor.Name == "" || exists {
				return 0, fmt.Errorf("concurrent HTTP requests require unique nonempty extractor names")
			}
			outputs[extractor.Name] = extractor.Mode
		}
	}
	for name := range outputs {
		index := strings.LastIndexByte(name, '_')
		if index >= 0 {
			base := name[:index]
			if mode, ok := outputs[base]; ok && mode == "all" && HTTPConcurrentExtractorReference(name, base, mode) {
				return 0, fmt.Errorf("concurrent HTTP extractor output names overlap")
			}
		}
	}
	remaining := 4096
	visit := func(value string) error {
		work, err := HTTPConcurrentTemplateDependency(value, outputs, &remaining, b.workRemaining)
		b.workRemaining -= work
		return err
	}

	b.remaining = MaxHTTPConcurrentPreparationBytes
	b.nodes = 32768
	for _, child := range children {
		if err := b.walk(reflect.ValueOf(child), 0, visit); err != nil {
			return 0, err
		}
		text := child.Body
		body, encoded, decodeErr := DecodeBodyEncoding(child.Body, child.BodyEncoding, max(1, min(b.remaining, b.workRemaining)/2))
		if decodeErr != nil && !IsTrustedFullBodyBase64Template(child.Body) {
			return 0, fmt.Errorf("concurrent HTTP body decoding failed")
		}
		if encoded && decodeErr == nil {
			if err := b.charge(2 * len(body)); err != nil {
				return 0, err
			}
			text = string(body)
			if err := visit(text); err != nil {
				return 0, err
			}
		}
		if strings.Contains(text, "%") {
			// QueryUnescape's output cannot exceed the input length. Reserve before
			// materialization, including malformed percent input that may fail.
			if err := b.charge(len(text)); err != nil {
				return 0, err
			}
			decoded, decodeErr := url.QueryUnescape(text)
			if decodeErr == nil && decoded != text {
				if err := visit(decoded); err != nil {
					return 0, err
				}
			}
		}
	}
	return 0, nil
}
