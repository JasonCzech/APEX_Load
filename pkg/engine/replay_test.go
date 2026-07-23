package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayTestProtocol struct {
	execute    func(action *protocol.Action) (*protocol.Result, error)
	executeCtx func(ctx context.Context, action *protocol.Action) (*protocol.Result, error)
	initConfig func(map[string]any)
	initErr    error
}

func (p *replayTestProtocol) Name() string { return "replay-test" }
func (p *replayTestProtocol) Init(_ context.Context, config map[string]any) error {
	if p.initConfig != nil {
		p.initConfig(config)
	}
	return p.initErr
}
func (p *replayTestProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if p.executeCtx != nil {
		return p.executeCtx(ctx, action)
	}
	return p.execute(action)
}
func (p *replayTestProtocol) Teardown(_ context.Context) error    { return nil }
func (p *replayTestProtocol) HealthCheck(_ context.Context) error { return nil }

func registerReplayProtocol(t *testing.T, execute func(action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(name, func() protocol.Protocol {
		return &replayTestProtocol{execute: execute}
	})
	return name
}

func registerReplayProtocolWithContext(t *testing.T, execute func(ctx context.Context, action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(name, func() protocol.Protocol {
		return &replayTestProtocol{executeCtx: execute}
	})
	return name
}

func registerReplayProtocolWithInitError(t *testing.T, initErr error) string {
	t.Helper()
	name := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(name, func() protocol.Protocol {
		return &replayTestProtocol{initErr: initErr}
	})
	return name
}

func TestReplayAndShakeoutScrubProtocolInitializationErrors(t *testing.T) {
	secret := "short-secret"
	protocolName := registerReplayProtocolWithInitError(t, fmt.Errorf("dial https://user:%s@example.test failed", secret))
	script := &dsl.Script{Name: "credential-error", Protocol: protocolName, Actions: []dsl.Action{{Name: "noop", Type: "noop"}}}

	if _, err := ReplayScript(context.Background(), script, ReplayOptions{}); err == nil {
		t.Fatal("ReplayScript returned nil error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("ReplayScript leaked URL credential: %q", err)
	}
	if err := RunScriptShakeout(context.Background(), "shakeout", script); err == nil {
		t.Fatal("RunScriptShakeout returned nil error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("RunScriptShakeout leaked URL credential: %q", err)
	}

	load, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := load.Run(context.Background()); err == nil {
		t.Fatal("Engine.Run returned nil error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("Engine.Run leaked URL credential: %q", err)
	}

	multi, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{{Name: "g", Script: script, VUsers: 1}}})
	if err != nil {
		t.Fatalf("NewMultiGroup returned error: %v", err)
	}
	if _, err := multi.Run(context.Background()); err == nil {
		t.Fatal("MultiGroupEngine.Run returned nil error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("MultiGroupEngine.Run leaked URL credential: %q", err)
	}
}

type replayDebugControllerFunc func(context.Context, ReplayDebugAction) error

func (f replayDebugControllerFunc) BeforeAction(ctx context.Context, action ReplayDebugAction) error {
	return f(ctx, action)
}

type replayCorrelationObserver struct {
	evidence *protocol.CorrelationEvidence
	event    ReplayActionEvent
}

func (o *replayCorrelationObserver) BeforeAction(context.Context, ReplayDebugAction) error {
	return nil
}

func (o *replayCorrelationObserver) ObserveCorrelationEvidence(_ context.Context, _ ReplayDebugAction, event *ReplayActionEvent) {
	if event == nil || event.CorrelationEvidence == nil {
		return
	}
	o.event = *event
	o.evidence = &protocol.CorrelationEvidence{
		UnavailableReason: event.CorrelationEvidence.UnavailableReason,
		RequestURL:        event.CorrelationEvidence.RequestURL,
		RequestBody:       event.CorrelationEvidence.RequestBody,
		RequestHeaders:    cloneStringMap(event.CorrelationEvidence.RequestHeaders),
		ResponseBody:      event.CorrelationEvidence.ResponseBody,
		ResponseHeaders:   cloneStringMap(event.CorrelationEvidence.ResponseHeaders),
		ResponseStatus:    event.CorrelationEvidence.ResponseStatus,
	}
	if event.CorrelationEvidence.ResponseSuccess != nil {
		value := *event.CorrelationEvidence.ResponseSuccess
		o.evidence.ResponseSuccess = &value
	}
}

func TestReplayCorrelationEvidenceIsObservedBeforeScrubAndNeverPublished(t *testing.T) {
	const rawToken = "raw-csrf-token-1234567890"
	var executedResult *protocol.Result
	var retainedEvidence *protocol.CorrelationEvidence
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		retainedEvidence = &protocol.CorrelationEvidence{
			RequestURL:      "https://example.test/login",
			RequestBody:     "csrf=" + rawToken,
			RequestHeaders:  map[string]string{"X-CSRF-Token": rawToken},
			ResponseBody:    `{"csrf_token":"` + rawToken + `"}`,
			ResponseHeaders: map[string]string{"Set-Cookie": "csrf=" + rawToken},
			ResponseStatus:  200,
		}
		executedResult = &protocol.Result{
			ActionName:          action.Name,
			Success:             true,
			StatusCode:          200,
			ResponseBody:        `{"csrf_token":"fallback-` + rawToken + `"}`,
			CorrelationEvidence: retainedEvidence,
		}
		return executedResult, nil
	})
	observer := &replayCorrelationObserver{}
	var published []*ReplayActionEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "raw correlation evidence",
		Protocol: protocolName,
		Actions: []dsl.Action{httpSequenceTestAction("txn",
			dsl.Action{Name: "login", Type: "POST", Method: "POST", Target: "/login"},
		)},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		DebugController: observer,
		OnProgress: func(progress ReplayProgressEvent) {
			if progress.Event != nil {
				published = append(published, progress.Event)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observer.evidence == nil || !strings.Contains(observer.evidence.ResponseBody, rawToken) || observer.evidence.ResponseHeaders["Set-Cookie"] == "" {
		t.Fatalf("observer did not receive raw correlation evidence: %#v", observer.evidence)
	}
	if executedResult == nil || executedResult.CorrelationEvidence != nil {
		t.Fatalf("protocol result retained correlation evidence past result recording: %#v", executedResult)
	}
	if retainedEvidence == nil || !reflect.DeepEqual(*retainedEvidence, protocol.CorrelationEvidence{}) {
		t.Fatalf("adapter-retained raw evidence was not zeroed: %#v", retainedEvidence)
	}
	if observer.event.ParentActionName != "txn" || observer.event.RequestIndex == nil || *observer.event.RequestIndex != 0 || len(observer.event.StepPath) == 0 {
		t.Fatalf("observer ran before replay leaf identity was applied: %#v", observer.event)
	}
	if len(report.Events) != 1 || report.Events[0].CorrelationEvidence != nil {
		t.Fatalf("report retained raw correlation evidence: %#v", report.Events)
	}
	for _, event := range published {
		if event.CorrelationEvidence != nil {
			t.Fatalf("progress event retained raw correlation evidence: %#v", event)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), rawToken) {
		t.Fatalf("public replay report disclosed raw correlation token: %s", encoded)
	}
}

func TestReplayRetryZerosDiscardedCorrelationEvidenceAfterBackoff(t *testing.T) {
	const discardedSecret = "discarded-attempt-secret"
	var attempts int
	var discardedResult *protocol.Result
	var discardedEvidence *protocol.CorrelationEvidence
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		attempts++
		if attempts == 1 {
			discardedEvidence = &protocol.CorrelationEvidence{
				RequestURL:      "https://example.test/first",
				ResponseBody:    discardedSecret,
				ResponseHeaders: map[string]string{"X-Secret": discardedSecret},
			}
			discardedResult = &protocol.Result{
				ActionName:          action.Name,
				Success:             false,
				ErrorMessage:        "retry",
				CorrelationEvidence: discardedEvidence,
			}
			return discardedResult, nil
		}
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			CorrelationEvidence: &protocol.CorrelationEvidence{
				RequestURL:   "https://example.test/second",
				ResponseBody: "final-attempt",
			},
		}, nil
	})

	observer := &replayCorrelationObserver{}
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "retry evidence",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{
			MaxRetries:   1,
			RetryDelayMs: 1,
		}},
		Actions: []dsl.Action{{Name: "request", Type: "GET", Method: "GET", Target: "/"}},
	}, ReplayOptions{IgnoreThinkTime: true, DebugController: observer})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || !report.Success || attempts != 2 {
		t.Fatalf("report = %#v, attempts = %d", report, attempts)
	}
	if discardedResult == nil || discardedResult.CorrelationEvidence != nil {
		t.Fatalf("discarded result retained correlation evidence: %#v", discardedResult)
	}
	if discardedEvidence == nil || !reflect.DeepEqual(*discardedEvidence, protocol.CorrelationEvidence{}) {
		t.Fatalf("discarded raw evidence was not zeroed: %#v", discardedEvidence)
	}
	if observer.evidence == nil || observer.evidence.ResponseBody != "final-attempt" {
		t.Fatalf("observer evidence = %#v, want final attempt only", observer.evidence)
	}
}

