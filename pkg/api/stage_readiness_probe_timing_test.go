package api

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type stageReadinessProbeCall struct {
	at     time.Time
	target int
}

type stageReadinessProbeBackend struct {
	mu         sync.Mutex
	rejections int
	calls      []stageReadinessProbeCall
	admitted   chan time.Time
	admitOnce  sync.Once
}

func (*stageReadinessProbeBackend) Run(*TestRunner, *ManagedTest) {}

func (*stageReadinessProbeBackend) requiresInitialRampReadinessProbe() {}

func (backend *stageReadinessProbeBackend) ScaleVUsers(_ *TestRunner, _ *ManagedTest, _ string, targetVUsers int) error {
	now := time.Now()
	backend.mu.Lock()
	backend.calls = append(backend.calls, stageReadinessProbeCall{at: now, target: targetVUsers})
	call := len(backend.calls)
	backend.mu.Unlock()
	if call <= backend.rejections {
		return fmt.Errorf("generator still preparing: %w", engine.ErrInitialRampInProgress)
	}
	backend.admitOnce.Do(func() { backend.admitted <- now })
	return nil
}

func (backend *stageReadinessProbeBackend) snapshotCalls() []stageReadinessProbeCall {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]stageReadinessProbeCall(nil), backend.calls...)
}

