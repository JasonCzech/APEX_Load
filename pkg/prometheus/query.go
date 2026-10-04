package prometheus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// The parser is pinned in go.mod. No unstable upstream parse endpoint is used.
// Request-relative anchors are rejected because splitting a range query would
// otherwise silently change its meaning. Fixed numeric anchors are stable across
// chunks; negative offsets and unsupported timestamps are rejected.
func parseExpression(expr string) (parser.Expr, error) {
	if len(expr) == 0 || len(expr) > 16<<10 || strings.ContainsAny(expr, "\x00\r\n") {
		return nil, errors.New("PromQL expression must contain 1-16384 bytes on one line")
	}
	parsed, err := parser.NewParser(parser.Options{}).ParseExpr(expr)
	if err != nil {
		return nil, errors.New("invalid PromQL expression")
	}
	if parsed.Type() != parser.ValueTypeVector && parsed.Type() != parser.ValueTypeScalar {
		return nil, errors.New("PromQL monitoring requires a numeric vector or scalar expression")
	}
	nodes := 0
	var invalid error
	parser.Inspect(parsed, func(n parser.Node, _ []parser.Node) error {
		if n == nil {
			return nil
		}
		nodes++
		if nodes > 256 {
			invalid = errors.New("PromQL expression exceeds complexity budget")
			return invalid
		}
		switch v := n.(type) {
		case *parser.VectorSelector:
			if invalidTimestamp(v.Timestamp) || v.StartOrEnd != 0 || v.OriginalOffset < 0 || v.OriginalOffset > 24*time.Hour {
				invalid = errors.New("PromQL relative time anchors and future or excessive offsets are unsupported")
			}
			if len(v.LabelMatchers) > 24 {
				invalid = errors.New("PromQL selector exceeds matcher budget")
			}
			for _, m := range v.LabelMatchers {
				if len(m.Name) > 128 || len(m.Value) > 2048 {
					invalid = errors.New("PromQL matcher exceeds bounds")
				}
			}
		case *parser.SubqueryExpr:
			if invalidTimestamp(v.Timestamp) || v.StartOrEnd != 0 || v.OriginalOffset < 0 || v.Range > 24*time.Hour || v.OriginalOffset > 24*time.Hour {
				invalid = errors.New("PromQL subquery exceeds the temporal budget")
			}
		case *parser.MatrixSelector:
			if v.Range > 24*time.Hour {
				invalid = errors.New("PromQL range selector exceeds one day")
			}
		case *parser.Call:
			if v.Func.Experimental {
				invalid = errors.New("experimental PromQL functions are unsupported")
			}
		}
		return invalid
	})
	if invalid != nil {
		return nil, invalid
	}
	return parsed, nil
}
func validateExpression(expr string) error { _, err := parseExpression(expr); return err }
func validateProfileSelection(c Config, s Selection) error {
	if c.BackendProfile != BackendAzureManaged {
		return nil
	}
	for _, q := range s.Queries {
		rendered, err := materializeQuery(q)
		if err != nil {
			return err
		}
		for _, expr := range []string{rendered.Expression, rendered.FreshnessExpression, rendered.TerminalExpression} {
			if expr == "" {
				continue
			}
			parsed, err := parseExpression(strings.ReplaceAll(expr, "{{window.duration}}", "1m"))
			if err != nil {
				return err
			}
			missing := false
			parser.Inspect(parsed, func(n parser.Node, _ []parser.Node) error {
				if v, ok := n.(*parser.VectorSelector); ok {
					explicit := v.Name != ""
					for _, m := range v.LabelMatchers {
						if m.Name == "__name__" && m.Type == labels.MatchEqual && m.Value != "" {
							explicit = true
						}
					}
					if !explicit {
						missing = true
					}
				}
				return nil
			})
			if missing {
				return errors.New("Azure managed queries require an explicit metric name in each selector")
			}
		}
	}
	return nil
}
func DefinitionHash(q Query) string {
	qbytes, _ := json.Marshal(q)
	sum := sha256.Sum256(qbytes)
	return hex.EncodeToString(sum[:])
}
func sensitiveLabel(label string) bool {
	name := strings.ToLower(label)
	return credential.SensitiveName(name) || strings.Contains(name, "email") || strings.Contains(name, "token") || strings.Contains(name, "password") || strings.Contains(name, "secret") || name == "url" || name == "uri" || name == "path" || name == "user" || name == "username" || name == "authorization"
}
func unixSeconds(t time.Time) string { return fmt.Sprintf("%.3f", float64(t.UnixMilli())/1000) }

