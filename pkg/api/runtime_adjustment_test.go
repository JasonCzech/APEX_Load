package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRuntimeAdjustmentPreviewRouteReturnsRevisionAndImpact(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	started := time.Now().UTC().Add(-time.Minute)
	server.runner.tests["runtime-preview"] = &ManagedTest{
		ID: "runtime-preview", Name: "Runtime preview", Status: "RUNNING", Duration: 10 * time.Minute, DurationStr: "10m",
		ProjectID: "default", StartedAt: &started, Groups: []TestGroup{{Name: "buyers", VUsers: 10}}, TotalVUsers: 10,
	}
	requestBody, _ := json.Marshal(RuntimeAdjustmentRequest{Groups: []RuntimeAdjustmentGroupRequest{{Name: "buyers", TargetDuration: "15m"}}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/runtime-preview/runtime-adjustments/preview", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var preview RuntimeAdjustmentPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Revision == "" || preview.Decision != "allowed" || preview.Impact.ScenarioDuration != "15m0s" {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestApplyRuntimeAdjustmentRequestDerivesScenarioDurationAndLoad(t *testing.T) {
	test := &ManagedTest{
		ID: "run", Status: "RUNNING", Duration: 10 * time.Minute, DurationStr: "10m",
		Groups:      []TestGroup{{Name: "buyers", VUsers: 10}, {Name: "observers", Role: GroupRoleObserver, VUsers: 2}},
		TotalVUsers: 12,
	}
	target := 15
	proposal, groups, err := applyRuntimeAdjustmentRequest(test, RuntimeAdjustmentRequest{Groups: []RuntimeAdjustmentGroupRequest{
		{Name: "buyers", TargetDuration: "20m", TargetVUsers: &target},
	}})
	if err != nil {
		t.Fatalf("applyRuntimeAdjustmentRequest: %v", err)
	}
	if proposal.Duration != 20*time.Minute || proposal.TotalVUsers != 17 {
		t.Fatalf("proposal duration/load = %s/%d, want 20m/17", proposal.Duration, proposal.TotalVUsers)
	}
	if groups[1].TargetDuration != "10m0s" || proposal.Groups[1].TargetDuration != "10m0s" {
		t.Fatalf("legacy group did not inherit scenario duration: %#v", groups[1])
	}
}

func TestApplyRuntimeAdjustmentRequestRejectsObserverOverflowAndEndedGroup(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		group TestGroup
		want  string
	}{
		{name: "observer", group: TestGroup{Name: "g", Role: GroupRoleObserver, VUsers: 1}, want: "cannot exceed"},
		{name: "ended", group: TestGroup{Name: "g", VUsers: 1, RuntimeState: "ended"}, want: "has ended"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			test := &ManagedTest{ID: "run", Status: "RUNNING", Duration: time.Minute, Groups: []TestGroup{testCase.group}}
			target := observerMaxVUsers + 1
			_, _, err := applyRuntimeAdjustmentRequest(test, RuntimeAdjustmentRequest{Groups: []RuntimeAdjustmentGroupRequest{{Name: "g", TargetDuration: "2m", TargetVUsers: &target}}})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestRuntimeAdjustmentRevisionChangesWithRuntimeTargets(t *testing.T) {
	test := &ManagedTest{ID: "run", Status: "RUNNING", Duration: time.Minute, Groups: []TestGroup{{Name: "g", VUsers: 1}}}
	before := runtimeAdjustmentRevision(test)
	test.Groups[0].TargetDuration = "2m"
	if after := runtimeAdjustmentRevision(test); after == before {
		t.Fatal("revision did not change with group duration")
	}
}

func TestLaunchCapacityAtReleasesCompletedGroupDemandWithoutDroppingScenario(t *testing.T) {
	now := time.Now().UTC()
	event := launchCapacityEvent{StartAt: now, EndAt: now.Add(20 * time.Minute), Generators: 4, VUsers: 100, Groups: []launchGroupCapacity{
		{EndAt: now.Add(5 * time.Minute), VUsers: 40}, {EndAt: now.Add(15 * time.Minute), VUsers: 60},
	}}
	if generators, vusers := launchCapacityAt(event, now.Add(time.Minute)); generators != 4 || vusers != 100 {
		t.Fatalf("initial demand = %d generators/%d VUsers", generators, vusers)
	}
	if generators, vusers := launchCapacityAt(event, now.Add(10*time.Minute)); generators != 3 || vusers != 60 {
		t.Fatalf("post-group demand = %d generators/%d VUsers, want 3/60", generators, vusers)
	}
	if generators, vusers := launchCapacityAt(event, now.Add(16*time.Minute)); generators != 1 || vusers != 0 {
		t.Fatalf("post-load baseline demand = %d generators/%d VUsers, want 1/0", generators, vusers)
	}
}

func TestLaunchCapacityAtHonorsDelayedGroupActivationAndOverlapPeak(t *testing.T) {
	now := time.Now().UTC()
	groups := []launchGroupCapacity{
		{StartAt: now, EndAt: now.Add(10 * time.Minute), VUsers: 40},
		{StartAt: now.Add(5 * time.Minute), EndAt: now.Add(15 * time.Minute), VUsers: 60},
		{StartAt: now.Add(10 * time.Minute), EndAt: now.Add(20 * time.Minute), VUsers: 80},
	}
	event := launchCapacityEvent{
		StartAt: now, EndAt: now.Add(20 * time.Minute), Generators: 4,
		VUsers: launchGroupPeakVUsers(groups), GroupProfileKnown: true, Groups: groups,
	}
	if event.VUsers != 140 {
		t.Fatalf("overlap peak = %d, want 140", event.VUsers)
	}
	if _, vusers := launchCapacityAt(event, now.Add(time.Minute)); vusers != 40 {
		t.Fatalf("pre-offset demand = %d, want 40", vusers)
	}
	if _, vusers := launchCapacityAt(event, now.Add(6*time.Minute)); vusers != 100 {
		t.Fatalf("overlap demand = %d, want 100", vusers)
	}
	if _, vusers := launchCapacityAt(event, now.Add(11*time.Minute)); vusers != 140 {
		t.Fatalf("later overlap demand = %d, want 140", vusers)
	}
}

func TestManagedGroupPeakVUsersUsesHalfOpenOffsetWindows(t *testing.T) {
	groups := []TestGroup{
		{Name: "first", VUsers: 60, Stages: []storage.LoadStage{{Type: "hold", Duration: "1m", TargetVUsers: 60}}},
		{Name: "second", VUsers: 80, StartOffset: "1m", Stages: []storage.LoadStage{{Type: "hold", Duration: "1m", TargetVUsers: 80}}},
	}
	if got := managedGroupPeakVUsers(groups, 2*time.Minute); got != 80 {
		t.Fatalf("non-overlapping peak = %d, want 80", got)
	}
}

func TestLaunchGroupCapacitiesCapsOffsetGroupAtScenarioDeadline(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	test := &ManagedTest{
		Duration: 5 * time.Minute,
		Groups:   []TestGroup{{Name: "late", VUsers: 10, StartOffset: "1m", TargetDuration: "5m", RampDown: "1m"}},
	}
	windows := launchGroupCapacities(test, start, start)
	if len(windows) != 1 {
		t.Fatalf("windows = %#v", windows)
	}
	if want := start.Add(5 * time.Minute); !windows[0].EndAt.Equal(want) {
		t.Fatalf("offset window end = %s, want scenario deadline %s", windows[0].EndAt, want)
	}
}

func TestLaunchCapacityAtDoesNotRestoreWholeRunDemandAfterAllGroupsEnd(t *testing.T) {
	now := time.Now().UTC()
	event := launchCapacityEvent{
		StartAt: now, EndAt: now.Add(20 * time.Minute), Generators: 4, VUsers: 100,
		GroupProfileKnown: true,
	}
	if generators, vusers := launchCapacityAt(event, now.Add(time.Minute)); generators != 1 || vusers != 0 {
		t.Fatalf("post-group baseline demand = %d generators/%d VUsers, want 1/0", generators, vusers)
	}
}

func TestLaunchCapacityAtRetainsDecreaseUntilRampCompletes(t *testing.T) {
	now := time.Now().UTC()
	event := launchCapacityEvent{
		StartAt: now, EndAt: now.Add(20 * time.Minute), Generators: 4, VUsers: 100, GroupProfileKnown: true,
		Groups: []launchGroupCapacity{{
			EndAt: now.Add(15 * time.Minute), VUsers: 40,
			BeforeReleaseVUsers: 100, ReleaseAt: now.Add(5 * time.Minute),
		}},
	}
	if _, vusers := launchCapacityAt(event, now.Add(4*time.Minute)); vusers != 100 {
		t.Fatalf("demand before ramp completion = %d, want 100", vusers)
	}
	if _, vusers := launchCapacityAt(event, now.Add(6*time.Minute)); vusers != 40 {
		t.Fatalf("demand after ramp completion = %d, want 40", vusers)
	}
}

func TestLaunchSampleTimesIncludesRampReleaseBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	releaseAt := now.Add(5 * time.Minute)
	window := LaunchWindow{StartAt: now, EndAt: now.Add(20 * time.Minute)}
	event := launchCapacityEvent{StartAt: now, EndAt: window.EndAt, GroupProfileKnown: true, Groups: []launchGroupCapacity{{EndAt: now.Add(10 * time.Minute), ReleaseAt: releaseAt}}}
	samples := launchSampleTimes(window, []launchCapacityEvent{event}, nil, nil)
	for _, sample := range samples {
		if sample.Equal(releaseAt) {
			return
		}
	}
	t.Fatalf("ramp release boundary %v missing from samples: %v", releaseAt, samples)
}

func TestLaunchSampleTimesIncludesGroupActivationBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	startAt := now.Add(5 * time.Minute)
	window := LaunchWindow{StartAt: now, EndAt: now.Add(20 * time.Minute)}
	event := launchCapacityEvent{
		StartAt: now, EndAt: window.EndAt, GroupProfileKnown: true,
		Groups: []launchGroupCapacity{{StartAt: startAt, EndAt: now.Add(10 * time.Minute), VUsers: 10}},
	}
	for _, sample := range launchSampleTimes(window, []launchCapacityEvent{event}, nil, nil) {
		if sample.Equal(startAt) {
			return
		}
	}
	t.Fatal("group activation boundary is absent from launch samples")
}

func TestInitializeRuntimeGroupsUsesActiveClockAndLegacyDuration(t *testing.T) {
	started := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{
		Duration:         10 * time.Minute,
		StartDelay:       "30s",
		ResourceBaseline: true,
		Groups:           []TestGroup{{Name: "legacy", VUsers: 2}, {Name: "short", VUsers: 1, TargetDuration: "3m"}},
	}
	initializeRuntimeGroupsLocked(test, started)
	if test.Groups[0].TargetDuration != "10m0s" || test.Groups[0].RuntimeState != "running" {
		t.Fatalf("legacy group = %#v", test.Groups[0])
	}
	want := started.Add(15*time.Minute + 30*time.Second)
	if test.Groups[0].EffectiveEndAt == nil || !test.Groups[0].EffectiveEndAt.Equal(want) {
		t.Fatalf("legacy end = %v, want %v", test.Groups[0].EffectiveEndAt, want)
	}
	if test.Groups[1].EffectiveEndAt == nil || !test.Groups[1].EffectiveEndAt.Equal(started.Add(8*time.Minute+30*time.Second)) {
		t.Fatalf("short group end = %v", test.Groups[1].EffectiveEndAt)
	}
}

func TestRefreshRuntimeGroupEffectiveEndsAddsCompletedPause(t *testing.T) {
	started := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{Duration: time.Minute, CumulativePause: 20 * time.Second, Groups: []TestGroup{{Name: "load", TargetDuration: "1m", RuntimeState: "running"}}}
	refreshRuntimeGroupEffectiveEndsLocked(test, started)
	want := started.Add(80 * time.Second)
	if test.Groups[0].EffectiveEndAt == nil || !test.Groups[0].EffectiveEndAt.Equal(want) {
		t.Fatalf("effective end = %v, want %v", test.Groups[0].EffectiveEndAt, want)
	}
}