func TestDistributedStageReadinessProbePreservesFirstHoldAndRateLimitsRetries(t *testing.T) {
	const holdDuration = 120 * time.Millisecond
	backend := &stageReadinessProbeBackend{
		rejections: 2,
		admitted:   make(chan time.Time, 1),
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.setExecutionBackend(backend)
	runner.tests["slow-initial-ramp"] = &ManagedTest{
		ID:           "slow-initial-ramp",
		Name:         "Slow distributed initial ramp",
		Status:       "RUNNING",
		ScenarioMode: "vuser_driven",
		ProjectID:    storage.DefaultProjectID,
		TotalVUsers:  1,
		Groups: []TestGroup{{
			Name:          "buyers",
			VUsers:        1,
			stageDriven:   true,
			initialVUsers: 1,
			currentVUsers: 1,
		}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	completed := make(chan time.Time, 1)
	go func() {
		runner.runStageSchedule(ctx, "slow-initial-ramp", "buyers", []storage.LoadStage{{
			Type: "hold", Duration: holdDuration.String(), TargetVUsers: 1,
		}}, 0, nil)
		completed <- time.Now()
	}()

	var admittedAt time.Time
	select {
	case admittedAt = <-backend.admitted:
	case <-ctx.Done():
		t.Fatalf("readiness probe was never admitted: %v", ctx.Err())
	}
	var completedAt time.Time
	select {
	case completedAt = <-completed:
	case <-ctx.Done():
		t.Fatalf("stage schedule did not finish: %v", ctx.Err())
	}

	calls := backend.snapshotCalls()
	if len(calls) != 3 {
		t.Fatalf("readiness probe calls = %#v, want two rejections followed by one admission", calls)
	}
	for index, call := range calls {
		if call.target != 1 {
			t.Fatalf("readiness probe call %d target = %d, want configured target 1", index, call.target)
		}
		if index == 0 {
			continue
		}
		spacing := call.at.Sub(calls[index-1].at)
		if spacing < initialRampScaleRetryInterval-50*time.Millisecond {
			t.Fatalf("readiness retry %d spacing = %s, want approximately %s rather than a 100 Hz poll", index, spacing, initialRampScaleRetryInterval)
		}
		if spacing > 4*initialRampScaleRetryInterval {
			t.Fatalf("readiness retry %d spacing = %s, want bounded near %s", index, spacing, initialRampScaleRetryInterval)
		}
	}
	if elapsed := completedAt.Sub(admittedAt); elapsed < holdDuration {
		t.Fatalf("first hold elapsed only %s after readiness admission, want at least %s; initial-ramp retries consumed the hold", elapsed, holdDuration)
	}
}

func TestAdoptedStageScheduleJoinsCurrentOrFinalTarget(t *testing.T) {
	for _, elapsed := range []time.Duration{150 * time.Millisecond, time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			backend := &stageReadinessProbeBackend{admitted: make(chan time.Time, 1)}
			runner := NewTestRunner(1, NewHub(), nil)
			runner.setExecutionBackend(backend)
			started := time.Now().Add(-elapsed)
			runner.tests["adopted"] = &ManagedTest{
				ID: "adopted", Status: "RUNNING", ScenarioMode: "vuser_driven",
				StartedAt: &started, stageScheduleResume: true, TotalVUsers: 1,
				Groups: []TestGroup{{Name: "buyers", VUsers: 1, currentVUsers: 1, stageDriven: true}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			runner.runStageSchedule(ctx, "adopted", "buyers", []storage.LoadStage{
				{Type: "hold", Duration: "100ms", TargetVUsers: 2},
				{Type: "hold", Duration: "100ms", TargetVUsers: 4},
			}, 0, nil)
			calls := backend.snapshotCalls()
			if len(calls) != 1 || calls[0].target != 4 {
				t.Fatalf("resumed scale calls = %+v, want only the current/final target 4", calls)
			}
		})
	}
}

// An open-model group's rate stages run on the generators' arrival
// scheduler: the controller's schedule must label each level without probing
// or scaling the group's users, which an open workload rejects.
func TestDistributedRateStageScheduleOnlyLabelsLevels(t *testing.T) {
	backend := &stageReadinessProbeBackend{admitted: make(chan time.Time, 1)}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.setExecutionBackend(backend)
	open := &dsl.Script{Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"}, Pacing: dsl.PacingConfig{Type: "tps", TPS: 10}}}
	runner.tests["rate-sweep"] = &ManagedTest{
		ID: "rate-sweep", Status: "RUNNING", ScenarioMode: "vuser_driven", TotalVUsers: 1,
		ProjectID: storage.DefaultProjectID,
		Groups:    []TestGroup{{Name: "llm", VUsers: 1, currentVUsers: 1, initialVUsers: 1, stageDriven: true, script: open}},
	}
	tps := func(v float64) *float64 { return &v }
	stages := []storage.LoadStage{
		{Name: "warm", Type: "ramp_up", Duration: "80ms", TargetTPS: tps(10), Warmup: true},
		{Name: "Level 1", Type: "hold", Duration: "80ms", TargetTPS: tps(10)},
		{Name: "drain", Type: "ramp_down", Duration: "80ms", TargetTPS: tps(2)},
	}
	seen := make(map[int]bool)
	var seenMu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for ctx.Err() == nil {
			runner.mu.RLock()
			if test := runner.tests["rate-sweep"]; test != nil && test.Groups[0].ActiveStage != nil {
				seenMu.Lock()
				seen[test.Groups[0].ActiveStage.StageIndex] = true
				seenMu.Unlock()
			}
			runner.mu.RUnlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()
	started := time.Now()
	runner.runStageSchedule(ctx, "rate-sweep", "llm", stages, 0, nil)
	elapsed := time.Since(started)
	cancel()
	<-watchDone
	if calls := backend.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("an open-model rate schedule scaled users: %+v", calls)
	}
	if elapsed < 240*time.Millisecond {
		t.Fatalf("schedule returned after %s, before its three stages ran", elapsed)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	for index := range stages {
		if !seen[index] {
			t.Fatalf("stage %d was never labelled the active level (seen %v)", index, seen)
		}
	}
}

// A restored or HA-hydrated open-model group with rate stages keeps its users:
// only stages that set target_vusers scale them, as at compile, and an open
// workload with zero users is refused by the engine.
func TestRestoredOpenModelRateStageGroupKeepsItsUsers(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := &dsl.Script{
		ID: "open-rate-restore", Name: "Open rate", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://example.com",
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}},
	}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	tps := func(v float64) *float64 { return &v }
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Restored open rate run", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 10, Stages: []storage.LoadStage{
			{Type: "ramp_up", Duration: "1m", TargetTPS: tps(20)},
			{Type: "hold", Duration: "2m", TargetTPS: tps(20)},
		}}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	specs, err := restoreStoredEngineGroupSpecs(test)
	if err != nil {
		t.Fatalf("restoreStoredEngineGroupSpecs: %v", err)
	}
	if specs[0].VUsers != 10 || specs[0].AllowZeroVUsers || !test.Groups[0].stageDriven {
		t.Fatalf("restored spec VUsers=%d AllowZero=%v stageDriven=%v, want the compiled 10 users and a running schedule", specs[0].VUsers, specs[0].AllowZeroVUsers, test.Groups[0].stageDriven)
	}
}

func storeOpenRateScript(t *testing.T, server *Server, id string) *dsl.Script {
	t.Helper()
	script := &dsl.Script{
		ID: id, Name: id, Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://example.com",
			RunLogic: dsl.RunLogicConfig{WorkloadModel: "open"},
			Pacing:   dsl.PacingConfig{Type: "tps", TPS: 10},
		},
		Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}},
	}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	return script
}

