package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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

func TestValidateTrafficProfileRelativeFileSource(t *testing.T) {
	valid := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "traffic.csv", TimestampColumn: "elapsed", TimestampMode: "relative", RelativeStartTime: "12:00", ValueColumns: []string{"orders"}, Timezone: "America/New_York"}}
	if err := validateTrafficProfileSource(valid); err != nil {
		t.Fatalf("valid relative file source: %v", err)
	}
	for name, mutate := range map[string]func(*TrafficProfileFileSource){
		"missing timezone": func(source *TrafficProfileFileSource) { source.Timezone = "" },
		"invalid timezone": func(source *TrafficProfileFileSource) { source.Timezone = "Mars/Olympus" },
		"missing start":    func(source *TrafficProfileFileSource) { source.RelativeStartTime = "" },
		"invalid start":    func(source *TrafficProfileFileSource) { source.RelativeStartTime = "25:00" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			file := *valid.File
			candidate.File = &file
			mutate(candidate.File)
			if err := validateTrafficProfileSource(candidate); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	overlap := valid
	overlapFile := *valid.File
	overlap.File = &overlapFile
	overlap.File.ARTColumns = []string{"orders"}
	if err := validateTrafficProfileSource(overlap); err == nil || !strings.Contains(err.Error(), "both a workload value and ART") {
		t.Fatalf("overlapping workload/ART columns error = %v", err)
	}
}

func TestTrafficProfileWindowSpecRelativeOffsets(t *testing.T) {
	window, err := trafficProfileWindowSpec(TrafficProfileWindow{Mode: "explicit", StartOffset: "15m", EndOffset: "45m"})
	if err != nil {
		t.Fatal(err)
	}
	if !window.RelativeOffsets || window.StartOffset != 15*time.Minute || window.EndOffset != 45*time.Minute {
		t.Fatalf("window = %#v", window)
	}
	for name, value := range map[string]TrafficProfileWindow{
		"missing end":  {Mode: "explicit", StartOffset: "15m"},
		"reversed":     {Mode: "explicit", StartOffset: "45m", EndOffset: "15m"},
		"mixed":        {Mode: "explicit", Start: time.Now(), StartOffset: "15m", EndOffset: "45m"},
		"not explicit": {Mode: "full", StartOffset: "15m", EndOffset: "45m"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := trafficProfileWindowSpec(value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateTrafficProfileSupportsSharedAndPerGroupSources(t *testing.T) {
	minimum, maximum := 1, 20
	goal := &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum}
	groups := []CreateGroupEntry{{Name: "checkout", VUsers: 5}, {Name: "search", VUsers: 5}}

	shared := testTrafficProfile("tps")
	shared.Mappings = append(shared.Mappings, TrafficProfileMapping{
		SeriesKey: "orders", SeriesName: "Orders", GroupName: "search",
		Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 0}, {OffsetMS: 5000, Target: 5}},
	})
	if err := validateTrafficProfile(shared, groups, "10s", "goal_oriented", goal); err != nil {
		t.Fatalf("shared series fan-out: %v", err)
	}

	checkoutSource := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "checkout.csv", TimestampColumn: "timestamp", ValueColumns: []string{"orders"}}}
	searchSource := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "search.xlsx", Sheet: "Traffic", TimestampColumn: "when", ValueColumns: []string{"queries"}}}
	checkoutEntry, searchEntry := trafficProfileSourceEntry(checkoutSource), trafficProfileSourceEntry(searchSource)
	separate := testTrafficProfile("tps")
	separate.Source = TrafficProfileSource{Kind: "multiple", Name: "Per-group files", Sources: []TrafficProfileSourceEntry{checkoutEntry, searchEntry}}
	separate.Mappings[0].Source = &checkoutEntry
	separate.Mappings[0].SourceDuration = "10s"
	separate.Mappings = append(separate.Mappings, TrafficProfileMapping{
		SeriesKey: "queries", SeriesName: "Queries", GroupName: "search", Source: &searchEntry, SourceDuration: "10s",
		Points: []trafficprofile.RuntimePoint{{OffsetMS: 0, Target: 1}, {OffsetMS: 5000, Target: 4}},
	})
	if err := validateTrafficProfile(separate, groups, "10s", "goal_oriented", goal); err != nil {
		t.Fatalf("per-group sources: %v", err)
	}
	unlistedSource := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "other.csv", TimestampColumn: "timestamp", ValueColumns: []string{"value"}}}
	unlistedEntry := trafficProfileSourceEntry(unlistedSource)
	separate.Mappings[1].Source = &unlistedEntry
	if err := validateTrafficProfile(separate, groups, "10s", "goal_oriented", goal); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("unexpected mismatched source error: %v", err)
	}
}

