package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

const concurrentPublicationFixtureProtocol = "concurrent-publication-http-fixture"

var concurrentPublicationFixtureOnce sync.Once
var concurrentPublicationFixtures sync.Map

type concurrentPublicationFixtureState struct {
	token           string
	outputMode      string
	panicOnTeardown bool
	teardowns       atomic.Int64
}

type concurrentPublicationHTTP struct {
	*httpprotocol.HTTP
	state    *concurrentPublicationFixtureState
	observed atomic.Bool
	redact   protocol.TextRedactor
	active   func() bool
}

func (p *concurrentPublicationHTTP) Init(ctx context.Context, config map[string]any) error {
	if state, found := concurrentPublicationFixtures.Load(config["base_url"]); found {
		p.state = state.(*concurrentPublicationFixtureState)
	}
	return p.HTTP.Init(ctx, config)
}
func (p *concurrentPublicationHTTP) SetRuntimeTextRedactor(redact protocol.TextRedactor) {
	p.redact = redact
}
func (p *concurrentPublicationHTTP) SetRuntimeSecretActivity(active func() bool) { p.active = active }
func (p *concurrentPublicationHTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result, err := p.HTTP.Execute(ctx, action)
	if result == nil || p.state == nil || !strings.HasPrefix(action.Target, "/producer") {
		return result, err
	}
	p.observed.Store(true)
	switch p.state.outputMode {
	case "invalid", "error_invalid":
		result.Extractions = map[string]string{"access_token": p.state.token}
		for i := 0; i < 5; i++ {
			result.Extractions[fmt.Sprintf("ordinary_%d", i)] = strings.Repeat("x", 16<<10)
		}
	case "incomplete":
		result.ExtractionsIncomplete = true
	}
	if p.state.outputMode == "error" || p.state.outputMode == "error_invalid" {
		return result, fmt.Errorf("synthetic diagnostic error echo: %s", p.state.token)
	}
	return result, err
}
func (p *concurrentPublicationHTTP) Teardown(ctx context.Context) error {
	p.HTTP.Teardown(ctx)
	if p.state == nil || !p.observed.Load() {
		return nil
	}
	p.state.teardowns.Add(1)
	if p.state.panicOnTeardown {
		panic(p.state.token)
	}
	return fmt.Errorf("synthetic teardown echo: %s", p.state.token)
}

func newConcurrentPublicationFixture(t *testing.T, mode string, panicOnTeardown bool) (*httptest.Server, *concurrentPublicationFixtureState) {
	t.Helper()
	concurrentPublicationFixtureOnce.Do(func() {
		protocol.Register(concurrentPublicationFixtureProtocol, func() protocol.Protocol {
			return &concurrentPublicationHTTP{HTTP: &httpprotocol.HTTP{}}
		})
	})
	state := &concurrentPublicationFixtureState{token: "synthetic-late-lifecycle-value", outputMode: mode, panicOnTeardown: panicOnTeardown}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": state.token})
	}))
	concurrentPublicationFixtures.Store(server.URL, state)
	t.Cleanup(func() { concurrentPublicationFixtures.Delete(server.URL); server.Close() })
	return server, state
}

