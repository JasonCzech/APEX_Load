package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/trafficprofile"
)

func testTrafficProfile(runtimeTarget string) *TrafficProfile {
	semantics := string(trafficprofile.SemanticsTPS)
	if runtimeTarget == "vusers" {
		semantics = string(trafficprofile.SemanticsVUsers)
	}
	return &TrafficProfile{
		Version: 1, ValueSemantics: semantics, RuntimeTarget: runtimeTarget, Interpolation: "step", GapPolicy: string(trafficprofile.GapHold),
		Source:         TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "traffic.csv", TimestampColumn: "timestamp", ValueColumns: []string{"orders"}}},
		SourceDuration: "10s", TargetDuration: "10s", RefreshedAt: time.Now().UTC(),
		Mappings: []TrafficProfileMapping{{SeriesKey: "orders", SeriesName: "Orders", GroupName: "checkout", Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 0}, {OffsetMS: 5000, Target: 5}}}},
	}
}

func TestValidateTrafficProfileCompatibility(t *testing.T) {
	profile := testTrafficProfile("tps")
	groups := []CreateGroupEntry{{Name: "checkout", VUsers: 5}}
	minimum, maximum := 1, 10
	goal := &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum}
	if err := validateTrafficProfile(profile, groups, "10s", "goal_oriented", goal); err != nil {
		t.Fatal(err)
	}
	withStages := groups
	withStages[0].Stages = []storage.LoadStage{{Type: "hold", Duration: "10s", TargetVUsers: 10}}
	if err := validateTrafficProfile(profile, withStages, "10s", "goal_oriented", goal); err == nil {
		t.Fatal("expected stages/profile conflict")
	}
}

func TestValidateTrafficProfileCanonicalizesGroupAndRequiresVUserPeak(t *testing.T) {
	profile := testTrafficProfile("vusers")
	profile.Mappings[0].GroupName = "CHECKOUT"
	groups := []CreateGroupEntry{{Name: "Checkout", VUsers: 5}}
	if err := validateTrafficProfile(profile, groups, "10s", "vuser_driven", nil); err != nil {
		t.Fatal(err)
	}
	if profile.Mappings[0].GroupName != "Checkout" {
		t.Fatalf("canonical group = %q", profile.Mappings[0].GroupName)
	}
	groups[0].VUsers = 4
	if err := validateTrafficProfile(profile, groups, "10s", "vuser_driven", nil); err == nil {
		t.Fatal("expected VUser peak mismatch")
	}
}

func TestTrafficProfileInitialTargetsAndCapacity(t *testing.T) {
	profile := testTrafficProfile("vusers")
	groups := []TestGroup{{Name: "checkout", VUsers: 5}, {Name: "static", VUsers: 7}}
	engineGroups := []engine.GroupConfig{{Name: "checkout", VUsers: 5}, {Name: "static", VUsers: 7}}
	if got := trafficProfileCapacityVUsers(profile, groups, 10*time.Second, nil); got != 12 {
		t.Fatalf("capacity = %d, want 12", got)
	}
	if got := applyTrafficProfileInitialTargets(profile, groups, engineGroups); got != 7 {
		t.Fatalf("initial total = %d, want 7", got)
	}
	if groups[0].VUsers != 0 || engineGroups[0].VUsers != 0 || !engineGroups[0].AllowZeroVUsers || groups[1].VUsers != 7 {
		t.Fatalf("initial groups = %#v engine=%#v", groups, engineGroups)
	}

	maxVUsers := 100
	tps := testTrafficProfile("tps")
	if got := trafficProfileCapacityVUsers(tps, groups, 10*time.Second, &GoalConfig{MaxVUsers: &maxVUsers}); got != 100 {
		t.Fatalf("TPS capacity = %d, want 100", got)
	}
}