func TestDeriveTrafficProfileAlignsPerGroupFileSpans(t *testing.T) {
	server := NewServer(ServerConfig{})
	start := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	checkoutSource := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "checkout.csv", TimestampColumn: "timestamp", ValueColumns: []string{"orders"}}}
	searchSource := TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "search.xlsx", Sheet: "Traffic", TimestampColumn: "timestamp", ValueColumns: []string{"queries"}}}
	checkoutEntry, searchEntry := trafficProfileSourceEntry(checkoutSource), trafficProfileSourceEntry(searchSource)
	request := trafficProfileDeriveRequest{
		Source: TrafficProfileSource{Kind: "multiple", Name: "Per-group production traffic files", Sources: []TrafficProfileSourceEntry{checkoutEntry, searchEntry}},
		Series: []trafficprofile.Series{
			{Key: "checkout:orders", Name: "Orders", Points: []trafficprofile.Point{{Timestamp: start, Value: 10}, {Timestamp: start.Add(10 * time.Second), Value: 20}}},
			{Key: "search:queries", Name: "Queries", Points: []trafficprofile.Point{{Timestamp: start, Value: 5}, {Timestamp: start.Add(20 * time.Second), Value: 15}}},
		},
		Mappings: []trafficProfileSeriesMappingRequest{
			{SeriesKey: "checkout:orders", GroupName: "checkout", Source: &checkoutEntry},
			{SeriesKey: "search:queries", GroupName: "search", Source: &searchEntry},
		},
		ValueSemantics: trafficprofile.SemanticsTPS,
		GapPolicy:      trafficprofile.GapHold,
		Window:         TrafficProfileWindow{Mode: "full"},
		Amplitude:      TrafficProfileAmplitude{Mode: "factor", Value: 1},
		ScenarioDraft: CreateScenarioRequest{
			Name: "Per-group replay", Duration: "1m", ScenarioMode: "vuser_driven",
			Groups: []CreateGroupEntry{{Name: "checkout", VUsers: 5}, {Name: "search", VUsers: 5}},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("derive = %d %s", recorder.Code, recorder.Body.String())
	}
	var response trafficProfileDeriveResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TrafficProfile.TargetDuration != "40s" || response.TrafficProfile.SourceDuration != "40s" || len(response.TrafficProfile.Mappings) != 2 {
		t.Fatalf("profile = %#v", response.TrafficProfile)
	}
	if response.TrafficProfile.Mappings[0].SourceDuration != "20s" || response.TrafficProfile.Mappings[0].SourceStart != start.Format(time.RFC3339Nano) || response.TrafficProfile.Mappings[0].SourceEnd != start.Add(20*time.Second).Format(time.RFC3339Nano) || response.TrafficProfile.Mappings[0].Points[1].OffsetMS != 20_000 || response.TrafficProfile.Mappings[1].Source == nil {
		t.Fatalf("aligned mappings = %#v", response.TrafficProfile.Mappings)
	}
}

