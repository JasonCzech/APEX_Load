package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestTypedParametersAreTokensAndPreserveDefinitionIdentity(t *testing.T) {
	malicious := `sut"} or vector(9000) #`
	q := Query{ID: "latency", Name: "Latency", Expression: `rate(requests_total{job={{param.job}}}[{{param.range}}]) > {{param.since}}`, TerminalExpression: `increase(requests_total{job={{param.job}}}[{{window.duration}}])`, Parameters: map[string]Parameter{"job": {Type: "label_value", Value: malicious}, "range": {Type: "duration", Value: "1h30m"}, "since": {Type: "timestamp", Value: "2026-10-03T12:00:00.125Z"}}}
	originalHash := DefinitionHash(q)
	rendered, err := materializeQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Expression, "{{") || !strings.Contains(rendered.Expression, "[1h30m]") || !strings.Contains(rendered.Expression, "1791028800.125") {
		t.Fatalf("rendered: %s", rendered.Expression)
	}
	parsed, err := parseExpression(rendered.Expression)
	if err != nil {
		t.Fatal(err)
	}
	selectors := 0
	parser.Inspect(parsed, func(node parser.Node, _ []parser.Node) error {
		if v, ok := node.(*parser.VectorSelector); ok {
			selectors++
			for _, m := range v.LabelMatchers {
				if m.Name == "job" && m.Value != malicious {
					t.Fatalf("parameter changed the label literal: %q", m.Value)
				}
			}
		}
		return nil
	})
	if selectors != 1 || DefinitionHash(q) != originalHash || DefinitionHash(rendered) == originalHash {
		t.Fatal("materialization changed identity or injected a selector")
	}
	empty := Query{Expression: `up{job={{param.job}}}`, Parameters: map[string]Parameter{"job": {Type: "label_value", Value: ""}}}
	value, err := materializeQuery(empty)
	if err != nil || value.Expression != `up{job=""}` {
		t.Fatalf("empty label: %+v %v", value, err)
	}
}

func TestTypedParameterValidationRejectsUnapprovedText(t *testing.T) {
	cases := []struct{ name, expression, kind, value string }{
		{"quoted", `up{job="{{param.value}}"}`, "label_value", "sut"},
		{"concatenated", `up_{{param.value}}`, "label_value", "sut"},
		{"suffix", `rate(up[{{param.value}}s])`, "duration", "1m"},
		{"arbitrary", `{{param.value}}`, "promql", "vector(1)"},
		{"durationLimit", `rate(up[{{param.value}}])`, "duration", "2d"},
		{"durationZero", `rate(up[{{param.value}}])`, "duration", "0s"},
		{"durationGoOnly", `rate(up[{{param.value}}])`, "duration", "1us"},
		{"timestampOffset", `time() > {{param.value}}`, "timestamp", "2026-10-03T12:00:00+00:00"},
		{"negativeTimestamp", `time() > {{param.value}}`, "timestamp", "1969-01-01T00:00:00Z"},
		{"lineBreak", `up{job={{param.value}}}`, "label_value", "sut\nprivate"},
		{"unknown", `up{job={{param.other}}}`, "label_value", "sut"},
		{"unused", `up`, "label_value", "sut"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := materializeQuery(Query{Expression: tc.expression, Parameters: map[string]Parameter{"value": {Type: tc.kind, Value: tc.value}}})
			if err == nil {
				t.Fatal("unsafe binding accepted")
			}
		})
	}
}

func TestJobPayloadBoundIncludesEscapingAndCombinedConfiguration(t *testing.T) {
	s := Selection{ProjectID: "p"}
	for i := 0; i < 20; i++ {
		s.Queries = append(s.Queries, Query{ID: fmt.Sprintf("q%d", i), Name: fmt.Sprintf("n%d", i), Expression: `up{job="` + strings.Repeat("a", 1200) + `"}`, FreshnessExpression: `timestamp(up{job="` + strings.Repeat("a", 1200) + `"})`})
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	c := Config{Endpoint: "https://prom.example", CAPEM: strings.Repeat("a", 20000)}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if (JobConfig{c, s}).Validate() == nil {
		t.Fatal("combined oversized signed input accepted")
	}
	for i := range s.Queries {
		s.Queries[i].Expression = `up{job="` + strings.Repeat("<", 1200) + `"}`
	}
	if s.Validate() == nil {
		t.Fatal("JSON escaping exceeded encoded bound without rejection")
	}
	for _, id := range []string{"query", "targets", "alerts"} {
		if (Selection{ProjectID: "p", Queries: []Query{{ID: id, Name: "metric", Expression: "up"}}}).Validate() == nil {
			t.Fatalf("reserved query ID %s accepted", id)
		}
	}
}

func TestTypedBindingsReachTransportWithoutChangingPointProvenance(t *testing.T) {
	from := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	q := Query{ID: "up", Name: "up", Expression: `up{job={{param.job}}}`, TerminalExpression: `avg_over_time(up{job={{param.job}}}[{{window.duration}}])`, Parameters: map[string]Parameter{"job": {Type: "label_value", Value: "sut"}}, AllowedLabels: []string{"job"}}
	c := fixture(t, Config{MaxSeries: 1}, func(r *http.Request) (*http.Response, error) {
		r.ParseForm()
		expr := r.Form.Get("query")
		if strings.Contains(expr, "{{") || !strings.Contains(expr, `job="sut"`) {
			t.Fatalf("unsafe or unresolved transport expression: %s", expr)
		}
		if r.URL.Path == "/api/v1/query" {
			stamp, _ := strconv.ParseFloat(r.Form.Get("time"), 64)
			return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "value": []any{stamp, "1"}}}}), nil
		}
		stamp, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
		return success(map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "values": []any{[]any{stamp, "1"}}}}}), nil
	})
	s := Selection{ProjectID: "p", Queries: []Query{q}, StepSeconds: 30, SettleSeconds: 1}
	result, err := c.Collect(context.Background(), s, from, from.Add(time.Second), "run")
	if err != nil || len(result.Points) != 1 {
		t.Fatalf("range: %+v %v", result, err)
	}
	terminal, err := c.CollectTerminal(context.Background(), s, from, from.Add(time.Second), "run")
	if err != nil || len(terminal.Points) != 1 {
		t.Fatalf("terminal: %+v %v", terminal, err)
	}
	if result.Points[0].Provider.DefinitionHash != DefinitionHash(q) || terminal.Points[0].Provider.DefinitionHash != DefinitionHash(q) {
		t.Fatal("runtime substitution changed frozen query identity")
	}
}