func TestReplayZerosCorrelationEvidenceWhenProtocolReturnsResultAndError(t *testing.T) {
	const rawSecret = "result-plus-error-raw-secret"
	evidence := &protocol.CorrelationEvidence{
		RequestURL:   "https://example.test/error",
		ResponseBody: rawSecret,
	}
	retainedResult := &protocol.Result{
		ActionName:          "request",
		Success:             false,
		CorrelationEvidence: evidence,
	}
	protocolName := registerReplayProtocol(t, func(*protocol.Action) (*protocol.Result, error) {
		return retainedResult, errors.New("adapter failed after collecting a result")
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "result and error evidence cleanup",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/error"}},
	}, ReplayOptions{IgnoreThinkTime: true, DebugController: &replayCorrelationObserver{}})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil || report.Success {
		t.Fatalf("report = %#v, want failed replay report", report)
	}
	if retainedResult.CorrelationEvidence != nil {
		t.Fatalf("adapter-retained result kept raw evidence: %#v", retainedResult.CorrelationEvidence)
	}
	if !reflect.DeepEqual(*evidence, protocol.CorrelationEvidence{}) {
		t.Fatalf("adapter-retained evidence was not zeroed: %#v", evidence)
	}
}

func TestReplayGenericCorrelationEvidenceOversizeUsesUnavailableMarker(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:   action.Name,
			Success:      true,
			StatusCode:   200,
			ResponseBody: strings.Repeat("x", protocol.MaxCorrelationEvidenceBytes+1),
		}, nil
	})
	observer := &replayCorrelationObserver{}
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "oversized generic correlation evidence",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "oversized", Type: "request", Target: "/oversized"}},
	}, ReplayOptions{IgnoreThinkTime: true, DebugController: observer})
	if err != nil {
		t.Fatal(err)
	}
	if observer.evidence == nil || observer.evidence.UnavailableReason != protocol.CorrelationEvidenceTooLargeReason {
		t.Fatalf("observed oversized evidence = %#v, want explicit unavailable marker", observer.evidence)
	}
	if observer.evidence.RequestURL != "" || observer.evidence.ResponseBody != "" || len(observer.evidence.RequestHeaders) != 0 || len(observer.evidence.ResponseHeaders) != 0 {
		t.Fatalf("unavailable evidence retained partial raw data: %#v", observer.evidence)
	}
	if len(report.Events) != 1 || report.Events[0].CorrelationEvidence != nil {
		t.Fatalf("public report retained private marker: %#v", report.Events)
	}
}