func TestDeriveTrafficProfileCanSelectOneOverallPeakWindow(t *testing.T) {
	server := NewServer(ServerConfig{})
	start := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	request := trafficProfileDeriveRequest{
		Source: TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "traffic.csv", TimestampColumn: "timestamp", ValueColumns: []string{"checkout", "search"}}},
		Series: []trafficprofile.Series{
			{Key: "checkout", Name: "Checkout", Points: []trafficprofile.Point{{Timestamp: start, Value: 0}, {Timestamp: start.Add(time.Second), Value: 100}, {Timestamp: start.Add(2 * time.Second), Value: 100}, {Timestamp: start.Add(3 * time.Second), Value: 0}, {Timestamp: start.Add(4 * time.Second), Value: 0}}},
			{Key: "search", Name: "Search", Points: []trafficprofile.Point{{Timestamp: start, Value: 0}, {Timestamp: start.Add(time.Second), Value: 0}, {Timestamp: start.Add(2 * time.Second), Value: 90}, {Timestamp: start.Add(3 * time.Second), Value: 90}, {Timestamp: start.Add(4 * time.Second), Value: 0}}},
		},
		Mappings: []trafficProfileSeriesMappingRequest{
			{SeriesKey: "checkout", GroupName: "checkout"},
			{SeriesKey: "search", GroupName: "search"},
		},
		ValueSemantics: trafficprofile.SemanticsTPS,
		GapPolicy:      trafficprofile.GapHold,
		Window:         TrafficProfileWindow{Mode: "peak", PeakScope: "overall", Duration: "2s"},
		TargetDuration: "10s",
		Amplitude:      TrafficProfileAmplitude{Mode: "factor", Value: 1},
		ScenarioDraft: CreateScenarioRequest{
			Name: "Overall peak replay", Duration: "10s", ScenarioMode: "goal_oriented",
			Groups: []CreateGroupEntry{{Name: "checkout", VUsers: 5}, {Name: "search", VUsers: 5}},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("derive = %d %s", recorder.Code, recorder.Body.String())
	}
	var response trafficProfileDeriveResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	wantStart := start.Add(time.Second).Format(time.RFC3339Nano)
	if response.TrafficProfile.Window.PeakScope != "overall" || len(response.TrafficProfile.Mappings) != 2 {
		t.Fatalf("profile = %#v", response.TrafficProfile)
	}
	for _, mapping := range response.TrafficProfile.Mappings {
		if mapping.SourceStart != wantStart || mapping.SourceDuration != "2s" || len(mapping.Points) != 2 || mapping.Points[1].OffsetMS != 5_000 {
			t.Fatalf("mapping %s resolved window = %s / %s", mapping.GroupName, mapping.SourceStart, mapping.SourceDuration)
		}
	}
	if !slices.ContainsFunc(response.Warnings, func(value string) bool { return strings.Contains(value, "overall peak window") }) {
		t.Fatalf("warnings = %#v", response.Warnings)
	}

	request.Window.PeakScope = ""
	body, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy per-group derive = %d %s", recorder.Code, recorder.Body.String())
	}
	response = trafficProfileDeriveResponse{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	starts := map[string]string{}
	for _, mapping := range response.TrafficProfile.Mappings {
		starts[mapping.GroupName] = mapping.SourceStart
	}
	if starts["checkout"] != wantStart || starts["search"] != start.Add(2*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("legacy per-group starts = %#v", starts)
	}
}

func TestTrafficProfilePeakScopeValidationAndBackwardCompatibleDefault(t *testing.T) {
	window, err := trafficProfileWindowSpec(TrafficProfileWindow{Mode: "peak", Duration: "1h"})
	if err != nil || window.Duration != time.Hour || trafficProfilePeakScope(TrafficProfileWindow{Mode: "peak"}) != "per_group" {
		t.Fatalf("legacy peak window = %#v, scope=%q, err=%v", window, trafficProfilePeakScope(TrafficProfileWindow{Mode: "peak"}), err)
	}
	if _, err := trafficProfileWindowSpec(TrafficProfileWindow{Mode: "peak", PeakScope: "invalid", Duration: "1h"}); err == nil {
		t.Fatal("expected invalid peak scope to fail")
	}
	if _, err := trafficProfileWindowSpec(TrafficProfileWindow{Mode: "full", PeakScope: "overall"}); err == nil {
		t.Fatal("expected peak scope on a full window to fail")
	}
}