func TestTypedFixedTimestampIsStableAndTerminalAnchorsAreRejected(t *testing.T) {
	q := Query{ID: "up", Name: "up", Expression: `up @ {{param.at}}`, Parameters: map[string]Parameter{"at": {Type: "timestamp", Value: "2026-10-03T12:00:00.125Z"}}}
	rendered, err := materializeQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateExpression(rendered.Expression); err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{`up @ start()`, `up @ end()`} {
		if validateExpression(expression) == nil {
			t.Fatal("request-relative anchor accepted")
		}
	}
	for _, expression := range []string{`increase(up[{{window.duration}}] @ 100)`, `avg_over_time((up)[{{window.duration}}:1s] @ 100)`} {
		if validateTerminalExpression(expression) == nil {
			t.Fatal("fixed terminal anchor accepted")
		}
	}
}

func TestFreshnessCompanionRequiresAtomicRequestAndDeadlineBudgets(t *testing.T) {
	s := Selection{ProjectID: "p", Queries: []Query{{ID: "up", Name: "up", Expression: "up", FreshnessExpression: "timestamp(up)"}}}
	for _, cfg := range []Config{{Endpoint: "https://prom.example", MaxRequestsPerPoll: 1}, {Endpoint: "https://prom.example", RequestsPerMinute: 2, MaxPollSeconds: 30}, {Endpoint: "https://prom.example", RequestsPerMinute: 60, MaxPollSeconds: 1}} {
		if (JobConfig{cfg, s}).Validate() == nil {
			t.Fatal("unfinishable freshness pair accepted")
		}
	}
	if err := (JobConfig{Config{Endpoint: "https://prom.example", MaxRequestsPerPoll: 2, RequestsPerMinute: 3, MaxPollSeconds: 30}, s}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshnessTemporalSettingsMatchFixedAnchorsAndOffsets(t *testing.T) {
	for _, pair := range [][2]string{{`up @ 100`, `timestamp(up @ 100)`}, {`rate(up[5m] offset 1m)`, `timestamp(up offset 1m)`}, {`avg_over_time(up[5m:1m] @ 100 offset 1m)`, `timestamp(up @ 100 offset 1m)`}, {`sum(rate(up[5m]))`, `timestamp(up)`}} {
		if err := validateFreshnessTemporalSettings(pair[0], pair[1]); err != nil {
			t.Fatalf("matching temporal settings rejected %+v: %v", pair, err)
		}
	}
	for _, pair := range [][2]string{{`up @ 100`, `timestamp(up)`}, {`up @ 100`, `timestamp(up @ 101)`}, {`up offset 1m`, `timestamp(up)`}, {`up`, `timestamp(up offset 1m)`}, {`up @ 100 + up`, `timestamp(up @ 100)`}, {`avg_over_time(up[5m:1m] @ 100 offset 1m)`, `timestamp(up @ 100)`}} {
		if validateFreshnessTemporalSettings(pair[0], pair[1]) == nil {
			t.Fatalf("mismatched temporal settings accepted %+v", pair)
		}
	}
}

func TestReservedCoverageQueryIDs(t *testing.T) {
	for _, id := range []string{"query", "targets", "alerts", "targets_polling", "alerts_polling", "targets_last_success", "alerts_last_success", "retention"} {
		t.Run(id, func(t *testing.T) {
			if err := (Selection{ProjectID: "p", Queries: []Query{{ID: id, Name: "metric", Expression: "up"}}}).Validate(); err == nil {
				t.Fatal("saved query can overwrite generated coverage")
			}
		})
	}
	for _, id := range []string{"target_rate", "targets_polling_rate", "alerts_last_success_age", "retention_bytes", "query_time"} {
		if err := (Selection{ProjectID: "p", Queries: []Query{{ID: id, Name: "metric", Expression: "up"}}}).Validate(); err != nil {
			t.Fatalf("unrelated query ID rejected: %s: %v", id, err)
		}
	}
}