// Stages that set no arrival rate leave the script's rate running; with at
// least one rate stage, a stage without a rate holds the current rate and a
// target-less ramp_down ramps it to zero.
func TestOpenModelStageRatesCompile(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := storeOpenRateScript(t, server, "open-rate-compile")
	tps := func(v float64) *float64 { return &v }
	goal, err := server.runner.CreateTest(CreateTestRequest{Name: "goal", Duration: "10m", ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{MaxErrorPct: tps(5)},
		Groups:     []CreateGroupEntry{{ScriptID: script.ID, Stages: []storage.LoadStage{{Type: "hold", Duration: "2m", TargetVUsers: 5}}}}})
	if err != nil {
		t.Fatalf("goal CreateTest: %v", err)
	}
	if got := goal.Groups[0].script.Config.Pacing; len(got.RateStages) != 0 || got.TPS != 10 {
		t.Fatalf("user-only stages must leave the script's 10 TPS running, got %+v", got)
	}
	mixed, err := server.runner.CreateTest(CreateTestRequest{Name: "mixed", Duration: "10m", ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{MaxErrorPct: tps(5)},
		Groups: []CreateGroupEntry{{ScriptID: script.ID, Stages: []storage.LoadStage{
			{Type: "ramp_down", Duration: "10s"},
			{Type: "ramp_up", Duration: "30s", TargetTPS: tps(20)},
			{Type: "hold", Duration: "60s", TargetVUsers: 5},
		}}}})
	if err != nil {
		t.Fatalf("mixed CreateTest: %v", err)
	}
	stages := mixed.Groups[0].script.Config.Pacing.RateStages
	if len(stages) != 3 {
		t.Fatalf("rate stages = %+v", stages)
	}
	if stages[0].TargetTPS != 10 || stages[0].EndTPS == nil || *stages[0].EndTPS != 0 {
		t.Fatalf("a leading target-less ramp_down must ramp the script's 10 TPS to zero, got %+v", stages[0])
	}
	if stages[2].TargetTPS != 20 || stages[2].EndTPS != nil || stages[2].DurationSeconds != 60 {
		t.Fatalf("a user-only hold must hold the current 20 TPS, got %+v", stages[2])
	}
}

// A revised copy of an open-model rate test compiles the revision's stages,
// not the source's, and keeps its level schedule.
func TestRevisedOpenModelRateTestRebuildsItsStages(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := storeOpenRateScript(t, server, "open-rate-revision")
	tps := func(v float64) *float64 { return &v }
	src, err := server.runner.CreateTest(CreateTestRequest{Name: "src", Duration: "10m", StartDelay: "1h",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "10s", TargetTPS: tps(20)}}}}})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	idx := 0
	rev, err := server.runner.CreateTest(CreateTestRequest{Name: "rev", Duration: "10m", StartDelay: "1h", RevisionSourceTestID: src.ID,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, Name: src.Groups[0].Name, RevisionSourceGroupIndex: &idx, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "40s", TargetTPS: tps(80)}}}}})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	group := rev.Groups[0]
	stages := group.script.Config.Pacing.RateStages
	if !group.stageDriven || len(stages) != 1 || stages[0].DurationSeconds != 40 || stages[0].EndTPS == nil || *stages[0].EndTPS != 80 {
		t.Fatalf("revision stageDriven=%v rate stages=%+v, want one 40s ramp to 80 TPS", group.stageDriven, stages)
	}
}

// Restore derives stage-driven groups as compile does: a closed group whose
// only stage is a target-less ramp_down is not stage driven, so a restart
// does not scale it to zero.
func TestRestoreMatchesCompileForTargetlessClosedRampDown(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := &dsl.Script{ID: "closed-ramp-down", Name: "closed", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}}}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	test, err := server.runner.CreateTest(CreateTestRequest{Name: "rd", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 10, Stages: []storage.LoadStage{{Type: "ramp_down", Duration: "1m"}}}}})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	compiled := test.Groups[0].stageDriven
	specs, err := restoreStoredEngineGroupSpecs(test)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if test.Groups[0].stageDriven != compiled || specs[0].VUsers != 10 {
		t.Fatalf("restore stageDriven=%v (compile %v) VUsers=%d, want them to match compile with 10 users", test.Groups[0].stageDriven, compiled, specs[0].VUsers)
	}
}