func validateTerminalExpression(expr string) error {
	count := strings.Count(expr, "{{window.duration}}")
	if count < 1 || count > 8 {
		return errors.New("terminal expressions require 1-8 whole-window duration tokens")
	}
	// Collect normalized source durations first, then choose a marker distinct
	// from every literal range. Equivalent duration spellings cannot masquerade
	// as the template (e.g. 20m34.567s versus 1234567ms).
	base, err := parseExpression(strings.ReplaceAll(expr, "{{window.duration}}", "1ms"))
	if err != nil {
		return err
	}
	literalRanges := map[time.Duration]bool{}
	parser.Inspect(base, func(n parser.Node, _ []parser.Node) error {
		switch v := n.(type) {
		case *parser.MatrixSelector:
			literalRanges[v.Range] = true
		case *parser.SubqueryExpr:
			literalRanges[v.Range] = true
		}
		return nil
	})
	markerMilliseconds := int64(1234567)
	for literalRanges[time.Duration(markerMilliseconds)*time.Millisecond] {
		markerMilliseconds++
	}
	markerText := fmt.Sprintf("%dms", markerMilliseconds)
	replaced := strings.ReplaceAll(expr, "{{window.duration}}", markerText)
	if strings.Contains(replaced, "{{") || strings.Contains(replaced, "}}") {
		return errors.New("unsupported terminal expression template")
	}
	parsed, err := parseExpression(replaced)
	if err != nil {
		return err
	}
	marker := time.Duration(markerMilliseconds) * time.Millisecond
	valid := true
	vectors := 0
	parser.Inspect(parsed, func(n parser.Node, path []parser.Node) error {
		isRange := false
		switch rangeNode := n.(type) {
		case *parser.MatrixSelector:
			isRange = true
			if rangeNode.Range != marker {
				valid = false
			}
		case *parser.SubqueryExpr:
			isRange = true
			if rangeNode.Range != marker || rangeNode.OriginalOffset != 0 || rangeNode.Timestamp != nil || rangeNode.StartOrEnd != 0 {
				valid = false
			}
		}
		if isRange {
			// Nested ranges extend the dependency before the frozen window even
			// when both use the same duration token. A terminal dependency has
			// exactly one full-window range over an instant vector selector.
			for _, ancestor := range path {
				switch ancestor.(type) {
				case *parser.MatrixSelector, *parser.SubqueryExpr:
					valid = false
				}
			}
		}
		if vector, ok := n.(*parser.VectorSelector); ok {
			vectors++
			covered := false
			if vector.OriginalOffset != 0 || vector.Timestamp != nil || vector.StartOrEnd != 0 {
				valid = false
			}
			for _, ancestor := range path {
				switch v := ancestor.(type) {
				case *parser.MatrixSelector:
					covered = covered || v.Range == marker
				case *parser.SubqueryExpr:
					covered = covered || v.Range == marker
				}
			}
			if !covered {
				valid = false
			}
		}
		return nil
	})
	if !valid || vectors == 0 {
		return errors.New("terminal expressions require one exact-window range per vector dependency without nested ranges, fixed timestamps or offsets")
	}
	return nil
}

func parseEvidenceSelector(value string) ([]*labels.Matcher, error) {
	parsed, err := parseExpression(value)
	if err != nil {
		return nil, errors.New("diagnostic evidence requires a bounded label selector")
	}
	selector, ok := parsed.(*parser.VectorSelector)
	if !ok || selector.Timestamp != nil || selector.StartOrEnd != 0 || selector.OriginalOffset != 0 {
		return nil, errors.New("diagnostic evidence requires a vector label selector")
	}
	matchers := []*labels.Matcher{}
	for _, matcher := range selector.LabelMatchers {
		if matcher.Name == "__name__" {
			continue
		}
		if sensitiveLabel(matcher.Name) || !labelRE.MatchString(matcher.Name) {
			return nil, errors.New("diagnostic selector contains an unsafe label")
		}
		matchers = append(matchers, matcher)
	}
	if len(matchers) == 0 || len(matchers) > 12 {
		return nil, errors.New("diagnostic selector requires 1-12 explicit safe label matchers")
	}
	return matchers, nil
}

// QueryStatistic preserves administrator-declared numeric semantics. Only a
// declared terminal count is an interval total; rolling count evaluations are
// never promoted to totals or summed as whole-run evidence.
func QueryStatistic(q Query, terminal bool) string {
	if q.Semantics == "" {
		return "value"
	}
	if terminal && q.Semantics == "count" {
		return "total"
	}
	return q.Semantics
}

var parameterNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// materializeQuery only emits complete typed PromQL tokens. The original query
// remains the identity/provenance definition and is never replaced in storage.
func materializeQuery(q Query) (Query, error) {
	if len(q.Parameters) > 16 {
		return q, errors.New("query exceeds the 16 typed parameter limit")
	}
	values := map[string]string{}
	for name, parameter := range q.Parameters {
		if !parameterNameRE.MatchString(name) || len(parameter.Value) > 256 || !utf8.ValidString(parameter.Value) || strings.ContainsAny(parameter.Value, "\x00\r\n") {
			return q, errors.New("invalid typed query parameter")
		}
		switch parameter.Type {
		case "label_value":
			values[name] = strconv.Quote(parameter.Value)
		case "duration":
			duration, err := model.ParseDuration(parameter.Value)
			if err != nil || duration <= 0 || time.Duration(duration) > 24*time.Hour {
				return q, errors.New("typed duration must be positive and at most one day")
			}
			values[name] = duration.String()
		case "timestamp":
			timestamp, err := time.Parse(time.RFC3339Nano, parameter.Value)
			if err != nil || timestamp.IsZero() || !strings.HasSuffix(parameter.Value, "Z") || timestamp.Unix() < 0 {
				return q, errors.New("typed timestamp must be a UTC RFC3339 timestamp after the Unix epoch")
			}
			values[name] = unixSeconds(timestamp)
		default:
			return q, errors.New("unsupported query parameter type")
		}
	}
	used := map[string]bool{}
	render := func(expression string, terminal bool) (string, error) {
		if len(expression) > 16<<10 {
			return "", errors.New("PromQL template exceeds bounds")
		}
		var out strings.Builder
		var quote byte
		escaped := false
		for i := 0; i < len(expression); {
			if strings.HasPrefix(expression[i:], "{{") {
				closing := strings.Index(expression[i+2:], "}}")
				if closing < 0 || quote != 0 {
					return "", errors.New("typed parameter must be an unquoted complete PromQL token")
				}
				end := i + 2 + closing + 2
				token := expression[i+2 : i+2+closing]
				if (i > 0 && tokenAdjacent(expression[i-1])) || (end < len(expression) && tokenAdjacent(expression[end])) {
					return "", errors.New("typed parameter cannot concatenate PromQL tokens")
				}
				if terminal && token == "window.duration" {
					out.WriteString("{{window.duration}}")
				} else {
					if !strings.HasPrefix(token, "param.") {
						return "", errors.New("unsupported PromQL template")
					}
					name := strings.TrimPrefix(token, "param.")
					value, exists := values[name]
					if !exists {
						return "", errors.New("query parameter has no approved binding")
					}
					used[name] = true
					out.WriteString(value)
				}
				i = end
				continue
			}
			ch := expression[i]
			if quote != 0 {
				if escaped {
					escaped = false
				} else if ch == '\\' && quote != '`' {
					escaped = true
				} else if ch == quote {
					quote = 0
				}
			} else if ch == '"' || ch == '\'' || ch == '`' {
				quote = ch
			}
			out.WriteByte(ch)
			i++
		}
		rendered := out.String()
		if strings.Contains(rendered, "{{") && !terminal || strings.Contains(rendered, "}}") && !terminal {
			return "", errors.New("unsupported PromQL template")
		}
		if len(rendered) > 16<<10 {
			return "", errors.New("rendered PromQL expression exceeds bounds")
		}
		return rendered, nil
	}
	var err error
	q.Expression, err = render(q.Expression, false)
	if err != nil {
		return q, err
	}
	q.FreshnessExpression, err = render(q.FreshnessExpression, false)
	if err != nil {
		return q, err
	}
	q.TerminalExpression, err = render(q.TerminalExpression, true)
	if err != nil {
		return q, err
	}
	if len(used) != len(values) {
		return q, errors.New("typed query parameter has no expression reference")
	}
	return q, nil
}
func tokenAdjacent(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '.'
}

func invalidTimestamp(value *int64) bool {
	return value != nil && (*value < 0 || *value > 253402300799999)
}

// A freshness companion must use the same effective selector timestamps and
// offsets as the monitored expression. Range widths and aggregation functions
// may differ; they do not manufacture a newer source sample for historical data.
func validateFreshnessTemporalSettings(expression, freshness string) error {
	settings := func(value string) (map[string]bool, error) {
		parsed, err := parseExpression(value)
		if err != nil {
			return nil, err
		}
		observed := map[string]bool{}
		parser.Inspect(parsed, func(node parser.Node, path []parser.Node) error {
			if sub, ok := node.(*parser.SubqueryExpr); ok && sub.Timestamp != nil {
				observed[fmt.Sprintf("anchor:%d", *sub.Timestamp)] = true
			}
			vector, ok := node.(*parser.VectorSelector)
			if !ok {
				return nil
			}
			timestamp := vector.Timestamp
			if timestamp != nil {
				observed[fmt.Sprintf("anchor:%d", *timestamp)] = true
			}
			offset := vector.OriginalOffset
			for i := len(path) - 1; i >= 0; i-- {
				if sub, ok := path[i].(*parser.SubqueryExpr); ok {
					offset += sub.OriginalOffset
					if timestamp == nil && sub.Timestamp != nil {
						timestamp = sub.Timestamp
					}
				}
			}
			anchor := "current"
			if timestamp != nil {
				anchor = fmt.Sprintf("%d", *timestamp)
			}
			observed[fmt.Sprintf("%s:%d", anchor, offset)] = true
			return nil
		})
		return observed, nil
	}
	primary, err := settings(expression)
	if err != nil {
		return err
	}
	companion, err := settings(freshness)
	if err != nil {
		return err
	}
	if len(primary) != len(companion) {
		return errors.New("freshness companion must match query timestamps and offsets")
	}
	for signature := range primary {
		if !companion[signature] {
			return errors.New("freshness companion must match query timestamps and offsets")
		}
	}
	return nil
}
