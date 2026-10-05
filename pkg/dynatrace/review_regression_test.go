package dynatrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequestQuotaIdentityIgnoresUnrelatedCredentialVersions(t *testing.T) {
	cfg := Config{EnvironmentURL: "https://sample.live.dynatrace.com", PlatformURL: "https://sample.apps.dynatrace.com", PlatformAuthType: AuthToken}
	creds := Credentials{EnvironmentToken: "environment", PlatformToken: "platform", VersionIdentity: "project-one-version-one"}
	for _, platform := range []bool{false, true} {
		before := RequestQuotaIdentity(cfg, creds, platform)
		changed := creds
		changed.VersionIdentity = "different-project-and-version"
		if platform {
			changed.EnvironmentToken = "another-environment-token"
		} else {
			changed.PlatformToken = "another-platform-token"
		}
		if RequestQuotaIdentity(cfg, changed, platform) != before {
			t.Fatal("unrelated secret created a new request quota", platform)
		}
		if platform {
			changed.PlatformToken = "different-principal"
		} else {
			changed.EnvironmentToken = "different-principal"
		}
		if RequestQuotaIdentity(cfg, changed, platform) == before {
			t.Fatal("different upstream principal shared quota identity", platform)
		}
	}
	cfg.PlatformAuthType = AuthOAuth
	cfg.OAuthResource = "resource"
	creds.ClientID, creds.ClientSecret = "client", "secret"
	before := RequestQuotaIdentity(cfg, creds, true)
	creds.PlatformToken, creds.EnvironmentToken, creds.VersionIdentity = "unrelated", "unrelated", "v2"
	cfg.OAuthScopes = []string{"storage:logs:read"}
	if RequestQuotaIdentity(cfg, creds, true) != before {
		t.Fatal("OAuth quota split by unrelated credentials or scope selection")
	}
	cfg.PlatformURL = "https://SAMPLE.apps.dynatrace.com:443/"
	if RequestQuotaIdentity(cfg, creds, true) != before {
		t.Fatal("equivalent SaaS origin split its quota")
	}
	cfg.OAuthResource = "another-resource"
	if RequestQuotaIdentity(cfg, creds, true) == before {
		t.Fatal("distinct OAuth resource reused principal quota identity")
	}
}

func TestGrailNumericDefinitionCannotRelabelAggregation(t *testing.T) {
	for _, tc := range []struct {
		query, statistic string
		valid            bool
	}{
		{"timeseries value=avg(m)", "sum", false},
		{"timeseries value=sum(m)", "average", false},
		{"timeseries value=count(m)", "sum", false},
		{"timeseries value=count(m)", "value", true},
		{"timeseries value=avg(m)", "average", true},
		{"timeseries value=avg(m)", "time_average", true},
		{"timeseries value=percentile(m,99)", "p95", false},
		{"timeseries value=percentile(m,99)", "p99", true},
		{"timeseries value=sum(m) | fieldsAdd value=0", "sum", false},
		{"timeseries value=sum(m)+0", "sum", false},
		{"timeseries value=sum(m,rate:1s)", "sum", false},
	} {
		t.Run(tc.query+tc.statistic, func(t *testing.T) {
			q := GrailQuery{Name: "metric", Kind: "metric", Query: tc.query, ValueField: "value", Unit: "count", Statistic: tc.statistic, BucketSeconds: 60}
			if err := q.Validate(); (err == nil) != tc.valid {
				t.Fatal("incorrect numeric semantic admission", err)
			}
		})
	}
}

