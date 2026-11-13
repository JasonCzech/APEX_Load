package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// Name/shape scrubbing of SCRIPT-AUTHORED request content follows
// config.log.redact_run_evidence (USER DECISION 2026-11-12): off keeps the values
// the script wrote, on masks them as before. Exact platform-managed values are
// masked in both modes, and server RESPONSE content keeps the full scrubber.
const (
	authoredLiteralPassword = "litUserPw-5521"
	authoredLiteralSecret   = "litClientSecret-5522"
	authoredLiteralToken    = "litBearerTok-5523"
	authoredPlatformSecret  = "platformManagedVal-5524"
	responseLiteralSecret   = "respClientSecret-5525"
	pageTrafficToken        = "pageTrafficTok-5526"
)

func authoredEvidenceRootConfig(t *testing.T, redact bool) *RedactionConfig {
	t.Helper()
	config, err := WithSecretValueRedactionChecked(nil, []string{authoredPlatformSecret})
	if err != nil {
		t.Fatal(err)
	}
	return withAuthoredEvidenceScrubbing(ensureDynamicSecretRegistry(config), redact)
}

func authoredEvidenceResult() *protocol.Result {
	requestBody := fmt.Sprintf(`{"userPassword":%q,"client_secret":%q,"token":%q,"platform":%q}`,
		authoredLiteralPassword, authoredLiteralSecret, authoredLiteralToken, authoredPlatformSecret)
	return &protocol.Result{
		ActionName:   "Login",
		ResponseBody: fmt.Sprintf(`{"client_secret":%q}`, responseLiteralSecret),
		Snapshot: &protocol.ErrorSnapshot{
			RequestMethod: "POST",
			RequestURL:    "https://api.example.test/login?client_secret=" + authoredLiteralSecret + "&password=" + authoredLiteralPassword,
			RequestHeaders: map[string]string{
				"Authorization": "Bearer " + authoredLiteralToken,
				"X-Api-Key":     "key " + authoredPlatformSecret,
				"X-Note":        "userPassword=" + authoredLiteralPassword,
			},
			RequestBody:     requestBody,
			ResponseStatus:  500,
			ResponseHeaders: map[string]string{"Set-Cookie": "sid=" + responseLiteralSecret},
			ResponseBody:    fmt.Sprintf(`{"client_secret":%q}`, responseLiteralSecret),
		},
		NetworkEntries: []protocol.NetworkEntry{
			{
				ScriptRequest:  true,
				URL:            "https://api.example.test/login?client_secret=" + authoredLiteralSecret,
				Method:         "POST",
				RequestHeaders: map[string]string{"Authorization": "Bearer " + authoredLiteralToken},
				RequestBody:    requestBody,
				HAR: &protocol.ReplayHARExchange{
					RequestBody:  &protocol.ReplayHARBody{State: "retained", Text: requestBody},
					ResponseBody: &protocol.ReplayHARBody{State: "retained", Text: fmt.Sprintf(`{"client_secret":%q}`, responseLiteralSecret)},
				},
			},
			{
				// Page/redirect traffic is not what the script authored.
				URL:            "https://cdn.example.test/app.js?access_token=" + pageTrafficToken,
				Method:         "GET",
				RequestHeaders: map[string]string{"Authorization": "Bearer " + pageTrafficToken},
			},
		},
	}
}

