package collectoragent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestCloudQueryApproval(t *testing.T) {
	good := CloudQuery{Region: "us-east-1", AccountID: "123456789012", Namespace: "AWS/EC2", Metrics: []CloudMetric{{Name: "CPUUtilization", Unit: "Percent", Statistic: "Average"}}}
	if err := validateCloudQuery("cloudwatch", &good); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CloudQuery){func(q *CloudQuery) { q.Region = "us-east-1.evil.test" }, func(q *CloudQuery) { q.AccountID = "" }, func(q *CloudQuery) { q.Metrics[0].Statistic = "p95" }, func(q *CloudQuery) { q.Metrics[0].Unit = "arbitrary" }, func(q *CloudQuery) { q.Metrics[0].Unit = "CountPerSecond" }, func(q *CloudQuery) { q.Metrics = append(q.Metrics, q.Metrics[0]) }} {
		q := good
		q.Metrics = append([]CloudMetric(nil), good.Metrics...)
		mutate(&q)
		if validateCloudQuery("cloudwatch", &q) == nil {
			t.Fatalf("unsafe approval: %+v", q)
		}
	}
	azure := CloudQuery{TenantID: "11111111-1111-1111-1111-111111111111", ClientID: "22222222-2222-2222-2222-222222222222", Identity: "workload", ResourceID: "/subscriptions/33333333-3333-3333-3333-333333333333/resourceGroups/test/providers/Microsoft.Compute/virtualMachines/test", Namespace: "Microsoft.Compute/virtualMachines", Metrics: good.Metrics}
	if err := validateCloudQuery("azure-monitor", &azure); err != nil {
		t.Fatal(err)
	}
	azure.ResourceID += "/../other"
	if validateCloudQuery("azure-monitor", &azure) == nil {
		t.Fatal("path traversal accepted")
	}
}
func TestCloudWatchEvidenceBoundsAndSemantics(t *testing.T) {
	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	q := &CloudQuery{Metrics: []CloudMetric{{Name: "Latency", Unit: "Seconds", Statistic: "Average"}}}
	body, _ := json.Marshal(map[string]any{"MetricDataResults": []any{map[string]any{"Id": "m0", "StatusCode": "Complete", "Timestamps": []int64{start.Unix()}, "Values": []float64{0.25}}}})
	points, err := decodeCloudWatch(body, q, start, end)
	if err != nil || len(points) != 1 {
		t.Fatalf("decode: %v %+v", err, points)
	}
	if *points[0].Value != 250 || points[0].Unit != "ms" || points[0].Kind != analysis.KindSummary {
		t.Fatalf("wrong semantics: %+v", points[0])
	}
	for _, body := range []string{`{"NextToken":"next"}`, `{"MetricDataResults":[{"Id":"m0","StatusCode":"PartialData"}]}`, `{"MetricDataResults":[{"Id":"other","StatusCode":"Complete"}]}`, `{"MetricDataResults":[{"Id":"m0","StatusCode":"Complete","Timestamps":[0],"Values":[1]}]}`} {
		if _, err := decodeCloudWatch([]byte(body), q, start, end); err == nil {
			t.Fatal("invalid cloud response accepted")
		}
	}
}
func TestAzureMonitorMissingIsNotZero(t *testing.T) {
	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	m := CloudMetric{Name: "CPU", Unit: "Percent", Statistic: "Average"}
	body := `{"interval":"PT1M","value":[{"name":{"value":"CPU"},"unit":"Percent","timeseries":[{"data":[{"timeStamp":"2026-10-01T01:00:00Z"},{"timeStamp":"2026-10-01T01:01:00Z","average":0}]}]}]}`
	points, err := decodeAzureMonitor([]byte(body), m, start, end)
	if err != nil || len(points) != 2 {
		t.Fatalf("%v %+v", err, points)
	}
	if points[0].Value != nil || points[0].Quality != analysis.QualityMissing || points[1].Value == nil || *points[1].Value != 0 {
		t.Fatal("missing converted to zero")
	}
	for _, changed := range []string{strings.Replace(body, "PT1M", "PT5M", 1), strings.Replace(body, "Percent", "Count", 1), strings.Replace(body, `"data":`, `"metadatavalues":[{}],"data":`, 1)} {
		if _, err := decodeAzureMonitor([]byte(changed), m, start, end); err == nil {
			t.Fatal("incompatible series accepted")
		}
	}
}
func TestCloudRequestsBoundRetriesAndResponses(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusTooManyRequests) }))
	defer server.Close()
	build := func() (*http.Request, error) { return http.NewRequest(http.MethodGet, server.URL, nil) }
	if _, err := cloudRequest(context.Background(), server.Client(), build); err == nil || requests != 3 {
		t.Fatalf("retry budget: %v %d", err, requests)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requests = 0
	if _, err := cloudRequest(ctx, server.Client(), build); err == nil || requests != 0 {
		t.Fatal("cancelled request executed")
	}
}
func TestCloudJobsDoNotRequireSealedCredentialsOrPollAtPreparation(t *testing.T) {
	target := AdapterTarget{Pack: "cloudwatch", Destination: "cloudwatch://ec2", Cloud: &CloudQuery{Region: "us-east-1", AccountID: "123456789012", Namespace: "AWS/EC2", Metrics: []CloudMetric{{Name: "CPU", Unit: "Percent", Statistic: "Average"}}}}
	cfg := Config{ControllerURL: "http://127.0.0.1:8080", CollectorID: "collector", Token: "token", Packs: []string{"cloudwatch"}, ControllerPublicKey: make([]byte, 32), AdapterTargets: []AdapterTarget{target}}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.approvedDestinations(); len(got) != 1 || got[0] != target.Destination {
		t.Fatal(got)
	}
	target.Cloud.Metrics[0].Name = "mutated"
	if a.cfg.AdapterTargets[0].Cloud.Metrics[0].Name != "CPU" {
		t.Fatal("operator approval mutated after construction")
	}
}