func TestGrailSyntheticDefaultsCannotEstablishAcceptanceCoverage(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	q := GrailQuery{Name: "calls", Kind: "metric", Query: "timeseries value=sum(m,default:0)", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60}
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	c.now = func() time.Time { return from.Add(time.Hour) }
	var payload grailResponse
	if err := json.Unmarshal([]byte(`{"result":{"records":[{"timeframe":{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:01:00Z"},"interval":60000000000,"value":[0]}]}}`), &payload); err != nil {
		t.Fatal(err)
	}
	points, _, _, err := c.normalizeGrail(q, payload, "p", from, from.Add(time.Minute), "run", 120)
	if err != nil || len(points) != 1 || !points[0].Provider.Synthetic || points[0].Provider.AggregationEligible || *points[0].Value != 0 {
		t.Fatal("filled zero was promoted to a real observation", points, err)
	}
	s := Selection{ProjectID: "p", Grail: []GrailQuery{q}}
	if err = ValidateResult(s, c.cfg, from, from.Add(time.Minute), "run", Result{Points: points}); err != nil {
		t.Fatal(err)
	}
	points[0].Provider.Synthetic = false
	points[0].Provider.AggregationEligible = true
	if ValidateResult(s, c.cfg, from, from.Add(time.Minute), "run", Result{Points: points}) == nil {
		t.Fatal("collector could forge fill eligibility")
	}
}

func TestGrailRetainsActualRoundedResolutionAndRejectsMetadataConflicts(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	q := GrailQuery{Name: "calls", Kind: "metric", Query: "timeseries value=sum(m)", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60}
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	c.now = func() time.Time { return from.Add(time.Hour) }
	var payload grailResponse
	json.Unmarshal([]byte(`{"result":{"records":[{"timeframe":{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:02:00Z"},"interval":120000000000,"value":[4]}],"metadata":{"metrics":[{"fieldName":"value","unit":"count","aggregation":"sum","scalar":false}]}}}`), &payload)
	points, _, _, err := c.normalizeGrail(q, payload, "p", from, from.Add(2*time.Minute), "run", 120)
	if err != nil || len(points) != 1 || points[0].Provider.ResolutionSeconds != 120 {
		t.Fatal("rounded interval rejected or replaced with requested interval", points, err)
	}
	if err = ValidateResult(Selection{ProjectID: "p", Grail: []GrailQuery{q}}, c.cfg, from, from.Add(2*time.Minute), "run", Result{Points: points}); err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []string{
		`{"fieldName":"value","unit":"ms","aggregation":"sum"}`,
		`{"fieldName":"value","unit":"count","aggregation":"avg"}`,
		`{"fieldName":"value","unit":"count","aggregation":"sum","shifted":true}`,
		`{"fieldName":"value","unit":"count","aggregation":"sum","scalar":true}`,
	} {
		json.Unmarshal([]byte(`{"result":{"metadata":{"metrics":[`+metadata+`]}}}`), &payload)
		if _, _, _, err = c.normalizeGrail(q, payload, "p", from, from.Add(2*time.Minute), "run", 120); err == nil {
			t.Fatal("contradictory metric metadata accepted", metadata)
		}
	}
}

func TestGrailMakeTimeseriesProjectionPreservesBucketMetadata(t *testing.T) {
	q := GrailQuery{Name: "calls", Kind: "metric", Query: "fetch logs | makeTimeseries value=count(),interval:1m", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60}
	query, err := BuildGrailQuery(q, time.Now().UTC(), time.Now().Add(time.Minute).UTC(), "")
	if err != nil || !strings.Contains(query, "fields timeframe, interval, value") {
		t.Fatal("fetch-derived time series lost bucket metadata", query, err)
	}
	q.Query = "fetch logs | makeTimeseries value=count()"
	query, err = BuildGrailQuery(q, time.Now().UTC(), time.Now().Add(time.Minute).UTC(), "")
	if err != nil || !strings.Contains(query, "makeTimeseries value=count(), interval: 60s") {
		t.Fatal("fetch-derived series ignored requested bucket interval", query, err)
	}
	q.BucketSeconds = 0
	if q.Validate() == nil {
		t.Fatal("makeTimeseries was falsely admitted as a scalar summary")
	}
}