// Restore reproduces compile in every scenario mode: a goal-oriented
// open-model group with rate stages keeps its level schedule after a restart.
func TestRestoredGoalOrientedOpenRateGroupStaysStageDriven(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := storeOpenRateScript(t, server, "open-rate-goal-restore")
	tps := func(v float64) *float64 { return &v }
	test, err := server.runner.CreateTest(CreateTestRequest{Name: "goal", Duration: "10m", ScenarioMode: "goal_oriented",
		GoalConfig: &GoalConfig{MaxErrorPct: tps(5)},
		Groups:     []CreateGroupEntry{{ScriptID: script.ID, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "40s", TargetTPS: tps(80)}}}}})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	compiled, users := test.Groups[0].stageDriven, test.Groups[0].VUsers
	specs, err := restoreStoredEngineGroupSpecs(test)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !compiled || !test.Groups[0].stageDriven || specs[0].VUsers != users {
		t.Fatalf("compile stageDriven=%v, restore stageDriven=%v VUsers=%d (compiled %d)", compiled, test.Groups[0].stageDriven, specs[0].VUsers, users)
	}
}

// Whether a revision rebuilds its rate stages depends on where they came
// from: rate stages compiled from the stages are rebuilt, even under a
// runtime pacing override; rate stages a script authored are kept.
func TestRevisionRateStageProvenance(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	tps := func(v float64) *float64 { return &v }
	idx := 0

	derived := storeOpenRateScript(t, server, "open-rate-override")
	override := &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "tps", TPS: 12}}
	src, err := server.runner.CreateTest(CreateTestRequest{Name: "src", Duration: "10m", StartDelay: "1h", RuntimeSettings: override,
		Groups: []CreateGroupEntry{{ScriptID: derived.ID, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "10s", TargetTPS: tps(20)}}}}})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	rev, err := server.runner.CreateTest(CreateTestRequest{Name: "rev", Duration: "10m", StartDelay: "1h", RuntimeSettings: override, RevisionSourceTestID: src.ID,
		Groups: []CreateGroupEntry{{ScriptID: derived.ID, Name: src.Groups[0].Name, RevisionSourceGroupIndex: &idx, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "40s", TargetTPS: tps(80)}}}}})
	if err != nil {
		t.Fatalf("revision: %v", err)
	}
	if stages := rev.Groups[0].script.Config.Pacing.RateStages; len(stages) != 1 || stages[0].DurationSeconds != 40 || *stages[0].EndTPS != 80 {
		t.Fatalf("a revision under a pacing override kept the source's rate stages: %+v", stages)
	}

	end := 5.0
	authored := storeOpenRateScript(t, server, "open-rate-authored")
	authored.Config.Pacing.RateStages = []dsl.PacingRateStage{{DurationSeconds: 30, TargetTPS: 15, EndTPS: &end}}
	if _, err := server.runner.StoreScriptDurable(authored); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	src2, err := server.runner.CreateTest(CreateTestRequest{Name: "src2", Duration: "10m", StartDelay: "1h",
		Groups: []CreateGroupEntry{{ScriptID: authored.ID, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "10s", TargetTPS: tps(20)}}}}})
	if err != nil {
		t.Fatalf("authored source: %v", err)
	}
	rev2, err := server.runner.CreateTest(CreateTestRequest{Name: "rev2", Duration: "10m", StartDelay: "1h", RevisionSourceTestID: src2.ID,
		Groups: []CreateGroupEntry{{ScriptID: authored.ID, Name: src2.Groups[0].Name, RevisionSourceGroupIndex: &idx, Stages: []storage.LoadStage{{Type: "ramp_up", Duration: "40s", TargetTPS: tps(80)}}}}})
	if err != nil {
		t.Fatalf("authored revision: %v", err)
	}
	if stages := rev2.Groups[0].script.Config.Pacing.RateStages; len(stages) != 1 || stages[0].DurationSeconds != 30 || stages[0].TargetTPS != 15 {
		t.Fatalf("a revision dropped the script's authored rate stages: %+v", stages)
	}
}