func concurrentPublicationActions() (dsl.Action, dsl.Action) {
	ordinary := dsl.Action{Name: "Earlier ordinary alias", Type: "GET", Target: "/ordinary", Extractors: []dsl.Extractor{{Name: "ordinary_alias", Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
	producer := dsl.Action{Name: "Sensitive producer", Type: "GET", Target: "/producer", Extractors: []dsl.Extractor{{Name: "access_token", Source: "body", Type: "jsonpath", Expression: "$.value", OnMiss: "fail"}}}
	return ordinary, producer
}

func TestConcurrentHTTPInvalidOrIncompleteWorkerOutputClosesPublication(t *testing.T) {
	for _, mode := range []string{"invalid", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			server, state := newConcurrentPublicationFixture(t, mode, false)
			ordinary, producer := concurrentPublicationActions()
			group := dsl.Action{Name: "Incomplete extraction group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": []dsl.Action{ordinary, producer}}}
			var progress [][]byte
			report, err := ReplayScript(context.Background(), &dsl.Script{Name: "Worker contract", Protocol: concurrentPublicationFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL}, Actions: []dsl.Action{group}}, ReplayOptions{IgnoreThinkTime: true, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
			if err != nil || report.Success || !report.RuntimeEvidenceUnavailable || report.FinalVariables != nil || len(report.Events) != 2 {
				t.Fatal("incomplete worker output did not fail the whole group before publication")
			}
			data, _ := json.Marshal(report)
			for _, value := range append(progress, data) {
				if bytes.Contains(value, []byte(state.token)) {
					t.Fatal("incomplete classification exposed an earlier neutral alias")
				}
			}
		})
	}
}

func TestConcurrentHTTPBuiltinRejectedSensitiveOutputClosesPublication(t *testing.T) {
	token := strings.Repeat("z", protocol.MaxExtractionValueBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()
	_, producer := concurrentPublicationActions()
	ordinary := dsl.Action{Name: "Earlier echo", Type: "GET", Target: "/ordinary"}
	group := dsl.Action{Name: "Rejected extraction group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": []dsl.Action{ordinary, producer}}}
	vu := newConcurrentVU(t, server.URL)
	sink := &concurrentCaptureSink{}
	vu.ResultSink = sink
	result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{}, nil)
	if err != nil || result.Success || vu.runtimeCredentialRedactionErr == nil || vu.replayFinalVariables() != nil {
		t.Fatal("built-in rejected extraction failed to invalidate classification")
	}
	for _, retained := range sink.results {
		data, _ := json.Marshal(retained)
		if bytes.Contains(data, []byte(token[:128])) {
			t.Fatal("built-in discarded output left an unknown public prefix")
		}
	}
}

func TestConcurrentHTTPLifecycleTeardownUsesObservedAndUnavailablePolicy(t *testing.T) {
	for _, mode := range []string{"observed", "invalid"} {
		for _, runner := range []string{"replay", "shakeout", "engine_closed", "engine_open", "group_closed", "group_open", "group_at_once"} {
			t.Run(mode+"/"+runner, func(t *testing.T) {
				server, state := newConcurrentPublicationFixture(t, mode, false)
				_, producer := concurrentPublicationActions()
				group := dsl.Action{Name: "Observed producer group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []dsl.Action{producer}}}
				script := &dsl.Script{Name: "Lifecycle publication", Protocol: concurrentPublicationFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL, RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Actions: []dsl.Action{group}}
				var logs bytes.Buffer
				previous := slog.Default()
				slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
				defer slog.SetDefault(previous)
				var runErr error
				switch runner {
				case "replay":
					_, runErr = ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
				case "shakeout":
					// Shakeout runs an ordinary leaf, so test the same observed
					// matcher through its separate deferred protocol cleanup.
					script.Actions = []dsl.Action{producer}
					runErr = RunScriptShakeout(context.Background(), "Synthetic", script)
				case "engine_closed", "engine_open":
					if runner == "engine_open" {
						script.Config.RunLogic.WorkloadModel = "open"
						script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 100}
					}
					e, err := New(Config{Script: script, VUsers: 1, Duration: 100 * time.Millisecond})
					if err != nil {
						t.Fatal(err)
					}
					_, runErr = e.Run(context.Background())
				default:
					strategy := "at_runtime"
					if runner == "group_open" {
						script.Config.RunLogic.WorkloadModel = "open"
						script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 100}
					}
					if runner == "group_at_once" {
						strategy = "at_once"
						script.Init, script.Actions = script.Actions, []dsl.Action{{Name: "Benign", Type: "GET", Target: "/benign"}}
					}
					mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "Synthetic", Script: script, VUsers: 1, InitStrategy: strategy}}, Duration: 100 * time.Millisecond})
					if err != nil {
						t.Fatal(err)
					}
					_, runErr = mge.Run(context.Background())
				}
				if mode == "observed" && runErr != nil {
					t.Fatal("observed-token fixture changed successful execution")
				}
				if state.teardowns.Load() == 0 || !strings.Contains(logs.String(), "protocol teardown failed") || strings.Contains(logs.String(), state.token) {
					t.Fatal("lifecycle cleanup lost its outcome or exposed detail under a stale matcher")
				}
			})
		}
	}
}