func TestClassicRemainderDimensionsAreReportedAsPartialWithoutInventedIdentity(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: "custom.calls", Aggregation: "sum", Unit: "count"}}}
	c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/metrics/query" {
			return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
		}
		raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls:sum", "data": []any{
			map[string]any{"dimensionMap": nil, "dimensions": nil, "timestamps": []int64{from.Add(time.Minute).UnixMilli()}, "values": []int{900}},
			map[string]any{"dimensionMap": map[string]string{}, "timestamps": []int64{from.Add(time.Minute).UnixMilli()}, "values": []int{7}},
		}}}})
		return response(200, string(raw)), nil
	})
	result, err := c.Collect(t.Context(), s, from, from.Add(time.Minute), "run")
	if err != nil || len(result.Points) != 1 || *result.Points[0].Value != 7 || result.Coverage["metrics:calls"].Status != "partial" || !result.Coverage["metrics:calls"].Truncated {
		t.Fatal("anonymous remainder became a canonical selected series", result, err)
	}
}

func TestGrailGroupingPreservesAllPrimitiveTupleDimensions(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	q := GrailQuery{Name: "calls", Kind: "metric", Query: "timeseries value=sum(m),by:{dt.entity.host,region}", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60, EntityIDField: "dt.entity.host", EntityIDs: []string{"HOST-0000000000000001"}}
	query, err := BuildGrailQuery(q, from, from.Add(time.Minute), "run")
	if err != nil || !strings.Contains(query, "fields timeframe, interval, value, dt.entity.host, region") {
		t.Fatal("grouping projection lost a dimension", query, err)
	}
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	var response grailResponse
	response.Result.Records = []map[string]json.RawMessage{}
	for _, region := range []string{`"1"`, `1`, `true`, `"true"`, `9007199254740992`, `9007199254740993`, `null`, `""`} {
		response.Result.Records = append(response.Result.Records, map[string]json.RawMessage{
			"dt.entity.host": json.RawMessage(`"HOST-0000000000000001"`), "region": json.RawMessage(region),
			"timeframe": json.RawMessage(`{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:01:00Z"}`),
			"interval":  json.RawMessage(`60000000000`), "value": json.RawMessage(`[7]`),
		})
	}
	points, _, _, err := c.normalizeGrail(q, response, "p", from, from.Add(time.Minute), "run", 120)
	if err != nil || len(points) != len(response.Result.Records) {
		t.Fatal(points, err)
	}
	seen := map[string]bool{}
	for _, point := range points {
		identity, err := json.Marshal(point.Dimensions)
		if err != nil || seen[string(identity)] || point.Provider.EntityIDs["dt.entity.host"] != "HOST-0000000000000001" {
			t.Fatal("distinct grouping tuple collapsed", point, err)
		}
		seen[string(identity)] = true
	}
	if err = ValidateResult(Selection{ProjectID: "p", Grail: []GrailQuery{q}}, c.cfg, from, from.Add(time.Minute), "run", Result{Points: points}); err != nil {
		t.Fatal(err)
	}
	points[0].Dimensions["unselected"] = "value"
	if ValidateResult(Selection{ProjectID: "p", Grail: []GrailQuery{q}}, c.cfg, from, from.Add(time.Minute), "run", Result{Points: points}) == nil {
		t.Fatal("undeclared collector grouping accepted")
	}
	for _, value := range []string{`1`, `true`, `{}`, `[]`} {
		response.Result.Records[0]["dt.entity.host"] = json.RawMessage(value)
		if _, _, _, err = c.normalizeGrail(q, response, "p", from, from.Add(time.Minute), "run", 120); err == nil {
			t.Fatal("non-string entity identity accepted", value)
		}
	}
	for _, query := range []string{
		"timeseries value=sum(m),by:{region=toString(region)}",
		"timeseries value=sum(m),by:{lower(region)}",
		`timeseries value=sum(m),by:{region} | fieldsAdd region="all"`,
		`timeseries value=sum(m),by:{region} | fields region="all",value`,
	} {
		q.Query = query
		if q.Validate() == nil {
			t.Fatal("ambiguous or rewritten grouping expression accepted", query)
		}
	}
}

