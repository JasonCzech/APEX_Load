package appdynamics

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAppDynamicsAnalyticsAggregationSemantics(t *testing.T) {
	count, sum := 2.0, 10.0
	for _, tc := range []struct {
		name, query, statistic string
		eligible, window       bool
	}{
		{"whole gauge", "SELECT count(*) AS value FROM transactions", "value", true, true},
		{"timestamp gauge", "SELECT eventTimestamp, cpu AS value FROM transactions", "value", true, false},
		{"bucket alias", "SELECT series(eventTimestamp, '1m') AS bucket, cpu AS value FROM transactions", "value", true, false},
		{"default average", "SELECT avg(responseTime) AS value FROM transactions", "value", false, true},
		{"average missing projections", "SELECT avg(responseTime) AS value FROM transactions", "average", false, true},
		{"weighted average", "SELECT avg(responseTime) AS value, count(*) AS count, sum(responseTime) AS sum FROM transactions", "average", true, true},
		{"primitive weights", "SELECT value, count, sum FROM custom_bucket_metrics", "average", true, true},
		{"percentile", "SELECT percentile(responseTime, 95) AS value FROM transactions", "value", false, true},
		{"quoted percentile function", "SELECT `percentile`(responseTime, 95) AS value FROM transactions", "value", false, true},
		{"quoted text", `SELECT "avg(value) percentile(value) AS count" AS text, cpu AS value FROM transactions`, "value", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := AnalyticsQuery{Query: tc.query, Statistic: tc.statistic}
			if got := AnalyticsAggregationEligible(q, analysis.QualityValid, &count, &sum); got != tc.eligible {
				t.Fatalf("eligible=%v", got)
			}
			if got := AnalyticsQueryWindow(q); got != tc.window {
				t.Fatalf("window=%v", got)
			}
			if AnalyticsAggregationEligible(q, analysis.QualityMissing, &count, &sum) {
				t.Fatal("missing observation was eligible")
			}
		})
	}
	q := AnalyticsQuery{Query: "SELECT avg(responseTime) AS value, count(*) AS count, sum(responseTime) AS sum FROM transactions", Statistic: "average"}
	zero := 0.0
	if AnalyticsAggregationEligible(q, analysis.QualityValid, &zero, &sum) || AnalyticsAggregationEligible(q, analysis.QualityValid, &count, nil) {
		t.Fatal("invalid weighting metadata was eligible")
	}
	for _, query := range []string{"", "FROM", "FROM events", "DELETE FROM events", "SELECT FROM events", "SELECT value FROM events; DELETE FROM events"} {
		if AnalyticsAggregationEligible(AnalyticsQuery{Query: query}, analysis.QualityValid, &count, &sum) {
			t.Fatal("invalid projection was eligible", query)
		}
	}
}

func TestAppDynamicsAnalyticsQueryWindowProvenance(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	q := AnalyticsQuery{Name: "latency", ApplicationID: "1", ApplicationIDField: "applicationId", Kind: "metric", Query: "SELECT avg(responseTime) AS value FROM transactions"}
	c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"fields":[{"label":"value"}],"results":[[5]]}]`), nil
	})
	_, points, _, err := c.analytics(context.Background(), q, "p", "r", from, from.Add(60*time.Second), time.Now().UTC(), 120, false)
	if err != nil || len(points) != 1 || !points[0].Provider.QueryWindow || points[0].Provider.AggregationEligible {
		t.Fatal(points, err)
	}
	c = fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `[{"fields":[{"label":"bucket"},{"label":"value"}],"results":[[`+jsonNumber(from.UnixMilli())+`,5]]}]`), nil
	})
	q.BucketSeconds = 60
	if _, _, _, err := c.analytics(context.Background(), q, "p", "r", from, from.Add(60*time.Second), time.Now().UTC(), 120, false); err == nil {
		t.Fatal("unapproved timestamp projection was accepted")
	}
}

func TestAppDynamicsCorrelationDestinationOrigins(t *testing.T) {
	for _, origin := range []string{"http://service.example", "https://service.example", "http://service.example:8080/"} {
		selection := Selection{ProjectID: "p", Applications: []string{"1"}, RunHeader: true, HeaderOrigins: []string{origin}}
		if err := selection.Validate(); err != nil {
			t.Fatal(origin, err)
		}
	}
	for _, origin := range []string{"ftp://service.example", "http://user:password@service.example", "http://service.example/path", "http://service.example?token=secret", "http://service.example#fragment"} {
		selection := Selection{ProjectID: "p", Applications: []string{"1"}, RunHeader: true, HeaderOrigins: []string{origin}}
		if selection.Validate() == nil {
			t.Fatal("unsafe correlation origin accepted", origin)
		}
	}
	if (Config{Endpoint: "http://controller.example", AccountName: "account"}).Validate() == nil {
		t.Fatal("plain HTTP Controller was permitted")
	}
}
