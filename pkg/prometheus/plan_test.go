package prometheus

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func TestCollectPlanResumesTerminalWindowsUnderOneRequestBudget(t *testing.T) {
	to := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	from := to.Add(-2 * time.Second)
	calls := 0
	c := fixture(t, Config{MaxRequestsPerPoll: 2, MaxSeries: 1, MaxPoints: 8}, func(r *http.Request) (*http.Response, error) {
		calls++
		r.ParseForm()
		fresh := strings.HasPrefix(r.Form.Get("query"), "timestamp")
		if r.URL.Path == "/api/v1/query_range" {
			start, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
			end, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
			values := []any{}
			for at := start; at <= end; at++ {
				value := "1"
				if fresh {
					value = strconv.FormatFloat(at, 'f', 3, 64)
				}
				values = append(values, []any{at, value})
			}
			return success(map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "values": values}}}), nil
		}
		at, _ := strconv.ParseFloat(r.Form.Get("time"), 64)
		value := "1"
		if fresh {
			value = strconv.FormatFloat(at, 'f', 3, 64)
		}
		return success(map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{"job": "sut"}, "value": []any{at, value}}}}), nil
	})
	s := Selection{ProjectID: "p", StepSeconds: 1, SettleSeconds: 1, Queries: []Query{{ID: "up", Name: "up", Expression: `up{job="sut"}`, FreshnessExpression: `timestamp(up{job="sut"})`, TerminalExpression: `sum_over_time(up{job="sut"}[{{window.duration}}])`, AllowedLabels: []string{"job"}}}}
	windows := []monitorprovider.EvaluationWindow{{QueryID: "up", From: from, To: to}, {QueryID: "up", From: from.Add(time.Second), To: to}}
	first, err := c.CollectPlan(context.Background(), s, from, to, "run", windows)
	if err != nil || first.Progress.Complete || first.Progress.NextQuery != 1 || first.Progress.TotalQueries != 3 || len(first.Points) != 3 || calls != 2 {
		t.Fatalf("first: %+v %v calls%d", first, err, calls)
	}
	s.QueryOffset = first.Progress.NextQuery
	s.QueryTime = first.Progress.NextTime
	second, err := c.CollectPlan(context.Background(), s, from, to, "run", windows)
	if err != nil || second.Progress.Complete || second.Progress.NextQuery != 2 || len(second.Points) != 1 || calls != 4 || second.Coverage["terminal:0:up"].Status != "complete" {
		t.Fatalf("second: %+v %v calls%d", second, err, calls)
	}
	s.QueryOffset = second.Progress.NextQuery
	s.QueryTime = second.Progress.NextTime
	third, err := c.CollectPlan(context.Background(), s, from, to, "run", windows)
	if err != nil || !third.Progress.Complete || third.Progress.NextQuery != 3 || len(third.Points) != 1 || calls != 6 || third.Coverage["terminal:1:up"].Status != "complete" {
		t.Fatalf("third: %+v %v calls%d", third, err, calls)
	}
	if second.Points[0].Provider.DefinitionHash != third.Points[0].Provider.DefinitionHash || !second.Points[0].Provider.BucketStart.Equal(from) || !third.Points[0].Provider.BucketStart.Equal(from.Add(time.Second)) {
		t.Fatal("windows lost exact provenance")
	}
}

func TestCollectPlanValidatesWindowsAndAdvancesNoData(t *testing.T) {
	to := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	from := to.Add(-time.Second)
	c := fixture(t, Config{MaxSeries: 1}, func(r *http.Request) (*http.Response, error) {
		return success(map[string]any{"resultType": "vector", "result": []any{}}), nil
	})
	s := Selection{ProjectID: "p", QueryOffset: 1, Queries: []Query{{ID: "up", Name: "up", Expression: "up", TerminalExpression: `avg_over_time(up[{{window.duration}}])`}}}
	window := monitorprovider.EvaluationWindow{QueryID: "up", From: from, To: to}
	result, err := c.CollectPlan(context.Background(), s, from, to, "run", []monitorprovider.EvaluationWindow{window})
	if err != nil || !result.Progress.Complete || result.Progress.NextQuery != 2 || result.Coverage["terminal:0:up"].Status != "no_data" {
		t.Fatalf("no data: %+v %v", result, err)
	}
	for _, bad := range []monitorprovider.EvaluationWindow{{QueryID: "unknown", From: from, To: to}, {QueryID: "up", From: from.Add(-time.Second), To: to}, {QueryID: "up", From: from, To: to.Add(time.Second)}, {QueryID: "up", From: to, To: from}} {
		if _, err := c.CollectPlan(context.Background(), s, from, to, "run", []monitorprovider.EvaluationWindow{bad}); err == nil {
			t.Fatal("unapproved terminal window accepted")
		}
	}
}