func TestTrafficProfileGoalScalingPinsStaticGroupsAndHonorsZeroSuspension(t *testing.T) {
	profile := testTrafficProfile("tps")
	groups := []TestGroup{{Name: "checkout", VUsers: 10}, {Name: "static", VUsers: 7}}
	current := map[string]int{"checkout": 10, "static": 7}
	targets := distributeTrafficProfileTargetVUsers(groups, current, 27, map[string]GoalTargetStatus{"checkout": {Group: "checkout", TargetTPS: 100}}, profile)
	if targets["checkout"] != 20 || targets["static"] != 7 {
		t.Fatalf("targets = %#v, want managed=20 static=7", targets)
	}

	zero := 0.0
	minimum := 5
	test := &ManagedTest{ScenarioMode: "goal_oriented", TrafficProfile: profile, Groups: groups, GoalConfig: &GoalConfig{TargetAverageTPS: &zero, MinVUsers: &minimum}, GoalTargets: map[string]GoalTargetStatus{"checkout": {Group: "checkout", TargetTPS: 0}}, goalControl: &goalControllerState{}}
	update := evaluateGoalControllerForTest(test, time.Now().UTC(), 7, &LiveMetrics{})
	if update.ShouldScale || update.Status == nil || update.Status.SuggestedTotalVUsers != 7 {
		t.Fatalf("zero-target update = %#v", update)
	}
}

func TestTrafficProfileGoalControllerExcludesStaticGroupTPS(t *testing.T) {
	profile := testTrafficProfile("tps")
	target := 100.0
	minimum, maximum := 1, 100
	test := &ManagedTest{
		ScenarioMode:   "goal_oriented",
		TrafficProfile: profile,
		Groups:         []TestGroup{{Name: "checkout", VUsers: 10}, {Name: "static", VUsers: 10}},
		GoalConfig:     &GoalConfig{TargetAverageTPS: &target, MinVUsers: &minimum, MaxVUsers: &maximum},
		GoalTargets:    map[string]GoalTargetStatus{"checkout": {Group: "checkout", TargetTPS: target}},
		goalControl:    &goalControllerState{},
	}
	live := &LiveMetrics{
		Timestamp: time.Now().UTC(), TPS: 150,
		GroupMetrics: map[string]*GroupLiveMetrics{
			"checkout": {Name: "checkout", TPS: 50},
			"static":   {Name: "static", TPS: 100},
		},
	}
	update := evaluateGoalControllerForTest(test, live.Timestamp, 20, live)
	if !update.ShouldScale || update.ScaleDirection != "up" || update.Status == nil || update.Status.CurrentTPS != 50 {
		t.Fatalf("managed-only controller update = %#v", update)
	}
	applyRuntimeGoalTargetsToLiveMetrics(test, live)
	if live.TargetTPS != 100 || live.ThroughputGapPct != 50 || !live.ThroughputTargetMissed {
		t.Fatalf("managed-only target evidence = %#v", live)
	}
}

func TestArchiveGoalsRetainConfiguredTrafficProfilePeak(t *testing.T) {
	profile := testTrafficProfile("tps")
	profile.Mappings = append(profile.Mappings, TrafficProfileMapping{
		SeriesKey: "search", SeriesName: "Search", GroupName: "search", PeakTarget: 4,
		Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 3}, {OffsetMS: 5000, Target: 4}},
	})
	goals, completion := buildRunArchiveGoalCompletion(RunArchiveProvenance{TrafficProfile: profile}, RunArchiveOverview{})
	if goals == nil || completion == nil || goals.TargetTPS != 9 || completion.ThroughputTargetTPS != 9 {
		t.Fatalf("goals=%#v completion=%#v", goals, completion)
	}
}

func TestCurrentGoalTargetsRetainsProfileZeroAndExcludesStaticGroups(t *testing.T) {
	now := time.Now().UTC()
	test := &ManagedTest{TrafficProfile: testTrafficProfile("tps"), Groups: []TestGroup{{Name: "checkout", VUsers: 0}, {Name: "static", VUsers: 10}}, GoalTargets: map[string]GoalTargetStatus{"checkout": {Group: "checkout", TargetTPS: 0, UpdatedAt: now}}}
	targets := currentGoalTargetStatuses(test, now)
	if len(targets) != 1 || targets["checkout"].TargetTPS != 0 {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestTrafficProfileRecoverySelectsOnlyActivePoint(t *testing.T) {
	points := make([]trafficprofile.RuntimePoint, 5760)
	for index := range points {
		points[index] = trafficprofile.RuntimePoint{OffsetMS: int64(index * 15_000), Target: float64(index)}
	}
	for _, test := range []struct {
		name    string
		elapsed time.Duration
		want    int
	}{
		{name: "before start", elapsed: -time.Second, want: 0},
		{name: "first boundary", elapsed: 0, want: 0},
		{name: "between boundaries", elapsed: 29 * time.Second, want: 1},
		{name: "exact boundary", elapsed: 30 * time.Second, want: 2},
		{name: "after daily profile", elapsed: 25 * time.Hour, want: 5759},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := trafficProfileStartIndex(points, test.elapsed); got != test.want {
				t.Fatalf("start index = %d, want %d", got, test.want)
			}
		})
	}
}