func TestGrailGroupingEntityIdentityWithoutExplicitScopeAndMissingFields(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := fixtureClient(t, func(*http.Request) (*http.Response, error) { return nil, nil })
	for _, field := range []string{"dt.entity.host", "dt.smartscape.host"} {
		q := GrailQuery{Name: "calls", Kind: "metric", Query: "timeseries value=sum(m),by:{" + field + ",region}", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60}
		var response grailResponse
		json.Unmarshal([]byte(`{"result":{"records":[{"timeframe":{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:01:00Z"},"interval":60000000000,"value":[7],"region":null}]}}`), &response)
		response.Result.Records[0][field] = json.RawMessage(`"HOST-0000000000000001"`)
		points, _, _, err := c.normalizeGrail(q, response, "p", from, from.Add(time.Minute), "run", 120)
		if err != nil || len(points) != 1 || points[0].Dimensions[field] != "HOST-0000000000000001" || points[0].Provider.EntityIDs[field] != "HOST-0000000000000001" || points[0].Dimensions["region"] != "null" {
			t.Fatal("entity identity was quoted or null grouping was lost", points, err)
		}
		if err = ValidateResult(Selection{ProjectID: "p", Grail: []GrailQuery{q}}, c.cfg, from, from.Add(time.Minute), "run", Result{Points: points}); err != nil {
			t.Fatal(err)
		}
		delete(response.Result.Records[0], "region")
		if _, _, _, err = c.normalizeGrail(q, response, "p", from, from.Add(time.Minute), "run", 120); err == nil {
			t.Fatal("missing grouping field invented an anonymous series")
		}
		response.Result.Records[0]["region"] = json.RawMessage(`null`)
		response.Result.Records[0][field] = json.RawMessage(`null`)
		if _, _, _, err = c.normalizeGrail(q, response, "p", from, from.Add(time.Minute), "run", 120); err == nil {
			t.Fatal("null entity identity was invented")
		}
	}
}

func TestQuotedMetricKeysCannotIntroduceMultipleMetricAggregations(t *testing.T) {
	for _, selector := range []string{`"custom.calls","custom.calls"`, `"custom.calls":splitBy(),"custom.calls"`, `"custom.calls" + "other.calls"`} {
		s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: selector, Aggregation: "sum"}}}
		if s.Validate() == nil {
			t.Fatal("multiple metric query could relabel a default aggregation", selector)
		}
	}
	s := Selection{ProjectID: "p", Metrics: []MetricSelection{{Name: "calls", MetricSelector: `"custom.calls":filter(eq("label","a,b")):splitBy("label","zone")`, Aggregation: "sum"}}}
	if err := s.Validate(); err != nil {
		t.Fatal("valid commas inside transformations rejected", err)
	}
}