func TestReplayEnablesCorrelationCaptureOnlyForPrivateObserver(t *testing.T) {
	var captureFlags []bool
	protocolName := "replay-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &replayTestProtocol{
			initConfig: func(config map[string]any) {
				value, _ := config[protocol.CorrelationEvidenceCaptureConfigKey].(bool)
				captureFlags = append(captureFlags, value)
			},
			execute: func(action *protocol.Action) (*protocol.Result, error) {
				return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
			},
		}
	})
	script := &dsl.Script{Name: "capture boundary", Protocol: protocolName, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}}
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, DebugController: &replayCorrelationObserver{}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(captureFlags, []bool{false, true}) {
		t.Fatalf("protocol correlation capture flags = %v, want [false true]", captureFlags)
	}
}

func TestReplayCorrelationEvidenceCapIncludesAuthoredProvenance(t *testing.T) {
	body := strings.Repeat("x", protocol.MaxCorrelationEvidenceBytes/2+1)
	action := &protocol.Action{Target: "/", Body: []byte(body)}
	evidence := replayCorrelationEvidence(action, &protocol.Result{Success: true}, dsl.Action{Target: "/", Body: body}, true)
	if evidence == nil || evidence.UnavailableReason != protocol.CorrelationEvidenceTooLargeReason {
		t.Fatalf("doubled wire/authored evidence = %#v, want unavailable marker", evidence)
	}
	if evidence.RequestBody != "" || evidence.AuthoredRequestBody != "" {
		t.Fatal("oversized evidence retained wire or authored body")
	}
}

func TestReplayDebugActionsAreScrubbedBeforeControllersAndProgressCallbacks(t *testing.T) {
	const secret = "inline-debug-target-secret"
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true}, nil
	})
	script := &dsl.Script{
		Name: "debug-action-redaction", Protocol: protocolName,
		Actions: []dsl.Action{{
			Name:   "request-" + secret,
			Type:   "request",
			Method: "GET",
			Target: "https://example.test/?api_key=" + secret,
		}},
	}
	var controllerAction ReplayDebugAction
	var progressAction *ReplayDebugAction
	_, err := ReplayScript(context.Background(), script, ReplayOptions{
		IgnoreThinkTime: true,
		RedactionRules:  []RedactionRule{{Pattern: secret}},
		DebugController: replayDebugControllerFunc(func(_ context.Context, action ReplayDebugAction) error {
			controllerAction = action
			return nil
		}),
		OnProgress: func(event ReplayProgressEvent) {
			if event.Type == ReplayProgressActionStarted {
				progressAction = event.Action
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for label, action := range map[string]*ReplayDebugAction{
		"controller": &controllerAction,
		"progress":   progressAction,
	} {
		if action == nil {
			t.Fatalf("%s action was not observed", label)
		}
		encoded, err := json.Marshal(action)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), secret) || !strings.Contains(string(encoded), redactedPayloadValue) {
			t.Fatalf("%s action was not disclosure-safe: %s", label, encoded)
		}
	}
}

func TestReplayScriptRunsAllPhasesAndInterpolatesExtractions(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "login":
			return &protocol.Result{
				ActionName:  action.Name,
				Success:     true,
				StatusCode:  200,
				Extractions: map[string]string{"token": "abc123"},
			}, nil
		case "profile":
			if action.Target != "/users/abc123" {
				return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "target not interpolated"}, nil
			}
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
		default:
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
		}
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Replay Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{},
		Init:     []dsl.Action{{Name: "setup", Type: "setup", Target: "/setup"}},
		Actions: []dsl.Action{
			{Name: "login", Type: "POST", Method: "POST", Target: "/login"},
			{Name: "profile", Type: "GET", Method: "GET", Target: "/users/{{var.token}}"},
		},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "cleanup", Target: "/cleanup"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if !report.Success {
		t.Fatalf("report.Success = false, error=%q", report.Error)
	}
	if len(report.Events) != 4 {
		t.Fatalf("event count = %d, want 4", len(report.Events))
	}
	if report.Events[2].Target != "/users/[REDACTED]" {
		t.Fatalf("profile target = %q, want /users/[REDACTED]", report.Events[2].Target)
	}
	if got := report.FinalVariables["token"]; got != redactedPayloadValue {
		t.Fatalf("final token = %q, want redacted", got)
	}
	if report.Events[1].Phase != "action" || report.Events[3].Phase != "teardown" {
		t.Fatalf("unexpected phases: %#v", report.Events)
	}
}

func TestReplayScriptInterpolatesParametersInEventsAndSnapshots(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:   action.Name,
			Success:      false,
			StatusCode:   404,
			ErrorMessage: "not found",
			Snapshot: &protocol.ErrorSnapshot{
				RequestMethod: action.Method,
				RequestURL:    action.Target,
			},
		}, nil
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Replay Parameter Script",
		Protocol: protocolName,
		Config: dsl.ScriptConfig{
			ErrorHandling: dsl.ErrorHandlingConfig{
				SnapshotOnError: true,
			},
		},
		Parameters: []dsl.Parameter{{
			Name:      "order_id",
			Type:      "list",
			Values:    []string{"ORDER-42"},
			Update:    "once",
			Selection: "sequential",
		}},
		Actions: []dsl.Action{{
			Name:   "get-order",
			Type:   "GET",
			Method: "GET",
			Target: "https://csi-gw-ext.att.com:443/orders/{{param.order_id}}",
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}

	wantURL := "https://csi-gw-ext.att.com:443/orders/ORDER-42"
	event := report.Events[0]
	if event.Target != wantURL {
		t.Fatalf("event target = %q, want %q", event.Target, wantURL)
	}
	if event.Snapshot == nil || event.Snapshot.RequestURL != wantURL {
		t.Fatalf("snapshot request url = %#v, want %q", event.Snapshot, wantURL)
	}
	if len(report.Snapshots) != 1 || report.Snapshots[0].RequestURL != wantURL {
		t.Fatalf("stored snapshots = %#v, want one snapshot for %q", report.Snapshots, wantURL)
	}
	if event.Snapshot != report.Snapshots[0] {
		t.Fatal("event and report retained duplicate copies of the same error snapshot")
	}
}

