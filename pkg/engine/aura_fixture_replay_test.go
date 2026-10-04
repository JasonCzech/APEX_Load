package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurafixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const auraFixtureHTTPProtocol = "aura-fixture-replay-http"

var registerAuraFixtureHTTPProtocol sync.Once

func newReplayAuraFixture(t *testing.T, scenario aurafixture.Scenario, delay time.Duration) (*aurafixture.Fixture, string) {
	t.Helper()
	// HTTP replay never requests browser assets. Supplying inert bytes keeps the
	// shared helper's asset contract explicit without claiming a browser test.
	fixture, err := aurafixture.New(aurafixture.Options{
		ReactJS:    []byte("/* unused by HTTP-only replay */"),
		ReactDOMJS: []byte("/* unused by HTTP-only replay */"),
		Scenario:   scenario, ResponseDelay: delay,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	registerAuraFixtureHTTPProtocol.Do(func() {
		protocol.Register(auraFixtureHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	return fixture, server.URL
}

func replayAuraFixtureScript(baseURL string, cycles int) *dsl.Script {
	bootstrap := dsl.Action{
		Name: "Bootstrap session", Type: "request", Method: "GET", Target: "/bootstrap",
		Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
		Extractors: []dsl.Extractor{
			{Name: "aura_token", Source: "body", Type: "jsonpath", Expression: "$.token", OnMiss: "fail"},
			{Name: "aura_context", Source: "body", Type: "jsonpath", Expression: "$.context", OnMiss: "fail"},
			{Name: "aura_first_id", Source: "body", Type: "jsonpath", Expression: "$.action_ids[0]", OnMiss: "fail"},
			{Name: "aura_second_id", Source: "body", Type: "jsonpath", Expression: "$.action_ids[1]", OnMiss: "fail"},
		},
	}
	batch := dsl.Action{
		Name: "Submit Aura batch", Type: "request", Method: "POST", Target: "/aura",
		Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:       "aura.token={{var.aura_token}}&aura.context={{var.aura_context}}&message=%7B%22actions%22%3A%5B%7B%22id%22%3A%22{{var.aura_first_id}}%22%2C%22descriptor%22%3A%22fixture%22%2C%22params%22%3A%7B%7D%7D%2C%7B%22id%22%3A%22{{var.aura_second_id}}%22%2C%22descriptor%22%3A%22fixture%22%2C%22params%22%3A%7B%7D%7D%5D%7D",
		Metadata:   map[string]any{dsl.ImportURLFormTemplateValueEncodingMetadataKey: dsl.ImportURLFormTemplateValueEncodingQueryComponent},
		Assertions: []dsl.Assertion{{Type: "aura_actions_success"}},
	}
	script := &dsl.Script{Name: "Local session-bound Aura fixture", Protocol: auraFixtureHTTPProtocol,
		Config: dsl.ScriptConfig{BaseURL: baseURL}, Actions: []dsl.Action{bootstrap, batch}}
	if cycles > 1 {
		// Replay performs one action phase. These are explicit warm cycles,
		// not a claim that ReplayScript implements RunLogic.Iterations.
		script.Actions = []dsl.Action{{Name: "Session cycles", Type: "control_loop", Control: &dsl.ControlFlow{Count: cycles, Steps: script.Actions}}}
	}
	return script
}

func runReplayAuraFixture(t *testing.T, script *dsl.Script) *ReplayReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func replayAuraFixtureEvents(t *testing.T, report *ReplayReport) ([]ReplayActionEvent, []aura.Report) {
	t.Helper()
	events := make([]ReplayActionEvent, 0)
	observed := make([]aura.Report, 0)
	for _, event := range report.Events {
		if event.ActionName != "Submit Aura batch" {
			continue
		}
		encoded, err := json.Marshal(event.ProtocolEvidence["aura"])
		if err != nil {
			t.Fatal("structural Aura evidence could not be encoded")
		}
		var outcome aura.Report
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if string(encoded) == "null" || decoder.Decode(&outcome) != nil {
			t.Fatal("structural Aura evidence missing or contains an unrecognized field")
		}
		// The helper mints opaque synthetic credentials at runtime. Retained
		// Aura diagnostics must remain counts, enums and action ordinals.
		for _, forbidden := range []string{`"token":`, `"descriptor":`, `"returnValue":`, `"nonce":`} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("Aura evidence contains a forbidden field: %s", forbidden)
			}
		}
		events = append(events, event)
		observed = append(observed, outcome)
	}
	return events, observed
}

func TestReplayAuraFixtureExtractsSessionAndFreshIDsBeforeStrictOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name              string
		scenario          aurafixture.Scenario
		success, complete bool
		matched           int
		issue             aura.Code
		failedState       aura.State
	}{
		{name: "reordered success", scenario: aurafixture.Success, success: true, complete: true, matched: 2},
		{name: "HTTP200 error", scenario: aurafixture.Error, complete: true, matched: 2, issue: aura.NonSuccessState, failedState: aura.Error},
		{name: "HTTP200 incomplete", scenario: aurafixture.Incomplete, complete: true, matched: 2, issue: aura.NonSuccessState, failedState: aura.Incomplete},
		{name: "HTTP200 missing", scenario: aurafixture.Missing, matched: 1, issue: aura.MissingResponse},
		{name: "HTTP200 duplicate", scenario: aurafixture.Duplicate, issue: aura.DuplicateID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, baseURL := newReplayAuraFixture(t, tc.scenario, 0)
			report := runReplayAuraFixture(t, replayAuraFixtureScript(baseURL, 1))
			events, outcomes := replayAuraFixtureEvents(t, report)
			if report.Success != tc.success || len(events) != 1 || len(report.Events) != 2 {
				t.Fatalf("session replay success=%t, Aura batches=%d, total events=%d", report.Success, len(events), len(report.Events))
			}
			stats := fixture.Stats()
			if stats.AcceptedBatches != 1 || stats.RejectedBatches != 0 || stats.RequestActions != 2 || stats.DistinctActionIDs != 2 || stats.DistinctSessions != 1 || stats.DistinctTokens != 1 || stats.ColdBootstraps != 1 || stats.WarmBootstraps != 0 {
				t.Fatalf("session credentials and extracted IDs were not accepted: %+v", stats)
			}
			outcome := outcomes[0]
			if events[0].StatusCode != 200 || events[0].Success != tc.success || outcome.Success != tc.success || outcome.Complete != tc.complete || !outcome.HasResponse || outcome.RequestActions != 2 || outcome.MatchedActions != tc.matched {
				t.Fatalf("incorrect strict outcome: status=%d success=%t complete=%t requested=%d matched=%d", events[0].StatusCode, outcome.Success, outcome.Complete, outcome.RequestActions, outcome.MatchedActions)
			}
			if tc.issue != "" && (len(outcome.Issues) == 0 || outcome.Issues[0].Code != tc.issue) {
				t.Fatalf("missing expected safe issue: %s", tc.issue)
			}
			if tc.failedState != "" && (len(outcome.Outcomes) != 2 || outcome.Outcomes[1].Ordinal != 2 || outcome.Outcomes[1].State != tc.failedState) {
				t.Fatalf("reordered second action did not preserve its safe state: %s", tc.failedState)
			}
			if tc.success && (len(outcome.Outcomes) != 2 || outcome.Outcomes[0].Ordinal != 1 || outcome.Outcomes[1].Ordinal != 2 || outcome.Outcomes[0].State != aura.Success || outcome.Outcomes[1].State != aura.Success) {
				t.Fatal("reordered response actions were not matched to the outgoing request ordinals")
			}
		})
	}
}