func TestCredentialBearingDimensionsFailBeforeNumericPersistence(t *testing.T) {
	from := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, backend := range []string{"classic", "grail"} {
		for _, tc := range []struct {
			key, value string
			unsafe     bool
		}{
			{"region", "west-1", false},
			{"region", "my-api-token-service", false},
			{"region", "api_token=SECRET", true},
			{"region", "https://example.test/?client_secret=SECRET", true},
			{"api_token", "SECRET", true},
			{"region", `{"clientSecret":"SECRET"}`, true},
		} {
			t.Run(backend+"/"+tc.key+"/"+tc.value, func(t *testing.T) {
				s := Selection{ProjectID: "p"}
				if backend == "classic" {
					s.Metrics = []MetricSelection{{Name: "calls", MetricSelector: "custom.calls", Aggregation: "sum", Unit: "count"}}
				} else {
					s.Grail = []GrailQuery{{Name: "calls", Kind: "metric", Query: "timeseries value=sum(m),by:{" + tc.key + "}", ValueField: "value", Unit: "count", Statistic: "sum", BucketSeconds: 60}}
				}
				c := fixtureClient(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/api/v2/metrics/custom.calls" {
						return response(200, `{"metricId":"custom.calls","unit":"Count"}`), nil
					}
					if backend == "classic" {
						raw, _ := json.Marshal(map[string]any{"resolution": "1m", "result": []any{map[string]any{"metricId": "custom.calls:sum", "data": []any{map[string]any{"dimensionMap": map[string]string{tc.key: tc.value}, "timestamps": []int64{from.Add(time.Minute).UnixMilli()}, "values": []int{7}}}}}})
						return response(200, string(raw)), nil
					}
					var raw grailResponse
					json.Unmarshal([]byte(`{"state":"SUCCEEDED","result":{"records":[{"timeframe":{"start":"2026-10-03T12:00:00Z","end":"2026-10-03T12:01:00Z"},"interval":60000000000,"value":[7]}],"metadata":{"grail":{"scannedBytes":0}}}}`), &raw)
					value, _ := json.Marshal(tc.value)
					raw.Result.Records[0][tc.key] = value
					encoded, _ := json.Marshal(raw)
					return response(200, string(encoded)), nil
				})
				result, err := c.Collect(t.Context(), s, from, from.Add(time.Minute), "run")
				if err != nil {
					t.Fatal(err)
				}
				key := "metrics:calls"
				if backend == "grail" {
					key = "grail:calls"
				}
				if tc.unsafe {
					encoded, _ := json.Marshal(result)
					if len(result.Points) != 0 || result.Coverage[key].Status != "query_rejected" || strings.Contains(string(encoded), "SECRET") {
						t.Fatal("credential-bearing tuple reached a public projection", result)
					}
					return
				}
				if len(result.Points) != 1 || result.Coverage[key].Status != "complete" {
					t.Fatal("benign tuple was rejected", result)
				}
				wanted := tc.value
				if backend == "grail" {
					encoded, _ := json.Marshal(wanted)
					wanted = string(encoded)
				}
				if result.Points[0].Dimensions[tc.key] != wanted {
					t.Fatal("benign dimension identity changed", result.Points[0].Dimensions)
				}
				result.Points[0].Dimensions[tc.key] = "api_token=SECRET"
				if err = ValidateResult(s, c.cfg, from, from.Add(time.Minute), "run", result); !errors.Is(err, errUnsafeDimensions) {
					t.Fatal("collector-supplied sensitive tuple bypassed validation", err)
				}
			})
		}
	}
}