func TestConcurrentHTTPLifecycleCapacityFailureUpdatesRetainedCallbacksAndPanic(t *testing.T) {
	server, state := newConcurrentPublicationFixture(t, "observed", false)
	p := &concurrentPublicationHTTP{HTTP: &httpprotocol.HTTP{}}
	if err := p.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	values := make([]string, maxRuntimeSecretRedactionValues)
	for i := range values {
		values[i] = fmt.Sprintf("synthetic-lifecycle-capacity-%08d", i)
	}
	vu := &VUser{Proto: p, Script: &dsl.Script{Protocol: concurrentPublicationFixtureProtocol}, Variables: make(map[string]string)}
	if err := vu.replaceRuntimeCredentialScope("extraction:observed", values); err != nil {
		t.Fatal("supported observation history was not admitted")
	}
	base := vu.RedactionConfig
	vu.bindRuntimeTextRedactor()
	redact, active := p.redact, p.active
	if redact == nil || active == nil {
		t.Fatal("fixture did not retain both protocol callbacks")
	}
	// Exercise concurrent callback reads during the owning goroutine's atomic
	// publication transition. No callback reads mutable VUser fields.
	var callbackWG sync.WaitGroup
	callbackWG.Add(1)
	go func() {
		defer callbackWG.Done()
		for i := 0; i < 8; i++ {
			redact("synthetic unrelated text")
			active()
		}
	}()
	err := vu.mergeExtractions(&protocol.Result{Extractions: map[string]string{"access_token": state.token}})
	callbackWG.Wait()
	if err == nil || vu.runtimeCredentialRedactionErr == nil || vu.RedactionConfig != base || redact(state.token) == state.token || !active() {
		t.Fatal("failed classification changed the exact matcher or left retained callbacks permissive")
	}
	if scrubSensitiveTextWithConfig("", vu.resultPublicationRedactionConfig()) != "" {
		t.Fatal("unavailable projection changed empty omission shape")
	}
	p.observed.Store(true)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	if err := runProtocolTeardown(p, vu.resultPublicationRedactionConfig); err == nil || errors.Is(err, protocol.ErrContainmentUnproven) {
		t.Fatal("teardown failure changed its execution/sentinel disposition")
	}
	func() { defer recoverVUserGoroutine(0, vu.resultPublicationRedactionConfig); panic(state.token) }()
	if state.teardowns.Load() != 1 || !strings.Contains(logs.String(), "protocol teardown failed") || !strings.Contains(logs.String(), "vuser goroutine panicked") || strings.Contains(logs.String(), state.token) {
		t.Fatal("capacity failure exposed teardown/panic detail or lost lifecycle containment")
	}
}

func TestSerialHTTPIncompleteOutputBlocksRetriesFutureTrafficAndFinalDetail(t *testing.T) {
	token := strings.Repeat("z", protocol.MaxExtractionValueBytes+1)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	defer server.Close()
	_, producer := concurrentPublicationActions()
	script := &dsl.Script{Name: "Serial incomplete output", Protocol: concurrentFixtureProtocol, Config: dsl.ScriptConfig{
		BaseURL:       server.URL,
		ErrorHandling: dsl.ErrorHandlingConfig{MaxRetries: 2, ContinueOnError: dsl.ContinueOnErrorValue(true)},
		Log:           dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"data_returned", "advanced_trace"}},
	}, Actions: []dsl.Action{{Name: "Earlier ordinary echo", Type: "GET", Target: "/ordinary"}, producer, {Name: "Following consumer", Type: "GET", Target: "/consume"}}}
	registerConcurrentHTTPFixture()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	var progress [][]byte
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
	if err != nil || report.Success || !report.RuntimeEvidenceUnavailable || report.FinalVariables != nil || len(report.Events) != 3 || requests.Load() != 2 {
		t.Fatal("serial discarded output allowed retry/following traffic or retained runtime state")
	}
	if !report.Events[0].Success || report.Events[0].ActionType != "GET" || report.Events[0].StatusCode != 200 {
		t.Fatal("unavailable serial projection changed earlier structural outcome evidence")
	}
	data, _ := json.Marshal(report)
	for _, value := range append(progress, data, logs.Bytes()) {
		if bytes.Contains(value, []byte(token[:128])) {
			t.Fatal("serial discarded output retained an unknown token prefix")
		}
	}
}