func TestReplayAuraFixtureAwaitsDelayedBatchResponse(t *testing.T) {
	const delay = 50 * time.Millisecond
	_, baseURL := newReplayAuraFixture(t, aurafixture.Success, delay)
	report := runReplayAuraFixture(t, replayAuraFixtureScript(baseURL, 1))
	events, outcomes := replayAuraFixtureEvents(t, report)
	if !report.Success || len(events) != 1 || !outcomes[0].Success || events[0].DurationMs < float64(delay.Microseconds())/1000 {
		t.Fatalf("delayed outcome was not awaited: success=%t batches=%d", report.Success, len(events))
	}
}

func TestReplayAuraFixtureWarmCyclesReuseSessionWithFreshActionIDs(t *testing.T) {
	fixture, baseURL := newReplayAuraFixture(t, aurafixture.Success, 0)
	report := runReplayAuraFixture(t, replayAuraFixtureScript(baseURL, 2))
	events, outcomes := replayAuraFixtureEvents(t, report)
	if !report.Success || len(events) != 2 {
		t.Fatalf("warm cycle replay did not complete: success=%t batches=%d", report.Success, len(events))
	}
	for i, outcome := range outcomes {
		if !events[i].Success || events[i].StatusCode != 200 || !outcome.Success || !outcome.Complete || outcome.MatchedActions != 2 {
			t.Fatalf("warm cycle %d did not match both fresh actions", i+1)
		}
	}
	stats := fixture.Stats()
	if stats.BootstrapRequests != 2 || stats.ColdBootstraps != 1 || stats.WarmBootstraps != 1 || stats.ColdBatches != 1 || stats.WarmBatches != 1 || stats.AcceptedBatches != 2 || stats.RequestActions != 4 || stats.ResponseActions != 4 || stats.DistinctSessions != 1 || stats.DistinctTokens != 1 || stats.DistinctActionIDs != 4 || stats.RejectedBatches != 0 {
		t.Fatalf("warm cycles did not retain session scope with fresh IDs: %+v", stats)
	}
}