// A stage longer than a rate stage may run compiles to pieces along the same
// line in every scenario mode: the script validates, restore keeps the group
// stage driven, and the joins between pieces are not RCA levels.
func TestLongOpenModelRateStagesCompileToPieces(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := storeOpenRateScript(t, server, "open-rate-long")
	tps := func(v float64) *float64 { return &v }
	for _, mode := range []string{"", "goal_oriented"} {
		t.Run("mode="+mode, func(t *testing.T) {
			req := CreateTestRequest{Name: "long " + mode, Duration: "4h", ScenarioMode: mode,
				Groups: []CreateGroupEntry{{ScriptID: script.ID, Stages: []storage.LoadStage{
					{Type: "ramp_up", Duration: "90m", TargetTPS: tps(30)},
					{Type: "hold", Duration: "2h", TargetTPS: tps(30)},
				}}}}
			if mode == "goal_oriented" {
				req.GoalConfig = &GoalConfig{MaxErrorPct: tps(5)}
			}
			test, err := server.runner.CreateTest(req)
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			group := test.Groups[0]
			stages := group.script.Config.Pacing.RateStages
			end := func(s dsl.PacingRateStage) float64 {
				if s.EndTPS == nil {
					return -1
				}
				return *s.EndTPS
			}
			if len(stages) != 4 ||
				stages[0].DurationSeconds != 3600 || stages[0].TargetTPS != 0 || end(stages[0]) != 20 ||
				stages[1].DurationSeconds != 1800 || stages[1].TargetTPS != 20 || end(stages[1]) != 30 ||
				stages[2].DurationSeconds != 3600 || stages[2].TargetTPS != 30 || stages[2].EndTPS != nil ||
				stages[3].DurationSeconds != 3600 || stages[3].TargetTPS != 30 || stages[3].EndTPS != nil {
				t.Fatalf("want the 90m ramp as 60m+30m pieces through 20 TPS and the 2h hold as two 1h pieces, got %+v", stages)
			}
			if issues := dsl.ValidateScriptIssues(group.script); len(issues) != 0 {
				t.Fatalf("the compiled script must validate, got %v", issues)
			}
			if !group.stageDriven || !rateStagesDerivedFromStages(group.script, group.Stages) {
				t.Fatalf("stageDriven=%v derived=%v, want a stage-driven group whose rate stages its stages compile to", group.stageDriven, rateStagesDerivedFromStages(group.script, group.Stages))
			}
			users := group.VUsers
			specs, err := restoreStoredEngineGroupSpecs(test)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if !test.Groups[0].stageDriven || specs[0].VUsers != users {
				t.Fatalf("restore stageDriven=%v VUsers=%d (compiled %d)", test.Groups[0].stageDriven, specs[0].VUsers, users)
			}
			if levels := workloadGroup(test.Groups[0], test.Groups[0].script).Levels; len(levels) != 1 || levels[0] != 30 {
				t.Fatalf("RCA levels = %v, want only the 30 TPS target", levels)
			}
		})
	}
}

// Stages that compile to arrival-rate stages may need at most
// maxDerivedRateStages one-hour pieces; compile refuses more before building
// any. Stages that compile to none (a closed group's) are not limited.
func TestRateStagePieceLimitAppliesWhereStagesCompile(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	open := storeOpenRateScript(t, server, "open-rate-pieces")
	tps := func(v float64) *float64 { return &v }
	hold := func(d time.Duration) []storage.LoadStage {
		return []storage.LoadStage{{Type: "hold", Duration: d.String(), TargetTPS: tps(10)}}
	}
	_, err := server.runner.CreateTest(CreateTestRequest{Name: "too long", Duration: "1h", Groups: []CreateGroupEntry{{ScriptID: open.ID, Stages: hold(time.Duration(math.MaxInt64))}}})
	if err == nil || !strings.Contains(err.Error(), "shorten the stages") {
		t.Fatalf("an open-model rate hold of %s: error = %v, want the piece limit", time.Duration(math.MaxInt64), err)
	}
	many := make([]storage.LoadStage, maxManagedLoadStagesPerGroup)
	for i := range many {
		many[i] = storage.LoadStage{Type: "hold", Duration: "5h", TargetTPS: tps(10)}
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{Name: "many", Duration: "1h", Groups: []CreateGroupEntry{{ScriptID: open.ID, Stages: many}}}); err == nil || !strings.Contains(err.Error(), "shorten the stages") {
		t.Fatalf("256 five-hour open-model rate holds: error = %v, want the piece limit", err)
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{Name: "at the limit", Duration: "1h", Groups: []CreateGroupEntry{{ScriptID: open.ID, Stages: hold(maxDerivedRateStages * time.Hour)}}}); err != nil {
		t.Fatalf("an open-model rate hold of exactly the limit: %v", err)
	}
	closed := &dsl.Script{ID: "closed-rate-pieces", Name: "closed", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/orders"}}}
	if _, err := server.runner.StoreScriptDurable(closed); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{Name: "closed goal", Duration: "1h", ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{MaxErrorPct: tps(5)},
		Groups: []CreateGroupEntry{{ScriptID: closed.ID, Stages: []storage.LoadStage{{Type: "hold", Duration: (2000 * time.Hour).String(), TargetVUsers: 5, TargetTPS: tps(10)}}}}}); err != nil {
		t.Fatalf("a closed goal group's long target_tps hold compiles to no rate stages and must not be limited: %v", err)
	}
	if got := len(deriveOpenModelRateStages(hold(time.Duration(math.MaxInt64)), 10)); got != maxDerivedRateStages {
		t.Fatalf("derivation built %d pieces, want it capped at %d", got, maxDerivedRateStages)
	}
}