func TestOpenSearchTrafficProfileQueryMapsBucketsAndAuth(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"aggregations": map[string]any{"traffic": map[string]any{"buckets": []any{map[string]any{"key": 1000, "doc_count": 3}, map[string]any{"key": 16000, "doc_count": 4}}}}})
	}))
	defer upstream.Close()
	t.Setenv("OPENSEARCH_TEST_TOKEN", "secret-token")
	source := newOpenSearchTrafficProfileSource(OpenSearchSettings{Enabled: true, BaseURL: upstream.URL, TimeoutSeconds: 2, BearerTokenEnvVar: "OPENSEARCH_TEST_TOKEN"})
	series, err := source.query(context.Background(), TrafficProfileOpenSearchSource{Index: "transactions-*", TimestampField: "@timestamp", Interval: "15s", Metric: "count", Range: TrafficProfileQueryRange{StepSeconds: 15}}, time.Unix(0, 0), time.Unix(60, 0), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer secret-token" || len(series) != 1 || len(series[0].Points) != 2 || series[0].Points[1].Value != 4 {
		t.Fatalf("authorization=%q series=%#v", authorization, series)
	}
}

func TestOpenSearchTrafficProfileRejectsMismatchedIntervalAndScalarFilter(t *testing.T) {
	source := newOpenSearchTrafficProfileSource(OpenSearchSettings{Enabled: true, BaseURL: "https://example.invalid", TimeoutSeconds: 2})
	base := TrafficProfileOpenSearchSource{Index: "transactions", TimestampField: "@timestamp", Interval: "15s", Metric: "count", Range: TrafficProfileQueryRange{StepSeconds: 30}}
	if _, err := source.query(context.Background(), base, time.Unix(0, 0), time.Unix(60, 0), 1<<20); err == nil {
		t.Fatal("expected interval mismatch")
	}
	base.Range.StepSeconds = 15
	base.Filter = json.RawMessage(`"match_all"`)
	if _, err := source.query(context.Background(), base, time.Unix(0, 0), time.Unix(60, 0), 1<<20); err == nil {
		t.Fatal("expected scalar filter rejection")
	}
}

func TestOpenSearchTrafficProfileBoundsAndValidatesResponses(t *testing.T) {
	baseSource := TrafficProfileOpenSearchSource{Index: "transactions", TimestampField: "@timestamp", Interval: "15s", Metric: "count", Range: TrafficProfileQueryRange{StepSeconds: 15}}
	for _, test := range []struct {
		name     string
		body     string
		maxBytes int64
	}{
		{name: "oversized", body: strings.Repeat("x", 1025), maxBytes: 1024},
		{name: "malformed", body: `{not-json}`, maxBytes: 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer upstream.Close()
			source := newOpenSearchTrafficProfileSource(OpenSearchSettings{Enabled: true, BaseURL: upstream.URL, TimeoutSeconds: 2})
			if _, err := source.query(context.Background(), baseSource, time.Unix(0, 0), time.Unix(60, 0), test.maxBytes); err == nil {
				t.Fatal("expected bounded response error")
			}
		})
	}
}