type cloudFixtureTransport func(*http.Request) (*http.Response, error)

func (f cloudFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloudWatchSignedRequestAndAccountFence(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "fixture-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fixture-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	q := &CloudQuery{Region: "us-east-1", AccountID: "123456789012", Namespace: "AWS/EC2", Metrics: []CloudMetric{{Name: "CPU", Unit: "Percent", Statistic: "Average"}}}
	metricCalls := 0
	account := q.AccountID
	client := &http.Client{Transport: cloudFixtureTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Errorf("request is not signed")
		}
		body := ""
		contentType := "text/xml"
		switch r.URL.Host {
		case "sts.us-east-1.amazonaws.com":
			body = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Account>` + account + `</Account><Arn>arn:aws:iam::` + account + `:role/collector</Arn><UserId>fixture</UserId></GetCallerIdentityResult></GetCallerIdentityResponse>`
		case "monitoring.us-east-1.amazonaws.com":
			metricCalls++
			if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" || r.Header.Get("Content-Encoding") != "amz-1.0" || r.Header.Get("X-Amz-Target") != "GraniteServiceVersion20100801.GetMetricData" {
				t.Errorf("invalid JSON transport headers: %v", r.Header)
			}
			var payload struct {
				StartTime, EndTime int64
				MetricDataQueries  []json.RawMessage
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.StartTime != start.Unix() || payload.EndTime != start.Add(5*time.Minute).Unix() || len(payload.MetricDataQueries) != 1 {
				t.Errorf("wrong query: %+v %v", payload, err)
			}
			encoded, _ := json.Marshal(map[string]any{"MetricDataResults": []any{map[string]any{"Id": "m0", "StatusCode": "Complete", "Timestamps": []int64{start.Unix()}, "Values": []float64{0}}}})
			body, contentType = string(encoded), "application/json"
		default:
			t.Fatalf("unapproved credential destination: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	points, err := collectCloudWatch(context.Background(), client, q, start, start.Add(5*time.Minute))
	if err != nil || len(points) != 1 || metricCalls != 1 {
		t.Fatalf("collection: %v %+v calls=%d", err, points, metricCalls)
	}
	account = "999999999999"
	if _, err := collectCloudWatch(context.Background(), client, q, start, start.Add(5*time.Minute)); err == nil || metricCalls != 1 {
		t.Fatalf("wrong account queried metrics: %v calls=%d", err, metricCalls)
	}
}

func TestCloudWindowExcludesPartialMinutesWithoutDroppingWholeFirstMinute(t *testing.T) {
	now := time.Date(2026, 10, 1, 1, 5, 30, 0, time.UTC)
	for _, tc := range []struct{ started, want time.Time }{
		{now.Add(-time.Hour), now.Truncate(time.Minute).Add(-5 * time.Minute)},
		{now.Truncate(time.Minute).Add(-2 * time.Minute), now.Truncate(time.Minute).Add(-2 * time.Minute)},
		{now.Truncate(time.Minute).Add(-2*time.Minute + time.Second), now.Truncate(time.Minute).Add(-time.Minute)},
	} {
		start, end := cloudCollectionWindow(tc.started, now)
		if !start.Equal(tc.want) || !end.Equal(now.Truncate(time.Minute)) || start.Before(tc.started) {
			t.Fatalf("window %s-%s for %s", start, end, tc.started)
		}
	}
}

func TestCloudMissingBucketCanArriveLater(t *testing.T) {
	for _, pack := range []string{"cloudwatch", "azure-monitor"} {
		t.Run(pack, func(t *testing.T) {
			a := &Agent{}
			job := RunJob{ID: "job", Pack: pack}
			point := analysis.MetricPoint{MetricName: "CPU", TimestampUTC: time.Now().UTC(), Quality: analysis.QualityMissing}
			if got := a.freshCloudPoints(job, []analysis.MetricPoint{point}); len(got) != 0 {
				t.Fatal("missing placeholder submitted")
			}
			// Also protect replay of a previously persisted missing batch.
			a.acknowledgeOTLP(job, []analysis.MetricPoint{point})
			zero := 0.0
			point.Value = &zero
			point.Quality = ""
			points := []analysis.MetricPoint{point}
			if got := a.freshCloudPoints(job, points); len(got) != 1 || *got[0].Value != 0 {
				t.Fatal("late observed zero discarded")
			}
			a.acknowledgeOTLP(job, points)
			if got := a.freshCloudPoints(job, points); len(got) != 0 {
				t.Fatal("accepted observation submitted twice")
			}
		})
	}
}

func TestAzureMonitorRejectsEquivalentTimestamps(t *testing.T) {
	start := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	body := `{"interval":"PT1M","value":[{"name":{"value":"CPU"},"unit":"Percent","timeseries":[{"data":[{"timeStamp":"2026-10-01T01:00:00Z","average":1},{"timeStamp":"2026-09-30T21:00:00-04:00","average":2}]}]}]}`
	if _, err := decodeAzureMonitor([]byte(body), CloudMetric{Name: "CPU", Unit: "Percent", Statistic: "Average"}, start, start.Add(5*time.Minute)); err == nil {
		t.Fatal("duplicate instant with different offset accepted")
	}
}