func TestReplayAuraFixtureFreshReplaysIsolateSessionAndActionIDs(t *testing.T) {
	fixture, baseURL := newReplayAuraFixture(t, aurafixture.Success, 0)
	for replay := 0; replay < 2; replay++ {
		report := runReplayAuraFixture(t, replayAuraFixtureScript(baseURL, 1))
		events, outcomes := replayAuraFixtureEvents(t, report)
		if !report.Success || len(events) != 1 || !outcomes[0].Success {
			t.Fatalf("fresh replay %d did not complete: success=%t batches=%d", replay+1, report.Success, len(events))
		}
	}
	stats := fixture.Stats()
	if stats.BootstrapRequests != 2 || stats.ColdBootstraps != 2 || stats.WarmBootstraps != 0 || stats.ColdBatches != 2 || stats.WarmBatches != 0 || stats.AcceptedBatches != 2 || stats.RequestActions != 4 || stats.DistinctSessions != 2 || stats.DistinctTokens != 2 || stats.DistinctActionIDs != 4 || stats.RejectedBatches != 0 {
		t.Fatalf("fresh replays did not isolate session credentials and IDs: %+v", stats)
	}
}

func TestReplayAuraFixtureRejectsConsumedActionIDsWithoutFreshBootstrap(t *testing.T) {
	fixture, baseURL := newReplayAuraFixture(t, aurafixture.Success, 0)
	script := replayAuraFixtureScript(baseURL, 1)
	// Retaining an old extraction deliberately proves this target distinguishes
	// fresh producer values from request-local response ID matching alone.
	script.Init = []dsl.Action{script.Actions[0]}
	script.Actions = []dsl.Action{{Name: "Reuse consumed IDs", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{script.Actions[1]}}}}
	report := runReplayAuraFixture(t, script)
	events, _ := replayAuraFixtureEvents(t, report)
	if report.Success || len(events) != 2 || !events[0].Success || events[1].Success || events[1].StatusCode != 409 {
		t.Fatalf("consumed-ID control did not fail: success=%t batches=%d", report.Success, len(events))
	}
	stats := fixture.Stats()
	if stats.AcceptedBatches != 1 || stats.RejectedBatches != 1 || stats.ReusedIDRejections != 1 || stats.ScopeRejections != 0 || stats.RequestActions != 2 || stats.DistinctActionIDs != 2 {
		t.Fatalf("consumed-ID control was not rejected by the fixture: %+v", stats)
	}
}