func TestAuthoredRequestNameScrubFollowsRunEvidenceSetting(t *testing.T) {
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			config := authoredEvidenceRootConfig(t, mode.redact)
			result := authoredEvidenceResult()
			scrubSensitiveResultWithOptions(result, config, true)
			snapshot := result.Snapshot
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			// Platform-managed: masked in both modes, and a sensitively named
			// header carrying it is withheld whole.
			if strings.Contains(string(encoded), authoredPlatformSecret) {
				t.Fatalf("platform-managed value leaked: %s", encoded)
			}
			if got := snapshot.RequestHeaders["X-Api-Key"]; got != redactedPayloadValue {
				t.Fatalf("X-Api-Key = %q, want the whole header masked", got)
			}
			// Response side and page traffic: unchanged full scrub in both modes.
			for _, value := range []string{responseLiteralSecret, pageTrafficToken} {
				if strings.Contains(string(encoded), value) {
					t.Fatalf("response/page value %q leaked: %s", value, encoded)
				}
			}
			if result.NetworkEntries[1].AuthoredRequestRetained {
				t.Fatal("page traffic entry marked authored")
			}
			// Authored request side: visible exactly when the setting is off.
			surfaces := map[string]string{
				"snapshot url":         snapshot.RequestURL,
				"snapshot auth header": snapshot.RequestHeaders["Authorization"],
				"snapshot note header": snapshot.RequestHeaders["X-Note"],
				"snapshot body":        snapshot.RequestBody,
				"entry url":            result.NetworkEntries[0].URL,
				"entry auth header":    result.NetworkEntries[0].RequestHeaders["Authorization"],
				"entry body":           result.NetworkEntries[0].RequestBody,
				"entry har body":       result.NetworkEntries[0].HAR.RequestBody.Text,
			}
			wants := map[string]string{
				"snapshot url":         authoredLiteralSecret,
				"snapshot auth header": authoredLiteralToken,
				"snapshot note header": authoredLiteralPassword,
				"snapshot body":        authoredLiteralPassword,
				"entry url":            authoredLiteralSecret,
				"entry auth header":    authoredLiteralToken,
				"entry body":           authoredLiteralSecret,
				"entry har body":       authoredLiteralToken,
			}
			for label, got := range surfaces {
				visible := strings.Contains(got, wants[label])
				if visible == mode.redact {
					t.Fatalf("%s = %q: authored %q visible=%v, want visible=%v", label, got, wants[label], visible, !mode.redact)
				}
			}
			if snapshot.AuthoredRequestRetained != !mode.redact || result.NetworkEntries[0].AuthoredRequestRetained != !mode.redact {
				t.Fatalf("authored markers snapshot=%v entry=%v, want %v", snapshot.AuthoredRequestRetained, result.NetworkEntries[0].AuthoredRequestRetained, !mode.redact)
			}
		})
	}
}

// Every derived layer a VUser publishes through (per-VUser overlays, concurrent
// joins, policy clones, the runtime matcher attach) keeps the script's decision.
func TestAuthoredEvidenceDecisionSurvivesDerivedRedactionLayers(t *testing.T) {
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			root := authoredEvidenceRootConfig(t, mode.redact)
			overlay, err := withRuntimeSecretValueOverlay(root, []string{"overlay-platform-5527"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			matcher, err := WithSecretValueRedactionChecked(nil, []string{"joined-5528"})
			if err != nil {
				t.Fatal(err)
			}
			joined, err := attachConcurrentRuntimeMatcher(overlay, matcher)
			if err != nil {
				t.Fatal(err)
			}
			rules, err := NewRedactionConfig([]RedactionRule{{Name: "r", Pattern: "never-matches-5529"}})
			if err != nil {
				t.Fatal(err)
			}
			attached := attachRuntimeSecretRedaction(withAuthoredEvidenceScrubbing(rules, mode.redact), matcher)
			bare := attachRuntimeSecretRedaction(withAuthoredEvidenceScrubbing(&RedactionConfig{}, mode.redact), matcher)
			for label, config := range map[string]*RedactionConfig{
				"root": root, "overlay": overlay, "joined": joined, "clone": cloneRedactionPolicy(overlay),
				"attached": attached, "attached_bare": bare, "dynamic": ensureDynamicSecretRegistry(overlay),
			} {
				if got := config.scrubsAuthoredEvidence(); got != mode.redact {
					t.Fatalf("%s scrubsAuthoredEvidence = %v, want %v", label, got, mode.redact)
				}
				body := `{"password":"` + authoredLiteralPassword + `","k":"overlay-platform-5527"}`
				got := scrubAuthoredBodyTextWithConfig(body, config)
				if strings.Contains(got, authoredLiteralPassword) == mode.redact {
					t.Fatalf("%s authored body = %q", label, got)
				}
				carriesOverlay := label == "overlay" || label == "joined" || label == "dynamic"
				if carriesOverlay && strings.Contains(got, "overlay-platform-5527") {
					t.Fatalf("%s leaked an overlay platform value: %q", label, got)
				}
			}
			if (*RedactionConfig)(nil).scrubsAuthoredEvidence() != true {
				t.Fatal("a nil config must keep the historical full scrub")
			}
			if !unavailableRuntimePublicationConfig.scrubsAuthoredEvidence() || withAuthoredEvidenceScrubbing(unavailableRuntimePublicationConfig, false) != unavailableRuntimePublicationConfig {
				t.Fatal("the publication-unavailable sentinel must not be relaxed")
			}
		})
	}
}