func TestSerialHTTPSensitiveObservationPrecedesResultLogs(t *testing.T) {
	server, state := newConcurrentPublicationFixture(t, "observed", false)
	_, producer := concurrentPublicationActions()
	script := &dsl.Script{Name: "Serial observed logging", Protocol: concurrentPublicationFixtureProtocol, Config: dsl.ScriptConfig{
		BaseURL: server.URL, Log: dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"data_returned"}},
	}, Actions: []dsl.Action{producer}}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil || !report.Success || report.RuntimeEvidenceUnavailable || report.FinalVariables["access_token"] != redactedPayloadValue {
		t.Fatal("serial valid output was not classified and retained correctly")
	}
	if strings.Contains(logs.String(), state.token) || !strings.Contains(logs.String(), redactedPayloadValue) || !strings.Contains(logs.String(), "replay action ok") {
		t.Fatal("serial result logs preceded sensitive-value observation")
	}
}

func TestConcurrentHTTPDiagnosticResultErrorClosesGroupAndSerialPublication(t *testing.T) {
	for _, mode := range []string{"error", "error_invalid"} {
		for _, runner := range []string{"load", "replay", "serial"} {
			t.Run(mode+"/"+runner, func(t *testing.T) {
				server, state := newConcurrentPublicationFixture(t, mode, false)
				ordinary, producer := concurrentPublicationActions()
				group := dsl.Action{Name: "Diagnostic error group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 1, "requests": []dsl.Action{ordinary, producer}}}
				var logs bytes.Buffer
				previous := slog.Default()
				slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
				defer slog.SetDefault(previous)
				if runner == "load" {
					p := &concurrentPublicationHTTP{HTTP: &httpprotocol.HTTP{}}
					if err := p.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
						t.Fatal(err)
					}
					vu := newConcurrentVU(t, server.URL)
					vu.Proto, vu.Script.Protocol = p, concurrentPublicationFixtureProtocol
					vu.Script.Config.Log = dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"data_returned"}}
					sink := &concurrentCaptureSink{}
					vu.ResultSink = sink
					result, err := vu.executeHTTPSequenceAction(context.Background(), group, dsl.ErrorHandlingConfig{MaxRetries: 2}, nil)
					if err != nil || result.Success || vu.runtimeCredentialRedactionErr == nil || vu.replayFinalVariables() != nil || vu.Variables["access_token"] != "" {
						t.Fatal("discarded diagnostic extraction did not close load publication without merging it")
					}
					for _, retained := range sink.results {
						data, _ := json.Marshal(retained)
						if bytes.Contains(data, []byte(state.token)) {
							t.Fatal("diagnostic result error exposed an earlier sibling alias")
						}
					}
					runProtocolTeardown(p, vu.resultPublicationRedactionConfig)
				} else {
					actions := []dsl.Action{group}
					if runner == "serial" {
						actions = []dsl.Action{producer, {Name: "Following", Type: "GET", Target: "/consume"}}
					}
					script := &dsl.Script{Name: "Diagnostic result error", Protocol: concurrentPublicationFixtureProtocol, Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{MaxRetries: 2, ContinueOnError: dsl.ContinueOnErrorValue(true)}, Log: dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"data_returned"}}}, Actions: actions}
					var progress [][]byte
					report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, OnProgress: func(event ReplayProgressEvent) { data, _ := json.Marshal(event); progress = append(progress, data) }})
					if err != nil || report.Success || !report.RuntimeEvidenceUnavailable || report.FinalVariables != nil || len(report.Events) != 2 {
						t.Fatal("discarded diagnostic output did not invalidate replay/serial evidence")
					}
					data, _ := json.Marshal(report)
					for _, value := range append(progress, data) {
						if bytes.Contains(value, []byte(state.token)) {
							t.Fatal("discarded diagnostic output exposed opaque error or retained aliases")
						}
					}
				}
				if strings.Contains(logs.String(), state.token) || state.teardowns.Load() != 1 {
					t.Fatal("diagnostic error publication leaked into logs or changed cleanup execution")
				}
			})
		}
	}
}