func TestReplayScriptPreservesSourceStepLabels(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Web2 Replay",
		Protocol: protocolName,
		Actions: []dsl.Action{{
			Name:   "Checkout_Click",
			Type:   "browser_click",
			Target: "#checkout",
			Metadata: map[string]any{
				"source_step_id":     "step-checkout",
				"source_step_index":  "7",
				"source_action":      "Click",
				"source_action_name": "Click Checkout",
				"source_transaction": "Checkout",
				"source_profile":     "web2_browser_profile",
			},
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}
	event := report.Events[0]
	if event.SourceStepID != "step-checkout" || event.SourceStepIndex != 7 || event.SourceTransaction != "Checkout" || event.SourceProfile != "web2_browser_profile" {
		t.Fatalf("event source labels = %#v, want Web2 source metadata", event)
	}
}

func TestReplayScriptStopsAfterFailureWhenContinueOnErrorDisabled(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "first":
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		default:
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
		}
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Failure Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false}},
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
		},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "cleanup", Target: "/cleanup"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if report.Success {
		t.Fatal("report.Success = true, want false")
	}
	if len(report.Events) != 2 {
		t.Fatalf("event count = %d, want 2 (failure + teardown)", len(report.Events))
	}
	if report.Events[0].ActionName != "first" || report.Events[1].Phase != "teardown" {
		t.Fatalf("unexpected events: %#v", report.Events)
	}
	if report.Error != "boom" {
		t.Fatalf("report.Error = %q, want boom", report.Error)
	}
}

func TestReplayProjectionScrubsShortSecretInputsAndProtocolErrors(t *testing.T) {
	t.Setenv("APEXLOAD_SECRET_REPLAY_SHORT", "x")
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Value != "x" {
			t.Fatalf("protocol value = %q, want resolved secret", action.Value)
		}
		return nil, errors.New(`unknown settlement_token "x"`)
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Secret Replay",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false}},
		Actions: []dsl.Action{{
			Name: "password", Type: "browser_type", Target: "/login", Value: "{{secret.replay_short}}",
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), `"value":"x"`) || strings.Contains(string(encoded), `token \"x\"`) {
		t.Fatalf("replay report leaked short secret: %s", encoded)
	}
	if report.Events[0].Value != redactedPayloadValue || !strings.Contains(report.Events[0].ErrorMessage, redactedPayloadValue) {
		t.Fatalf("replay event value/error = %q/%q", report.Events[0].Value, report.Events[0].ErrorMessage)
	}
}