func TestValidateOpenSearchSettingsAuthenticationPolicy(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	for _, test := range []struct {
		name     string
		settings OpenSearchSettings
	}{
		{name: "basic requires https", settings: OpenSearchSettings{Enabled: true, BaseURL: "http://example.com", Username: "reader", PasswordEnvVar: "OPENSEARCH_PASSWORD"}},
		{name: "bearer requires https", settings: OpenSearchSettings{Enabled: true, BaseURL: "http://example.com", BearerTokenEnvVar: "OPENSEARCH_TOKEN"}},
		{name: "auth modes are exclusive", settings: OpenSearchSettings{Enabled: true, BaseURL: "https://example.com", Username: "reader", PasswordEnvVar: "OPENSEARCH_PASSWORD", BearerTokenEnvVar: "OPENSEARCH_TOKEN"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateOpenSearchSettings(test.settings); err == nil {
				t.Fatal("expected authentication policy error")
			}
		})
	}
	if err := validateOpenSearchSettings(OpenSearchSettings{Enabled: true, BaseURL: "https://example.com", BearerTokenEnvVar: "OPENSEARCH_TOKEN"}); err != nil {
		t.Fatalf("valid bearer settings: %v", err)
	}
}

func TestValidateTrafficProfileSourceProvenance(t *testing.T) {
	profile := testTrafficProfile("tps")
	profile.Source = TrafficProfileSource{}
	minimum, maximum := 1, 10
	goal := &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum}
	if err := validateTrafficProfile(profile, []CreateGroupEntry{{Name: "checkout"}}, "10s", "goal_oriented", goal); err == nil {
		t.Fatal("expected missing source rejection")
	}
	profile = testTrafficProfile("tps")
	profile.RefreshedAt = time.Time{}
	if err := validateTrafficProfile(profile, []CreateGroupEntry{{Name: "checkout"}}, "10s", "goal_oriented", goal); err == nil {
		t.Fatal("expected missing refresh timestamp rejection")
	}
}

// Derivation authorizes on the declared project_id but compiles a
// caller-supplied draft, so the draft must be pinned to that project or the
// endpoint becomes a cross-project read of another tenant's scripts.
func TestDeriveTrafficProfileRejectsDraftFromAnotherProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	foreign := server.runner.StoreScript(&dsl.Script{
		ID: "script-other-project", Name: "Other Tenant Checkout", Version: "1.0.0", Protocol: "http",
		ProjectID: "project-b", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/"}},
	})
	body, err := json.Marshal(trafficProfileDeriveRequest{
		ProjectID: "project-a",
		Series:    []trafficprofile.Series{{Key: "orders", Name: "Orders"}},
		Mappings:  []trafficProfileSeriesMappingRequest{{SeriesKey: "orders", GroupName: "checkout"}},
		ScenarioDraft: CreateScenarioRequest{
			Name: "Cross tenant", Duration: "1m",
			Groups: []CreateGroupEntry{{Name: "checkout", ScriptID: foreign.ID, VUsers: 5}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "do not belong to project_id") {
		t.Fatalf("cross-project derive = %d %s, want a project mismatch rejection", recorder.Code, recorder.Body.String())
	}
}

// Traffic-profile failures must reach the dashboard as JSON. The client falls
// back to bare HTTP status text for any other content type, which discarded
// every specific validation message.
func TestTrafficProfileErrorsAreJSONEncoded(t *testing.T) {
	server := NewServer(ServerConfig{})
	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"project_id":"project-a","series":[],"mappings":[{"series_key":"orders","group_name":"checkout"}]}`)
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("derive = %d %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body %q: %v", recorder.Body.String(), err)
	}
	if !strings.Contains(payload.Error, "series must contain") {
		t.Fatalf("error payload = %q, want the specific validation message", payload.Error)
	}
}

// A catalog request for a project that does not exist is a client error, not a
// policy outage; 503 told clients and uptime monitoring to retry it.
func TestLoadModelCatalogDoesNotReportMissingProjectAsUnavailable(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	for _, target := range []string{
		"/api/v1/scenarios/load-models/support-matrix?project_id=no-such-project",
		"/api/v1/scenarios/load-models/templates?project_id=no-such-project",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if strings.Contains(target, "templates") {
			server.handleLoadModelTemplates(recorder, request)
		} else {
			server.handleLoadModelSupportMatrix(recorder, request)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s = %d %s, want the ungoverned catalog", target, recorder.Code, recorder.Body.String())
		}
	}
}