func TestSamePrincipalClientsShareOneExecutorSlotAcrossCancellationAndRelease(t *testing.T) {
	cfg := Config{EnvironmentURL: "https://concurrency-review.live.dynatrace.com"}
	first, err := NewClient(cfg, Credentials{EnvironmentToken: "shared-principal", VersionIdentity: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewClient(cfg, Credentials{EnvironmentToken: "shared-principal", PlatformToken: "unrelated", VersionIdentity: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if first.environmentRate != second.environmentRate {
		t.Fatal("same upstream principal received independent local concurrency")
	}
	release, err := first.acquire(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	// Remove request-rate pacing only to isolate the concurrency contract. The
	// held request slot must still block a different same-principal client.
	first.environmentRate.mu.Lock()
	first.environmentRate.next = time.Time{}
	first.environmentRate.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err = second.acquire(ctx, false)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("a second same-principal request bypassed the executor slot", err)
	}
	release()
	release = func() {} // Avoid a double release through the deferred closure.
	first.environmentRate.mu.Lock()
	first.environmentRate.next = time.Time{}
	first.environmentRate.mu.Unlock()
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	permit, err := second.acquire(ctx, false)
	if err != nil {
		t.Fatal("cancelled waiter leaked the shared request slot", err)
	}
	permit()
}

func TestRateCacheOverflowSharesOneConservativeLimiter(t *testing.T) {
	sharedStates.Lock()
	original := sharedStates.rates
	sharedStates.rates = map[string]*rateState{}
	for i := 0; i < 512; i++ {
		sharedStates.rates[fmt.Sprintf("review-cached-%d", i)] = &rateState{slots: make(chan struct{}, 1), lastUsed: time.Now()}
	}
	sharedStates.rates["review-cached-0"].lastUsed = time.Now().Add(-2 * time.Hour)
	sharedStates.Unlock()
	defer func() {
		sharedStates.Lock()
		sharedStates.rates = original
		sharedStates.Unlock()
	}()
	first := sharedRate("review-omitted-principal")
	second := sharedRate("review-omitted-principal")
	other := sharedRate("review-other-omitted-principal")
	if first != second || first != other || first != sharedStates.overflowRate {
		t.Fatal("overflow clients received independent request budgets")
	}
	sharedStates.Lock()
	count := len(sharedStates.rates)
	cached := sharedStates.rates["review-cached-0"]
	sharedStates.Unlock()
	if count != 512 || sharedRate("review-cached-0") != cached {
		t.Fatal("overflow changed bounded cached-principal mappings")
	}
}

func TestOAuthRefreshCacheOverflowFailsClosedWithoutAffectingTokenOnlyClients(t *testing.T) {
	cfg := Config{PlatformURL: "https://oauth-overflow-review.apps.dynatrace.com", PlatformAuthType: AuthOAuth, OAuthResource: "resource", OAuthScopes: []string{"storage:logs:read"}}
	creds := Credentials{ClientID: "cached-client", ClientSecret: "cached-secret", VersionIdentity: "v1"}
	key := hashIdentity(cfg.PlatformURL, cfg.OAuthResource, strings.Join(cfg.OAuthScopes, " "), creds.ClientID, creds.ClientSecret, creds.VersionIdentity)
	cached := &tokenState{lastUsed: time.Now()}
	sharedStates.Lock()
	original := sharedStates.tokens
	sharedStates.tokens = map[string]*tokenState{key: cached}
	for i := 0; i < 255; i++ {
		sharedStates.tokens[fmt.Sprintf("review-token-%d", i)] = &tokenState{lastUsed: time.Now()}
	}
	sharedStates.Unlock()
	defer func() {
		sharedStates.Lock()
		sharedStates.tokens = original
		sharedStates.Unlock()
	}()
	for range 2 {
		client, err := NewClient(cfg, Credentials{ClientID: "uncacheable-client", ClientSecret: "secret", VersionIdentity: "new-version"})
		if client != nil || !errors.Is(err, ErrBudget) {
			t.Fatal("uncacheable OAuth principal received independent refresh state", client, err)
		}
	}
	for range 2 {
		client, err := NewClient(cfg, creds)
		if err != nil || client.token != cached {
			t.Fatal("cached OAuth principal lost its shared refresh state", err)
		}
	}
	tokenConfig := cfg
	tokenConfig.PlatformAuthType = AuthToken
	if _, err := NewClient(tokenConfig, Credentials{PlatformToken: "token"}); err != nil {
		t.Fatal("token-only client consumed OAuth admission", err)
	}
	if _, err := NewClient(Config{EnvironmentURL: "https://classic-only-review.live.dynatrace.com"}, Credentials{EnvironmentToken: "token"}); err != nil {
		t.Fatal("Classic-only client consumed OAuth admission", err)
	}
	if _, err := NewClient(cfg, Credentials{}); err != nil {
		t.Fatal("unused OAuth configuration consumed refresh admission", err)
	}
	sharedStates.Lock()
	count := len(sharedStates.tokens)
	sharedStates.Unlock()
	if count != 256 {
		t.Fatal("refresh admission exceeded its bounded memory", count)
	}
}