func TestReplayScriptBoundsTeardownActions(t *testing.T) {
	t.Setenv("APEXLOAD_TEARDOWN_TIMEOUT", "25ms")

	protocolName := registerReplayProtocolWithContext(t, func(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "cleanup" {
			select {
			case <-ctx.Done():
				return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
			case <-time.After(2 * time.Second):
				return &protocol.Result{ActionName: action.Name, Success: false, ErrorMessage: "teardown was not cancelled"}, nil
			}
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	start := time.Now()
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Bounded Teardown Replay",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "main", Type: "GET", Target: "/main"}},
		Teardown: []dsl.Action{{Name: "cleanup", Type: "cleanup", Target: "/cleanup"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ReplayScript elapsed = %v, want bounded teardown", elapsed)
	}
	if len(report.Events) != 2 {
		t.Fatalf("event count = %d, want action plus teardown", len(report.Events))
	}
	if report.Events[1].Phase != "teardown" || !report.Events[1].Success {
		t.Fatalf("teardown event = %#v, want successful bounded teardown", report.Events[1])
	}
}

func TestReplayScriptRequiresScript(t *testing.T) {
	_, err := ReplayScript(context.Background(), nil, ReplayOptions{})
	if err == nil {
		t.Fatal("expected error for nil script")
	}
	if got := err.Error(); got != fmt.Sprintf("%s", "script is required") {
		t.Fatalf("error = %q, want script is required", got)
	}
}

func TestReplayScriptPublishesProgressEvents(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Progress Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{},
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
		},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		OnProgress: func(event ReplayProgressEvent) {
			progress = append(progress, event)
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}

	if len(progress) != 8 {
		t.Fatalf("progress count = %d, want 8", len(progress))
	}
	gotTypes := []string{progress[0].Type, progress[1].Type, progress[2].Type, progress[3].Type, progress[4].Type, progress[5].Type, progress[6].Type, progress[7].Type}
	wantTypes := []string{
		ReplayProgressStarted,
		ReplayProgressActionStarted,
		ReplayProgressActionCompleted,
		ReplayProgressEventRun,
		ReplayProgressActionStarted,
		ReplayProgressActionCompleted,
		ReplayProgressEventRun,
		ReplayProgressComplete,
	}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("progress types = %#v, want %#v", gotTypes, wantTypes)
	}
	if progress[1].Action == nil || progress[1].Action.ActionName != "first" {
		t.Fatalf("first action started = %#v, want action first", progress[1].Action)
	}
	if progress[3].Event == nil || progress[3].Event.ActionName != "first" {
		t.Fatalf("first event = %#v, want action first", progress[3].Event)
	}
	if progress[6].Event == nil || progress[6].Event.ActionName != "second" {
		t.Fatalf("second event = %#v, want action second", progress[6].Event)
	}
	if progress[7].Report == nil || len(progress[7].Report.Events) != len(report.Events) {
		t.Fatalf("completion report events = %d, want %d", len(progress[7].Report.Events), len(report.Events))
	}
	if progress[7].Report.CompletedAt.IsZero() {
		t.Fatal("completion report missing completed_at")
	}
	if progress[0].Report == nil || progress[0].Report.CompletedAt.IsZero() == false {
		t.Fatalf("start report completed_at = %v, want zero", progress[0].Report.CompletedAt)
	}
	if progress[3].EventIndex != 0 || progress[6].EventIndex != 1 {
		t.Fatalf("event indexes = %d,%d want 0,1", progress[3].EventIndex, progress[6].EventIndex)
	}
	if progress[1].EventID == "" || progress[1].EventID != progress[2].EventID || progress[2].EventID != progress[3].EventID {
		t.Fatalf("first action event IDs = %q/%q/%q, want stable lifecycle ID", progress[1].EventID, progress[2].EventID, progress[3].EventID)
	}
}

func TestReplayScriptClassifiesFailuresAndReportsArtifacts(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 403, ErrorMessage: "forbidden session expired"}, nil
	})

	var progress []ReplayProgressEvent
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Auth Failure Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false}},
		DataSources: []dsl.DataSource{{
			Name:       "users",
			Type:       "csv",
			InlineData: "id\n1\n",
			Strategy:   "sequential",
		}},
		Actions: []dsl.Action{{Name: "account", Type: "GET", Method: "GET", Target: "/account"}},
	}, ReplayOptions{
		ReplaySessionID: "diagnosis-session",
		Variables:       map[string]string{"token": "seeded"},
		IgnoreThinkTime: true,
		OnProgress: func(event ReplayProgressEvent) {
			progress = append(progress, event)
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if report.Success {
		t.Fatal("report.Success = true, want false")
	}
	if report.ReplaySessionID != "diagnosis-session" || report.ScriptHash == "" {
		t.Fatalf("report metadata = session %q hash %q, want populated", report.ReplaySessionID, report.ScriptHash)
	}
	if report.ArtifactManifest == nil || report.ArtifactManifest.ReplayID != "diagnosis-session" || !slices.Contains(report.ArtifactManifest.DataSources, "users") || !slices.Contains(report.ArtifactManifest.SeedVariables, "token") {
		t.Fatalf("artifact manifest = %#v, want replay/data/seed metadata", report.ArtifactManifest)
	}
	if report.DiagnosisSummary == nil || report.DiagnosisSummary.FailureCount != 1 || report.DiagnosisSummary.Categories["auth_session"] != 1 {
		t.Fatalf("diagnosis summary = %#v, want auth_session failure", report.DiagnosisSummary)
	}
	if len(report.RepairSuggestions) == 0 {
		t.Fatal("expected report repair suggestions")
	}
	event := report.Events[0]
	if event.EventID == "" || event.Diagnosis == nil || event.Diagnosis.Category != "auth_session" {
		t.Fatalf("event diagnosis = %#v id=%q, want auth_session with ID", event.Diagnosis, event.EventID)
	}
	if len(event.RepairSuggestions) == 0 {
		t.Fatal("expected event repair suggestions")
	}
	var failedLifecycle *ReplayProgressEvent
	for i := range progress {
		if progress[i].Type == ReplayProgressActionFailed {
			failedLifecycle = &progress[i]
			break
		}
	}
	if failedLifecycle == nil || failedLifecycle.Event == nil || failedLifecycle.Event.Diagnosis == nil {
		t.Fatalf("progress = %#v, want action_failed with diagnosis", progress)
	}
}

func TestReplayScriptCarriesNativeProtocolEvidence(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:       action.Name,
			Success:          true,
			StatusCode:       200,
			DependencyType:   "database",
			DependencyTarget: "sqlite",
			ResponseBody:     `{"row_count":1,"rows":[{"id":1}],"scalar":1}`,
			BytesSent:        18,
			BytesReceived:    44,
			Extractions:      map[string]string{"id": "1"},
			ProtocolEvidence: protocol.BuildProtocolEvidence(action, &protocol.Result{Success: true, StatusCode: 200, DependencyType: "database", DependencyTarget: "sqlite", ResponseBody: `{"row_count":1,"rows":[{"id":1}],"scalar":1}`, BytesSent: 18, BytesReceived: 44, Extractions: map[string]string{"id": "1"}}),
		}, nil
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Database Replay",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "query", Type: "db_query", Target: "select id from users"}},
	}, ReplayOptions{ReplaySessionID: "native-evidence", IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}
	event := report.Events[0]
	if event.ProtocolEvidence["protocol"] != "database" {
		t.Fatalf("event protocol evidence = %#v, want database", event.ProtocolEvidence)
	}
	if rows, ok := event.ProtocolEvidence["rows"].(map[string]any); !ok || rows["row_count"] == nil {
		t.Fatalf("event protocol rows = %#v, want row_count", event.ProtocolEvidence["rows"])
	}
	if report.ArtifactManifest == nil || report.ArtifactManifest.ProtocolArtifacts["database"] != 1 {
		t.Fatalf("artifact manifest = %#v, want database protocol artifact count", report.ArtifactManifest)
	}
}