// Project redaction rules are operator policy, not built-in name scrubbing:
// they apply to authored request content in both modes.
func TestAuthoredRequestKeepsProjectRedactionRules(t *testing.T) {
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			rules, err := NewRedactionConfig([]RedactionRule{{Name: "acct", Pattern: `ACCT-\d+`}})
			if err != nil {
				t.Fatal(err)
			}
			config := withAuthoredEvidenceScrubbing(rules, mode.redact)
			got := scrubAuthoredTextWithConfig("account=ACCT-7781 password="+authoredLiteralPassword, config)
			if strings.Contains(got, "ACCT-7781") {
				t.Fatalf("project rule not applied: %q", got)
			}
			if strings.Contains(got, authoredLiteralPassword) == mode.redact {
				t.Fatalf("authored password visibility wrong: %q", got)
			}
		})
	}
}

func TestReplayActionEventAuthoredFieldsFollowRunEvidenceSetting(t *testing.T) {
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			config := authoredEvidenceRootConfig(t, mode.redact)
			action := &protocol.Action{
				Target:  "https://api.example.test/login?client_secret=" + authoredLiteralSecret,
				Value:   "password=" + authoredLiteralPassword,
				Headers: map[string]string{"Authorization": "Bearer " + authoredLiteralToken, "X-Api-Key": authoredPlatformSecret},
				Body:    []byte(`{"client_secret":"` + authoredLiteralSecret + `"}`),
			}
			event := &ReplayActionEvent{
				Target:  scrubReplayTarget(action, config),
				Value:   scrubReplayValue(action, config),
				Headers: scrubReplayHeaders(action, config),
				Body:    scrubReplayBody(action, config),
				HTML:    `<input name="password" value="` + responseLiteralSecret + `">`,
			}
			scrubReplayActionEvent(event, config)
			for label, pair := range map[string][2]string{
				"target":  {event.Target, authoredLiteralSecret},
				"value":   {event.Value, authoredLiteralPassword},
				"headers": {event.Headers["Authorization"], authoredLiteralToken},
				"body":    {event.Body, authoredLiteralSecret},
			} {
				if strings.Contains(pair[0], pair[1]) == mode.redact {
					t.Fatalf("%s = %q, authored visible want %v", label, pair[0], !mode.redact)
				}
			}
			if event.Headers["X-Api-Key"] != redactedPayloadValue {
				t.Fatalf("platform-managed header = %q", event.Headers["X-Api-Key"])
			}
			if strings.Contains(event.HTML, responseLiteralSecret) {
				t.Fatalf("response HTML lost its scrub: %q", event.HTML)
			}
			debug := &ReplayDebugAction{Target: action.Target}
			scrubReplayDebugAction(debug, config)
			if strings.Contains(debug.Target, authoredLiteralSecret) == mode.redact {
				t.Fatalf("debug action target = %q", debug.Target)
			}
		})
	}
}

func TestProtocolConfigCarriesAuthoredEvidenceDecision(t *testing.T) {
	for _, mode := range runEvidenceRedactionModes {
		script := setRunEvidenceRedaction(&dsl.Script{Name: "cfg", Protocol: "http"}, mode.redact)
		config := buildProtoConfigForRunMode(script, "load")
		if value, ok := config[protocol.RedactAuthoredEvidenceConfigKey].(bool); !ok || value != mode.redact {
			t.Fatalf("%s: config key = %v (%v)", mode.name, config[protocol.RedactAuthoredEvidenceConfigKey], ok)
		}
		if protocol.RedactsAuthoredEvidence(config) != mode.redact {
			t.Fatalf("%s: RedactsAuthoredEvidence disagrees", mode.name)
		}
	}
	if config := buildProtoConfigForRunMode(&dsl.Script{Name: "unset", Protocol: "http"}, "load"); protocol.RedactsAuthoredEvidence(config) {
		t.Fatal("an unset setting must leave authored request content visible (default off)")
	}
}

var registerAuthoredEvidenceHTTPProtocol sync.Once

