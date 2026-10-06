package appdynamics

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppDynamicsAnalyticsRuntimeScope(t *testing.T) {
	for _, tc := range []struct {
		name, query, want string
	}{
		{"plain", "SELECT count(*) AS value FROM transactions", `SELECT count(*) AS value FROM transactions WHERE applicationId = 1 AND apexRunID = "run_123"`},
		{"or expression", `SELECT count(*) AS value FROM transactions WHERE error = true OR responseTime > 10 LIMIT 100`, `SELECT count(*) AS value FROM transactions WHERE (error = true OR responseTime > 10) AND applicationId = 1 AND apexRunID = "run_123" LIMIT 100`},
		{"quoted keyword", `SELECT count(*) AS value FROM transactions WHERE transactionName = "ORDER BY" ORDER BY value DESC`, `SELECT count(*) AS value FROM transactions WHERE (transactionName = "ORDER BY") AND applicationId = 1 AND apexRunID = "run_123" ORDER BY value DESC`},
		{"function filter", `SELECT filter(count(*), WHERE responseTime > 1) AS value FROM transactions SINCE 10 minutes`, `SELECT filter(count(*), WHERE responseTime > 1) AS value FROM transactions WHERE applicationId = 1 AND apexRunID = "run_123" SINCE 10 minutes`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := AnalyticsQuery{Query: tc.query, ApplicationID: "1", ApplicationIDField: "applicationId", RunIDField: "apexRunID"}
			got, err := CompileAnalyticsQuery(q, "run_123", true)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	q := AnalyticsQuery{Query: "SELECT requestGUID FROM transactions", ApplicationID: "1", ApplicationIDField: "applicationId", RunIDField: "apexRunID"}
	for _, value := range []string{`run" OR true`, `run' OR true`, "run\nfoo", ""} {
		if _, err := CompileAnalyticsQuery(q, value, true); err == nil {
			t.Fatal("unbounded runtime value accepted")
		}
	}
	query, err := CompileAnalyticsQuery(q, "ignored", false)
	if err != nil || query != "SELECT requestGUID FROM transactions WHERE applicationId = 1" {
		t.Fatal(query, err)
	}
	for _, field := range []string{`app; DROP`, `apexRunID OR true`, `*`, `app\nname`} {
		q.ApplicationIDField = field
		if _, err := CompileAnalyticsQuery(q, "run", true); err == nil {
			t.Fatal("unsafe field accepted")
		}
	}
	q.ApplicationIDField, q.ApplicationName = "", `Services "North"`
	query, err = CompileAnalyticsQuery(q, "run", true)
	if err != nil || !strings.Contains(query, `application = "Services \"North\""`) {
		t.Fatal(query, err)
	}
	for _, bad := range []string{`SELECT count(*) AS value FROM transactions WHERE`, `SELECT count(*) AS value FROM transactions WHERE name = "unterminated`, `SELECT count(*) AS value FROM transactions WHERE x = (1`, `SELECT count(*) AS value FROM transactions FROM logs`} {
		q.Query = bad
		if _, err := CompileAnalyticsQuery(q, "run", true); err == nil {
			t.Fatalf("malformed query accepted: %s", bad)
		}
	}
}

func TestAppDynamicsAnalyticsFilteredSamplesAndApplicationIdentity(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	var query string
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "analytics.example" {
			return jsonResponse(200, `[{"id":1,"name":"Services"}]`), nil
		}
		body, _ := io.ReadAll(r.Body)
		query = string(body)
		return jsonResponse(200, `[{"fields":[{"label":"eventTimestamp"},{"label":"applicationId"},{"label":"requestGUID"}],"results":[[`+jsonNumber(from.UnixMilli())+`,1,"01234567-89ab-cdef-0123-456789abcdef"]]}]`), nil
	})
	q := AnalyticsQuery{Name: "transactions", ApplicationID: "1", ApplicationIDField: "applicationId", RunIDField: "apexRunID", Query: "SELECT eventTimestamp, applicationId, requestGUID FROM transactions"}
	selection := Selection{ProjectID: "p", Applications: []string{"1"}, RunHeader: true, HeaderOrigins: []string{"https://service.example"}, Analytics: []AnalyticsQuery{q}}
	result, err := c.Collect(context.Background(), selection, from, from.Add(time.Minute), "run123")
	if err != nil || len(result.Evidence) != 1 || !strings.Contains(query, `applicationId = 1 AND apexRunID = "run123"`) {
		t.Fatal(result, err, query)
	}
	d := result.Evidence[0]
	if d.Properties["correlation"] != "run_header" || d.Properties["run_id"] != "run123" || d.Properties["requestguid"] != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatal(d)
	}
	q.ApplicationIDField, q.ApplicationName = "", "wrong application"
	if err := c.validateAnalyticsApplication(context.Background(), q); err == nil {
		t.Fatal("unverified application name accepted")
	}
	q.ApplicationName = "Services"
	if err := c.validateAnalyticsApplication(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	c = fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"fields":[{"label":"applicationId"},{"label":"value"}],"results":[[2,0]]}]`), nil
	})
	q.ApplicationName, q.ApplicationIDField = "", "applicationId"
	if _, _, _, err := c.analytics(context.Background(), q, "p", "r", from, from.Add(time.Minute), time.Now().UTC(), 120, false); err == nil {
		t.Fatal("misattributed upstream application accepted")
	}
}

func TestAppDynamicsAnalyticsAndMetricNameUniqueness(t *testing.T) {
	s := Selection{ProjectID: "p", Applications: []string{"1"}, Metrics: []MetricSelection{{Name: "calls", Path: "calls", ApplicationID: "1"}}, Analytics: []AnalyticsQuery{{Name: "calls", Query: "SELECT COUNT(*) AS value FROM transactions", ApplicationID: "1", ApplicationIDField: "applicationId"}}}
	if s.Validate() == nil {
		t.Fatal("ambiguous source series name accepted")
	}
	s.Analytics[0].Name = "transactions"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Analytics[0].ApplicationIDField = ""
	if s.Validate() == nil {
		t.Fatal("unscoped Analytics query accepted")
	}
}