func TestReplayScriptClassifiesNativeProtocolFailures(t *testing.T) {
	cases := []struct {
		name       string
		event      ReplayActionEvent
		want       string
		wantSignal string
	}{
		{name: "websocket handshake", event: ReplayActionEvent{ActionType: "ws_connect", ErrorMessage: "bad status during websocket handshake"}, want: "websocket_handshake", wantSignal: "websocket_handshake"},
		{name: "grpc contract", event: ReplayActionEvent{ActionType: "grpc_unary", ErrorMessage: "reflection descriptor for service not found"}, want: "grpc_contract", wantSignal: "grpc_contract"},
		{name: "database guard", event: ReplayActionEvent{ActionType: "db_exec", ErrorMessage: "database read_only mode blocks mutating SQL"}, want: "database_guard", wantSignal: "database_guard"},
		{name: "network dns", event: ReplayActionEvent{ActionType: "dns_query", ErrorMessage: "lookup missing.example: no such host"}, want: "network_dns_resolution", wantSignal: "dns"},
		{name: "jms broker", event: ReplayActionEvent{ActionType: "jms_consume", ErrorMessage: "message received timeout for destination orders.in"}, want: "jms_broker_message", wantSignal: "jms"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diagnosis := classifyReplayFailure(tc.event)
			if diagnosis.Category != tc.want {
				t.Fatalf("category = %q, want %q", diagnosis.Category, tc.want)
			}
			if !slices.Contains(diagnosis.Signals, tc.wantSignal) {
				t.Fatalf("signals = %#v, want %q", diagnosis.Signals, tc.wantSignal)
			}
			if diagnosis.Evidence == "" || diagnosis.Action == "" {
				t.Fatalf("diagnosis = %#v, want evidence/action", diagnosis)
			}
		})
	}
}

func TestReplayScriptDebugControllerRunsBeforeAction(t *testing.T) {
	var executed []string
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed = append(executed, action.Name)
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	stopErr := errors.New("debug stop before second")
	var checkpoints []ReplayDebugAction
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Debug Script",
		Protocol: protocolName,
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
		},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		DebugController: replayDebugControllerFunc(func(_ context.Context, action ReplayDebugAction) error {
			checkpoints = append(checkpoints, action)
			if action.ActionName == "second" {
				if slices.Contains(executed, "second") {
					t.Fatal("second action executed before debug checkpoint returned")
				}
				return stopErr
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if report.Success {
		t.Fatal("report.Success = true, want false after debug stop")
	}
	if report.Error != stopErr.Error() {
		t.Fatalf("report.Error = %q, want %q", report.Error, stopErr.Error())
	}
	if !slices.Equal(executed, []string{"first"}) {
		t.Fatalf("executed actions = %#v, want first only", executed)
	}
	if len(checkpoints) != 2 || checkpoints[1].Phase != "action" || checkpoints[1].ActionIndex != 1 || checkpoints[1].EventIndex != 1 {
		t.Fatalf("debug checkpoints = %#v, want second action checkpoint before event 1", checkpoints)
	}
}

func TestReplayScriptCarriesBrowserArtifacts(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:       action.Name,
			Success:          true,
			PageURL:          "https://example.test/page",
			PageTitle:        "Example",
			HTML:             "<html><body>ok</body></html>",
			ScreenshotBase64: "ZmFrZS1pbWFnZQ==",
			ConsoleEntries:   []protocol.ConsoleEntry{{Level: "log", Text: "ready"}},
			NetworkEntries:   []protocol.NetworkEntry{{URL: "https://example.test/page", Method: "GET", StatusCode: 200}},
			DOMSummary:       &protocol.DOMSummary{Title: "Example", ReadyState: "complete", VisibleText: []string{"ok"}},
			ClientMetrics:    &protocol.ClientMetrics{LCPMs: 240, INPMs: 55, CLSScore: 0.03, BrowserEngine: "chromium", DeviceProfile: "desktop_1080p"},
		}, nil
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Browser Replay",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{},
		Actions:  []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/page", Selector: "body", Value: "ignored"}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}

	event := report.Events[0]
	if event.PageURL != "https://example.test/page" || event.PageTitle != "Example" {
		t.Fatalf("event page fields = %#v", event)
	}
	if event.Screenshot != "ZmFrZS1pbWFnZQ==" {
		t.Fatalf("screenshot = %q, want ZmFrZS1pbWFnZQ==", event.Screenshot)
	}
	if event.HTML != "<html><body>ok</body></html>" {
		t.Fatalf("html = %q", event.HTML)
	}
	if len(event.Console) != 1 || len(event.Network) != 1 {
		t.Fatalf("console/network entries = %d/%d, want 1/1", len(event.Console), len(event.Network))
	}
	if event.DOMSummary == nil || event.DOMSummary.Title != "Example" {
		t.Fatalf("dom summary = %#v", event.DOMSummary)
	}
	if event.ClientMetrics == nil || event.ClientMetrics.LCPMs != 240 || event.ClientMetrics.BrowserEngine != "chromium" {
		t.Fatalf("client metrics = %#v, want browser vitals", event.ClientMetrics)
	}
	if event.Selector != "body" || event.Value != "ignored" {
		t.Fatalf("selector/value = %q/%q, want body/ignored", event.Selector, event.Value)
	}
}

func TestReplaySensitiveSeedValuesAreRedactedFromEventsAndScreenshots(t *testing.T) {
	const seedSecret = "tiny-seed-value"
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:       action.Name,
			Success:          true,
			PageURL:          "https://example.test/" + seedSecret,
			ScreenshotBase64: "opaque-image",
			Snapshot:         &protocol.ErrorSnapshot{ScreenshotBase64: "opaque-snapshot-image"},
		}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Sensitive seed replay",
		Protocol: protocolName,
		Actions:  []dsl.Action{{Name: "seed", Type: "request", Target: "https://example.test/{{var.password}}"}},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		Variables:       map[string]string{"password": seedSecret},
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(report.Events))
	}
	event := report.Events[0]
	serialized, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), seedSecret) || event.Screenshot != "" || event.Snapshot != nil && event.Snapshot.ScreenshotBase64 != "" {
		t.Fatalf("replay exposed sensitive seed evidence: %s", serialized)
	}
}