func TestDeriveTrafficProfileARTSizesSimultaneousPeakAndReplacesGuardrails(t *testing.T) {
	server := NewServer(ServerConfig{})
	start := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	existingMin, existingMax := 9, 10
	request := trafficProfileDeriveRequest{
		Source: TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "traffic.csv", TimestampColumn: "timestamp", ValueColumns: []string{"checkout", "search"}}},
		Series: []trafficprofile.Series{
			{Key: "checkout", Name: "Checkout", Points: []trafficprofile.Point{{Timestamp: start, Value: 100}, {Timestamp: start.Add(10 * time.Second), Value: 10}}},
			{Key: "search", Name: "Search", Points: []trafficprofile.Point{{Timestamp: start, Value: 10}, {Timestamp: start.Add(10 * time.Second), Value: 100}}},
		},
		Mappings: []trafficProfileSeriesMappingRequest{
			{SeriesKey: "checkout", GroupName: "checkout", ART: &trafficProfileARTRequest{Basis: "operator_peak", Value: 1, Unit: "seconds"}},
			{SeriesKey: "search", GroupName: "search", ART: &trafficProfileARTRequest{Basis: "operator_peak", Value: 1000, Unit: "milliseconds"}},
		},
		ValueSemantics: trafficprofile.SemanticsTPS,
		GapPolicy:      trafficprofile.GapHold,
		Window:         TrafficProfileWindow{Mode: "full"},
		Amplitude:      TrafficProfileAmplitude{Mode: "factor", Value: 1},
		ScenarioDraft: CreateScenarioRequest{
			Name: "ART replay", Duration: "20s", ScenarioMode: "goal_oriented",
			GoalConfig: &GoalConfig{MinVUsers: &existingMin, MaxVUsers: &existingMax},
			Groups:     []CreateGroupEntry{{Name: "checkout", VUsers: 5}, {Name: "search", VUsers: 5}, {Name: "static", VUsers: 5}},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("derive = %d %s", recorder.Code, recorder.Body.String())
	}
	var response trafficProfileDeriveResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	sizing := response.TrafficProfile.Sizing
	if sizing == nil || !sizing.Complete || sizing.ProfilePeakVUsers != 110 || sizing.StaticPeakVUsers != 5 || sizing.EstimatedPeakVUsers != 115 || sizing.SuggestedMinVUsers != 1 || sizing.SuggestedMaxVUsers != 225 {
		t.Fatalf("sizing = %#v", sizing)
	}
	if response.ScenarioDraft.GoalConfig == nil || *response.ScenarioDraft.GoalConfig.MinVUsers != 1 || *response.ScenarioDraft.GoalConfig.MaxVUsers != 225 {
		t.Fatalf("guardrails = %#v", response.ScenarioDraft.GoalConfig)
	}
	for _, mapping := range response.TrafficProfile.Mappings {
		if mapping.ART == nil || mapping.ART.MinARTMS != 1000 || mapping.ART.AverageARTMS != 1000 || mapping.ART.PeakARTMS != 1000 || mapping.ART.EstimatedPeakVUsers != 100 {
			t.Fatalf("ART mapping = %#v", mapping)
		}
	}

	request.Mappings[1].ART = nil
	body, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.handleDeriveTrafficProfile(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("incomplete ART derive = %d %s", recorder.Code, recorder.Body.String())
	}
	response = trafficProfileDeriveResponse{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TrafficProfile.Sizing != nil || response.ScenarioDraft.GoalConfig == nil || *response.ScenarioDraft.GoalConfig.MinVUsers != existingMin || *response.ScenarioDraft.GoalConfig.MaxVUsers != existingMax {
		t.Fatalf("incomplete ART should preserve guardrails: profile=%#v guardrails=%#v", response.TrafficProfile.Sizing, response.ScenarioDraft.GoalConfig)
	}
}

func TestTrafficProfileARTSizingSaturatesEstimateAndClampsGuardrail(t *testing.T) {
	profile := testTrafficProfile("tps")
	profile.Mappings[0].Points[1].Target = 1e12
	profile.Mappings[0].ART = &TrafficProfileART{Basis: "operator_peak", Points: []trafficprofile.ARTPoint{{OffsetMS: 0, ARTMS: 1e9}}, PeakARTMS: 1e9}
	minimum, maximum := 1, maxManagedTestVUsers
	if err := validateTrafficProfile(profile, []CreateGroupEntry{{Name: "checkout"}}, "10s", "goal_oriented", &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum}); err != nil {
		t.Fatal(err)
	}
	if profile.Mappings[0].ART.EstimatedPeakVUsers != maxCapacityUnit || profile.Sizing == nil || profile.Sizing.SuggestedMaxVUsers != maxManagedTestVUsers {
		t.Fatalf("saturated sizing = mapping %#v sizing %#v", profile.Mappings[0].ART, profile.Sizing)
	}
}

func TestTrafficProfileRejectsFileARTEvidenceForQuerySource(t *testing.T) {
	profile := testTrafficProfile("tps")
	profile.Source = TrafficProfileSource{Kind: "prometheus", Prometheus: &TrafficProfilePrometheusSource{Query: "sum(rate(requests_total[1m]))", Range: TrafficProfileQueryRange{Mode: "relative", Lookback: "1h", StepSeconds: 5}}}
	profile.Mappings[0].ART = &TrafficProfileART{Basis: "source_series", SeriesKey: "art", Points: []trafficprofile.ARTPoint{{OffsetMS: 0, ARTMS: 100}}, PeakARTMS: 100}
	minimum, maximum := 1, 10
	err := validateTrafficProfile(profile, []CreateGroupEntry{{Name: "checkout"}}, "10s", "goal_oriented", &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum})
	if err == nil || !strings.Contains(err.Error(), "file-backed ART requires a file source") {
		t.Fatalf("query source ART error = %v", err)
	}
}

