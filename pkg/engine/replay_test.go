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
				"source_profile":     "vugen_web2",
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
	if event.SourceStepID != "step-checkout" || event.SourceStepIndex != 7 || event.SourceTransaction != "Checkout" || event.SourceProfile != "vugen_web2" {
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

	if len(progress) != 4 {
		t.Fatalf("progress count = %d, want 4", len(progress))
	}
	gotTypes := []string{progress[0].Type, progress[1].Type, progress[2].Type, progress[3].Type}
	wantTypes := []string{ReplayProgressStarted, ReplayProgressEventRun, ReplayProgressEventRun, ReplayProgressComplete}
	if !slices.Equal(gotTypes, wantTypes) {
		t.Fatalf("progress types = %#v, want %#v", gotTypes, wantTypes)
	}
	if progress[1].Event == nil || progress[1].Event.ActionName != "first" {
		t.Fatalf("first event = %#v, want action first", progress[1].Event)
	}
	if progress[2].Event == nil || progress[2].Event.ActionName != "second" {
		t.Fatalf("second event = %#v, want action second", progress[2].Event)
	}
	if progress[3].Report == nil || len(progress[3].Report.Events) != len(report.Events) {
		t.Fatalf("completion report events = %d, want %d", len(progress[3].Report.Events), len(report.Events))
	}
	if progress[3].Report.CompletedAt.IsZero() {
		t.Fatal("completion report missing completed_at")
	}
	if progress[0].Report == nil || progress[0].Report.CompletedAt.IsZero() == false {
		t.Fatalf("start report completed_at = %v, want zero", progress[0].Report.CompletedAt)
	}
	if progress[1].EventIndex != 0 || progress[2].EventIndex != 1 {
		t.Fatalf("event indexes = %d,%d want 0,1", progress[1].EventIndex, progress[2].EventIndex)
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