func TestReplayScriptAppliesCustomRedactionRulesToEventsAndSnapshots(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:   action.Name,
			Success:      true,
			StatusCode:   200,
			ResponseBody: `{"customer":"customer-1234"}`,
			Snapshot: &protocol.ErrorSnapshot{
				RequestURL:      "https://example.test/customers/customer-1234",
				RequestHeaders:  map[string]string{"X-Customer": "customer-1234"},
				RequestBody:     "customer=customer-1234",
				ResponseHeaders: map[string]string{"X-Customer": "customer-1234"},
				ResponseBody:    `{"customer":"customer-1234"}`,
			},
		}, nil
	})

	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Replay Redaction Script",
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{},
		Actions: []dsl.Action{{
			Name:    "lookup",
			Type:    "GET",
			Target:  "/customers/customer-1234",
			Headers: map[string]string{"X-Customer": "customer-1234"},
			Body:    "customer=customer-1234",
		}},
	}, ReplayOptions{IgnoreThinkTime: true, RedactionRules: []RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}}})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if len(report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(report.Events))
	}
	event := report.Events[0]
	if event.Target != "/customers/[REDACTED]" {
		t.Fatalf("target = %q, want /customers/[REDACTED]", event.Target)
	}
	if event.Headers["X-Customer"] != "[REDACTED]" {
		t.Fatalf("header X-Customer = %q, want [REDACTED]", event.Headers["X-Customer"])
	}
	if event.Body != "customer=[REDACTED]" {
		t.Fatalf("body = %q, want customer=[REDACTED]", event.Body)
	}
	if event.Snapshot == nil || event.Snapshot.RequestURL != "https://example.test/customers/[REDACTED]" {
		t.Fatalf("snapshot request url = %#v, want redacted customer id", event.Snapshot)
	}
	if event.Snapshot.ResponseBody != `{"customer":"[REDACTED]"}` {
		t.Fatalf("snapshot response body = %q, want redacted response body", event.Snapshot.ResponseBody)
	}
}

func httpSequenceTestAction(name string, children ...dsl.Action) dsl.Action {
	return dsl.Action{
		Name:   name,
		Type:   "http_sequence",
		Method: "SEQUENCE",
		Target: "https://example.test",
		Params: map[string]any{"requests": children},
	}
}

func TestReplayScriptExpandsHTTPSequenceChildren(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "c_login":
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200, Extractions: map[string]string{"sid": "S9"}}, nil
		case "c_use":
			if action.Target != "/use/S9" {
				return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "not interpolated: " + action.Target}, nil
			}
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
		default:
			return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 204}, nil
		}
	})

	seq := httpSequenceTestAction("txn",
		dsl.Action{Name: "c_login", Type: "POST", Method: "POST", Target: "/login"},
		dsl.Action{Name: "c_use", Type: "GET", Method: "GET", Target: "/use/{{var.sid}}"},
		dsl.Action{Name: "c_done", Type: "GET", Method: "GET", Target: "/done"},
	)
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Seq", Protocol: protocolName, Actions: []dsl.Action{seq},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if !report.Success {
		t.Fatalf("report.Success=false err=%q", report.Error)
	}
	if len(report.Events) != 3 {
		t.Fatalf("events=%d want 3", len(report.Events))
	}
	for i, ev := range report.Events {
		if ev.RequestIndex == nil || *ev.RequestIndex != i {
			t.Fatalf("event %d RequestIndex=%v want %d", i, ev.RequestIndex, i)
		}
		if ev.ParentActionName != "txn" {
			t.Fatalf("event %d ParentActionName=%q want txn", i, ev.ParentActionName)
		}
		if ev.RequestCount != 3 {
			t.Fatalf("event %d RequestCount=%d want 3", i, ev.RequestCount)
		}
		if ev.ActionIndex != 0 {
			t.Fatalf("event %d ActionIndex=%d want 0 (top-level container)", i, ev.ActionIndex)
		}
		if ev.EventIndex != i {
			t.Fatalf("event %d EventIndex=%d want %d (flat)", i, ev.EventIndex, i)
		}
		if len(ev.StepPath) != 1 || ev.StepPath[0] != fmt.Sprintf("requests[%d]", i) {
			t.Fatalf("event %d StepPath=%v want [requests[%d]]", i, ev.StepPath, i)
		}
	}
	if !report.Events[1].Success {
		t.Fatalf("c_use failed (correlation not threaded): %q", report.Events[1].ErrorMessage)
	}
	if report.Events[1].Target != "/use/[REDACTED]" {
		t.Fatalf("c_use target=%q want /use/[REDACTED] (child->child correlation)", report.Events[1].Target)
	}
	if report.Events[0].EventID == report.Events[1].EventID {
		t.Fatalf("child event IDs not distinct: %q", report.Events[0].EventID)
	}
}

func TestReplayHTTPSequenceStopsAtFailingChildWhenStopOnError(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "c2" {
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	seq := httpSequenceTestAction("txn",
		dsl.Action{Name: "c1", Type: "GET", Method: "GET", Target: "/1"},
		dsl.Action{Name: "c2", Type: "GET", Method: "GET", Target: "/2"},
		dsl.Action{Name: "c3", Type: "GET", Method: "GET", Target: "/3"},
	)
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Seq", Protocol: protocolName,
		Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false}},
		Actions: []dsl.Action{seq},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if report.Success {
		t.Fatalf("report.Success=true want false")
	}
	if len(report.Events) != 2 {
		t.Fatalf("events=%d want 2 (stop at failing child)", len(report.Events))
	}
	if report.Events[1].ActionName != "c2" || report.Events[1].Success {
		t.Fatalf("event[1]=%+v want failed c2", report.Events[1])
	}
}

