package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type replayTestProtocol struct {
	execute func(action *protocol.Action) (*protocol.Result, error)
}

func (p *replayTestProtocol) Name() string                                   { return "replay-test" }
func (p *replayTestProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *replayTestProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
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

type replayDebugControllerFunc func(context.Context, ReplayDebugAction) error

func (f replayDebugControllerFunc) BeforeAction(ctx context.Context, action ReplayDebugAction) error {
	return f(ctx, action)
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
	if report.Events[2].Target != "/users/abc123" {
		t.Fatalf("profile target = %q, want /users/abc123", report.Events[2].Target)
	}
	if got := report.FinalVariables["token"]; got != "abc123" {
		t.Fatalf("final token = %q, want abc123", got)
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
	if report.Events[1].Target != "/use/S9" {
		t.Fatalf("c_use target=%q want /use/S9 (child->child correlation)", report.Events[1].Target)
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