// End to end through ReplayScript and the native HTTP adapter: the request the
// script authored (headers, body, query) is visible in the replay report with
// the setting off and masked with it on; an operator-supplied sensitive replay
// variable (platform-managed) and the server's response stay masked in both.
func TestReplayHTTPAuthoredRequestEvidenceFollowsRunEvidenceSetting(t *testing.T) {
	protocolName := "authored-evidence-local-http"
	registerAuthoredEvidenceHTTPProtocol.Do(func() {
		protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"client_secret":%q}`, responseLiteralSecret)
	}))
	defer server.Close()
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "Authored evidence", Protocol: protocolName,
				Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{{
					Name: "Login", Type: "POST", Target: "/login?client_secret=" + authoredLiteralSecret,
					Headers: map[string]string{
						"Content-Type":  "application/json",
						"Authorization": "Bearer " + authoredLiteralToken,
						"X-Api-Key":     "{{var.api_key}}",
					},
					Body: `{"userPassword":"` + authoredLiteralPassword + `","client_secret":"` + authoredLiteralSecret + `"}`,
				}},
			}, mode.redact)
			report, err := ReplayScript(context.Background(), script, ReplayOptions{
				ReplaySessionID: "authored-evidence-" + mode.name, IgnoreThinkTime: true,
				Variables: map[string]string{"api_key": authoredPlatformSecret},
			})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{authoredPlatformSecret, responseLiteralSecret} {
				if strings.Contains(string(encoded), value) {
					t.Fatalf("value %q leaked into the replay report: %s", value, encoded)
				}
			}
			if len(report.Events) == 0 {
				t.Fatalf("no events: %s", report.Error)
			}
			event := report.Events[0]
			surfaces := map[string][2]string{
				"event target":  {event.Target, authoredLiteralSecret},
				"event auth":    {event.Headers["Authorization"], authoredLiteralToken},
				"event body pw": {event.Body, authoredLiteralPassword},
				"event body cs": {event.Body, authoredLiteralSecret},
			}
			if event.Snapshot == nil {
				t.Fatal("the failed request published no snapshot")
			}
			{
				surfaces["snapshot auth"] = [2]string{event.Snapshot.RequestHeaders["Authorization"], authoredLiteralToken}
				surfaces["snapshot body"] = [2]string{event.Snapshot.RequestBody, authoredLiteralPassword}
				if event.Snapshot.AuthoredRequestRetained != !mode.redact {
					t.Fatalf("snapshot marker = %v", event.Snapshot.AuthoredRequestRetained)
				}
			}
			for label, pair := range surfaces {
				if strings.Contains(pair[0], pair[1]) == mode.redact {
					t.Fatalf("%s = %q: authored %q visible want %v", label, pair[0], pair[1], !mode.redact)
				}
			}
			if event.Headers["X-Api-Key"] != redactedPayloadValue {
				t.Fatalf("platform-managed header = %q, want masked by name", event.Headers["X-Api-Key"])
			}
		})
	}
}

// engine.New and NewMultiGroup stamp the script's decision on the run's root
// redaction config, so every VUser publication derived from it follows it.
func TestRunRootRedactionConfigCarriesAuthoredEvidenceDecision(t *testing.T) {
	newScript := func(redact bool) *dsl.Script {
		return setRunEvidenceRedaction(&dsl.Script{
			Name: "root", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"},
			Actions: []dsl.Action{{Name: "a", Type: "GET", Target: "/"}},
		}, redact)
	}
	for _, mode := range runEvidenceRedactionModes {
		e, err := New(Config{Script: newScript(mode.redact), VUsers: 1, Duration: time.Second})
		if err != nil {
			t.Fatalf("%s: New: %v", mode.name, err)
		}
		if got := e.redaction.scrubsAuthoredEvidence(); got != mode.redact {
			t.Fatalf("%s: engine.New root scrubsAuthoredEvidence = %v", mode.name, got)
		}
	}
	mge, err := NewMultiGroup(MultiGroupConfig{Duration: time.Second, Groups: []GroupConfig{
		{Name: "on", Script: newScript(true), VUsers: 1},
		{Name: "off", Script: newScript(false), VUsers: 1},
	}})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	for _, group := range mge.groups {
		if got := group.RedactionConfig.scrubsAuthoredEvidence(); got != (group.Name == "on") {
			t.Fatalf("group %q scrubsAuthoredEvidence = %v", group.Name, got)
		}
	}
}