func TestReplayHTTPSequenceContinuesPastFailingChildWhenContinueOnError(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Name == "c2" {
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	seq := httpSequenceTestAction("txn",
		dsl.Action{Name: "c1", Type: "GET", Method: "GET", Target: "/1"},
		dsl.Action{Name: "c2", Type: "GET", Method: "GET", Target: "/2"},
		dsl.Action{Name: "c3", Type: "GET", Method: "GET", Target: "/3"},
	)
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Seq", Protocol: protocolName,
		Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: true}},
		Actions: []dsl.Action{seq},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if report.Success {
		t.Fatalf("report.Success=true want false (a child failed)")
	}
	if len(report.Events) != 3 {
		t.Fatalf("events=%d want 3 (continue past failing child)", len(report.Events))
	}
}

func TestReplayHTTPSequenceFiresBeforeActionPerChild(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	seq := httpSequenceTestAction("txn",
		dsl.Action{Name: "c1", Type: "GET", Method: "GET", Target: "/1"},
		dsl.Action{Name: "c2", Type: "GET", Method: "GET", Target: "/2"},
	)
	var seen []ReplayDebugAction
	_, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Seq", Protocol: protocolName, Actions: []dsl.Action{seq},
	}, ReplayOptions{IgnoreThinkTime: true, DebugController: replayDebugControllerFunc(func(_ context.Context, a ReplayDebugAction) error {
		seen = append(seen, a)
		return nil
	})})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("BeforeAction calls=%d want 2 (one per child)", len(seen))
	}
	for i, a := range seen {
		if a.RequestIndex == nil || *a.RequestIndex != i {
			t.Fatalf("BeforeAction[%d] RequestIndex=%v want %d", i, a.RequestIndex, i)
		}
		if a.ParentActionName != "txn" {
			t.Fatalf("BeforeAction[%d] ParentActionName=%q want txn", i, a.ParentActionName)
		}
	}
}

func TestReplayControlIfRunsTakenBranch(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	makeIf := func(cond *dsl.Condition) dsl.Action {
		return dsl.Action{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{
			Condition: cond,
			Steps:     []dsl.Action{{Name: "then1", Type: "GET", Method: "GET", Target: "/then"}},
			Else:      []dsl.Action{{Name: "else1", Type: "GET", Method: "GET", Target: "/else"}},
		}}
	}

	// True condition -> then branch.
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "If", Protocol: protocolName,
		Actions: []dsl.Action{makeIf(&dsl.Condition{Op: "eq", Left: "a", Right: "a"})},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if len(report.Events) != 1 || report.Events[0].ActionName != "then1" {
		t.Fatalf("events=%#v want only then1", report.Events)
	}
	if len(report.Events[0].StepPath) != 1 || report.Events[0].StepPath[0] != "then[0]" {
		t.Fatalf("then StepPath=%v want [then[0]]", report.Events[0].StepPath)
	}

	// False condition -> else branch.
	report, err = ReplayScript(context.Background(), &dsl.Script{
		Name: "If", Protocol: protocolName,
		Actions: []dsl.Action{makeIf(&dsl.Condition{Op: "eq", Left: "a", Right: "b"})},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if len(report.Events) != 1 || report.Events[0].ActionName != "else1" {
		t.Fatalf("events=%#v want only else1", report.Events)
	}
	if len(report.Events[0].StepPath) != 1 || report.Events[0].StepPath[0] != "else[0]" {
		t.Fatalf("else StepPath=%v want [else[0]]", report.Events[0].StepPath)
	}
}

func TestReplayControlLoopRunsBodyPerPass(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	loop := dsl.Action{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{
		Count: 3,
		Steps: []dsl.Action{{Name: "body", Type: "GET", Method: "GET", Target: "/b"}},
	}}
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Loop", Protocol: protocolName, Actions: []dsl.Action{loop},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if len(report.Events) != 3 {
		t.Fatalf("events=%d want 3 (body per pass)", len(report.Events))
	}
	for i, ev := range report.Events {
		if ev.Iteration == nil || *ev.Iteration != i+1 {
			t.Fatalf("event %d Iteration=%v want %d", i, ev.Iteration, i+1)
		}
		if len(ev.StepPath) != 1 || ev.StepPath[0] != "loop[0]" {
			t.Fatalf("event %d StepPath=%v want [loop[0]] (static, iteration-independent)", i, ev.StepPath)
		}
	}
}

func TestReplayNestedControlLoopIterationPath(t *testing.T) {
	protocolName := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	inner := dsl.Action{Name: "inner", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{
		{Name: "body", Type: "GET", Method: "GET", Target: "/b"},
	}}}
	outer := dsl.Action{Name: "outer", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{inner}}}
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name: "Nested", Protocol: protocolName, Actions: []dsl.Action{outer},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript error: %v", err)
	}
	if len(report.Events) != 4 {
		t.Fatalf("events=%d want 4 (outer 2 x inner 2)", len(report.Events))
	}
	wantPaths := [][]int{{1, 1}, {1, 2}, {2, 1}, {2, 2}}
	for i, ev := range report.Events {
		if !slices.Equal(ev.IterationPath, wantPaths[i]) {
			t.Fatalf("event %d IterationPath=%v want %v", i, ev.IterationPath, wantPaths[i])
		}
		if len(ev.StepPath) != 2 || ev.StepPath[0] != "loop[0]" || ev.StepPath[1] != "loop[0]" {
			t.Fatalf("event %d StepPath=%v want [loop[0] loop[0]]", i, ev.StepPath)
		}
		if ev.Iteration == nil || *ev.Iteration != wantPaths[i][1] {
			t.Fatalf("event %d Iteration=%v want innermost %d", i, ev.Iteration, wantPaths[i][1])
		}
	}
}