func TestAnalyzeTrafficProfileSeparatesWorkloadAndARTColumns(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{
		"timestamp_column": "timestamp",
		"value_column":     "checkout_tps",
		"art_column":       "checkout_art_seconds",
		"timezone":         "UTC",
	} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", "traffic.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("timestamp,checkout_tps,checkout_art_seconds\n2026-01-01T00:00:00Z,10,0.2\n2026-01-01T00:00:15Z,20,\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/traffic-profile/analyze", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	server.handleAnalyzeTrafficProfile(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("analyze = %d %s", recorder.Code, recorder.Body.String())
	}
	var response trafficProfileAnalyzeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Series) != 1 || response.Series[0].Series.Name != "checkout_tps" || len(response.ARTSeries) != 1 || response.ARTSeries[0].Series.Name != "checkout_art_seconds" || response.ARTSeries[0].Stats.Count != 1 || response.ARTSeries[0].Stats.Max != 0.2 {
		t.Fatalf("analysis = %#v", response)
	}
	if response.Source.File == nil || len(response.Source.File.ARTColumns) != 1 || response.Source.File.ARTColumns[0] != "checkout_art_seconds" {
		t.Fatalf("source = %#v", response.Source)
	}
}

func TestAnalyzeCSVDrivenRejectsNonCSVFiles(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "schedule.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("not-an-xlsx")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/csv-driven/analyze", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	server.handleAnalyzeCSVDriven(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType || !strings.Contains(recorder.Body.String(), ".csv") {
		t.Fatalf("CSV Driven XLSX analysis = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestValidateCSVDrivenDeriveRequest(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	minimum, maximum := 2, 20
	base := trafficProfileDeriveRequest{
		Source: TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "schedule.csv"}},
		Series: []trafficprofile.Series{{Key: "checkout", Name: "Checkout", Points: []trafficprofile.Point{
			{Timestamp: start, Value: 2}, {Timestamp: start.Add(5 * time.Second), Value: 8},
		}}},
		Mappings:       []trafficProfileSeriesMappingRequest{{SeriesKey: "checkout", GroupName: "Checkout"}},
		ValueSemantics: trafficprofile.SemanticsVUsers,
		GapPolicy:      trafficprofile.GapDrop,
		Window:         TrafficProfileWindow{Mode: "full"},
		ScenarioDraft: CreateScenarioRequest{
			ScenarioMode: "vuser_driven",
			Groups:       []CreateGroupEntry{{Name: "Checkout", VUsers: 1}},
		},
	}
	if err := validateCSVDrivenDeriveRequest(base); err != nil {
		t.Fatalf("valid VUser request: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*trafficProfileDeriveRequest)
		want   string
	}{
		{name: "query source", mutate: func(request *trafficProfileDeriveRequest) { request.Source = TrafficProfileSource{Kind: "prometheus"} }, want: "source.kind file"},
		{name: "non csv source", mutate: func(request *trafficProfileDeriveRequest) { request.Source.File.Filename = "schedule.xlsx" }, want: ".csv source"},
		{name: "target duration", mutate: func(request *trafficProfileDeriveRequest) { request.TargetDuration = "1m" }, want: "derive duration"},
		{name: "amplitude", mutate: func(request *trafficProfileDeriveRequest) {
			request.Amplitude = TrafficProfileAmplitude{Mode: "factor", Value: 2}
		}, want: "amplitude"},
		{name: "fractional vusers", mutate: func(request *trafficProfileDeriveRequest) { request.Series[0].Points[1].Value = 8.5 }, want: "whole number"},
		{name: "mismatched bounds", mutate: func(request *trafficProfileDeriveRequest) {
			request.Series = append(request.Series, trafficprofile.Series{Key: "search", Name: "Search", Points: []trafficprofile.Point{{Timestamp: start, Value: 1}, {Timestamp: start.Add(10 * time.Second), Value: 2}}})
			request.Mappings = append(request.Mappings, trafficProfileSeriesMappingRequest{SeriesKey: "search", GroupName: "Search"})
			request.ScenarioDraft.Groups = append(request.ScenarioDraft.Groups, CreateGroupEntry{Name: "Search", VUsers: 1})
		}, want: "same start, end"},
		{name: "unmatched group", mutate: func(request *trafficProfileDeriveRequest) {
			request.Mappings[0].GroupName = "Missing"
		}, want: "was not found"},
		{name: "duplicate series mapping", mutate: func(request *trafficProfileDeriveRequest) {
			request.Mappings = append(request.Mappings, trafficProfileSeriesMappingRequest{SeriesKey: "checkout", GroupName: "Search"})
			request.ScenarioDraft.Groups = append(request.ScenarioDraft.Groups, CreateGroupEntry{Name: "Search", VUsers: 1})
		}, want: "mapped more than once"},
		{name: "TPS without ART", mutate: func(request *trafficProfileDeriveRequest) {
			request.ValueSemantics = trafficprofile.SemanticsTPS
			request.ScenarioDraft.ScenarioMode = "goal_oriented"
			request.ScenarioDraft.GoalConfig = &GoalConfig{MinVUsers: &minimum, MaxVUsers: &maximum}
		}, want: "requires ART"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.Source.File = &TrafficProfileFileSource{Filename: base.Source.File.Filename}
			request.Series = append([]trafficprofile.Series(nil), base.Series...)
			request.Series[0].Points = append([]trafficprofile.Point(nil), base.Series[0].Points...)
			request.Mappings = append([]trafficProfileSeriesMappingRequest(nil), base.Mappings...)
			request.ScenarioDraft.Groups = append([]CreateGroupEntry(nil), base.ScenarioDraft.Groups...)
			test.mutate(&request)
			if err := validateCSVDrivenDeriveRequest(request); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCSVDrivenDerivePreservesExactPointsAndInfersFinalBucket(t *testing.T) {
	server := NewServer(ServerConfig{})
	start := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	request := trafficProfileDeriveRequest{
		Source: TrafficProfileSource{Kind: "file", File: &TrafficProfileFileSource{Filename: "schedule.csv", TimestampColumn: "elapsed", TimestampMode: "relative", RelativeStartTime: "00:00", ValueColumns: []string{"checkout"}, Timezone: "UTC"}},
		Series: []trafficprofile.Series{{Key: "checkout", Name: "checkout", Points: []trafficprofile.Point{
			{Timestamp: start, Value: 10},
			{Timestamp: start.Add(5 * time.Second), Value: 20},
			{Timestamp: start.Add(10 * time.Second), Value: 7},
		}}},
		Mappings:       []trafficProfileSeriesMappingRequest{{SeriesKey: "checkout", GroupName: "Checkout"}},
		ValueSemantics: trafficprofile.SemanticsVUsers,
		GapPolicy:      trafficprofile.GapDrop,
		Window:         TrafficProfileWindow{Mode: "full"},
		ScenarioDraft: CreateScenarioRequest{
			Name: "Exact CSV", Duration: "1m", ScenarioMode: "vuser_driven",
			Groups: []CreateGroupEntry{{Name: "Checkout", VUsers: 1}},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleDeriveCSVDriven(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/csv-driven/derive", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("derive = %d %s", recorder.Code, recorder.Body.String())
	}
	var response trafficProfileDeriveResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TrafficProfile.TargetDuration != "15s" || response.TrafficProfile.SourceDuration != "15s" {
		t.Fatalf("inferred duration = %s / %s", response.TrafficProfile.TargetDuration, response.TrafficProfile.SourceDuration)
	}
	if response.ScenarioDraft.WorkloadShapeID == nil || *response.ScenarioDraft.WorkloadShapeID != csvDrivenShapeID || response.ScenarioDraft.ScenarioMode != "vuser_driven" {
		t.Fatalf("scenario draft = %#v", response.ScenarioDraft)
	}
	points := response.TrafficProfile.Mappings[0].Points
	if len(points) != 3 || points[0].Target != 10 || points[1].Target != 20 || points[2].Target != 7 || points[1].OffsetMS != 5_000 || points[2].OffsetMS != 10_000 {
		t.Fatalf("exact points = %#v", points)
	}
}

func TestValidateCSVDrivenSheetRowsRejectsMissingAuthoredValues(t *testing.T) {
	sheet, err := trafficprofile.ParseCSV([]byte("elapsed,group,target\n0,checkout,10\n5,checkout,\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = validateCSVDrivenSheetRows(sheet, "elapsed", "group", []string{"target"}, nil)
	if err == nil || !strings.Contains(err.Error(), "row 3 column \"target\"") {
		t.Fatalf("missing value error = %v", err)
	}
}

func TestValidateCSVDrivenSheetRowsRejectsAmbiguousColumnRoles(t *testing.T) {
	sheet, err := trafficprofile.ParseCSV([]byte("elapsed,group,target\n0,checkout,10\n5,checkout,20\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		seriesColumn string
		values       []string
		art          []string
	}{
		{name: "time as target", values: []string{"elapsed"}},
		{name: "series as target", seriesColumn: "group", values: []string{"group"}},
		{name: "target as ART", values: []string{"target"}, art: []string{"target"}},
		{name: "duplicate target", values: []string{"target", "target"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCSVDrivenSheetRows(sheet, "elapsed", test.seriesColumn, test.values, test.art)
			if err == nil || !strings.Contains(err.Error(), "cannot be used as both") {
				t.Fatalf("ambiguous role error = %v", err)
			}
		})
	}
}

func TestValidateCSVDrivenPersistedProfileContract(t *testing.T) {
	profile := testTrafficProfile("vusers")
	profile.GapPolicy = string(trafficprofile.GapDrop)
	profile.Mappings[0].SourceStart = "2026-09-02T12:00:00Z"
	profile.Mappings[0].SourceEnd = "2026-09-02T12:00:10Z"
	profile.Mappings[0].SourceDuration = "10s"
	groups := []CreateGroupEntry{{Name: "checkout", VUsers: 5}}
	if err := validateTrafficProfileForWorkloadShape(csvDrivenShapeID, profile, groups); err != nil {
		t.Fatalf("valid persisted CSV profile: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*TrafficProfile)
		want   string
	}{
		{name: "xlsx source", mutate: func(profile *TrafficProfile) { profile.Source.File.Filename = "traffic.xlsx" }, want: "CSV file"},
		{name: "stretched duration", mutate: func(profile *TrafficProfile) { profile.TargetDuration = "20s" }, want: "must equal"},
		{name: "window transform", mutate: func(profile *TrafficProfile) { profile.Window = TrafficProfileWindow{Mode: "peak", Duration: "5s"} }, want: "window selection"},
		{name: "amplitude transform", mutate: func(profile *TrafficProfile) { profile.Amplitude = TrafficProfileAmplitude{Mode: "factor", Value: 2} }, want: "amplitude"},
		{name: "fractional VUsers", mutate: func(profile *TrafficProfile) { profile.Mappings[0].Points[1].Target = 4.5 }, want: "whole number"},
		{name: "duplicate series", mutate: func(profile *TrafficProfile) {
			profile.Mappings = append(profile.Mappings, profile.Mappings[0])
			profile.Mappings[1].GroupName = "search"
		}, want: "mapped more than once"},
		{name: "ART in VUser mode", mutate: func(profile *TrafficProfile) {
			profile.Mappings[0].ART = &TrafficProfileART{Basis: "operator_peak", PeakARTMS: 100}
		}, want: "cannot contain ART"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneTrafficProfile(profile)
			test.mutate(candidate)
			candidateGroups := groups
			if test.name == "duplicate series" {
				candidateGroups = append(candidateGroups, CreateGroupEntry{Name: "search", VUsers: 5})
			}
			if err := validateTrafficProfileForWorkloadShape(csvDrivenShapeID, candidate, candidateGroups); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("persisted profile error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStoreScenarioEnforcesCSVDrivenPersistedProfileContract(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := server.runner.StoreScript(&dsl.Script{ID: "csv-contract-script", Name: "CSV Contract", Version: "1.0.0", Protocol: "http", Actions: []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/"}}})
	profile := testTrafficProfile("vusers")
	profile.Source.File.Filename = "traffic.xlsx"
	shape := csvDrivenShapeID
	_, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Invalid CSV shape", WorkloadShapeID: &shape, Duration: "10s", ScenarioMode: "vuser_driven", TrafficProfile: profile,
		Groups: []CreateGroupEntry{{Name: "checkout", ScriptID: script.ID, VUsers: 5}},
	})
	if err == nil || !strings.Contains(err.Error(), "CSV file") {
		t.Fatalf("store scenario error = %v", err)
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
