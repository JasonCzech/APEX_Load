package dsl

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	networkprotocol "github.com/jasonczech/apexload/pkg/protocol/network"
)

func TestValidateScriptIssues_RendezvousValidation(t *testing.T) {
	script := &Script{
		Name:     "rendezvous-valid",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "sync-buyers",
			Type:   "rendezvous",
			Params: map[string]any{"count": 10, "timeout_ms": 5000},
		}, {
			Name: "checkout",
			Type: "request", Method: "GET",
			Target: "/checkout",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Params = map[string]any{"count": 0, "timeout_ms": -1}
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "sync-buyers": rendezvous count must be greater than zero`)
	assertIssue(t, issues, `actions[0] "sync-buyers": rendezvous timeout_ms must be zero or greater`)
}

func TestValidateScriptIssues_Base64BodyEncodingTemplates(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantIssue bool
	}{
		{name: "literal base64", body: "AP8=", wantIssue: false},
		{name: "trusted env full body", body: "{{env.BINARY_PAYLOAD}}", wantIssue: false},
		{name: "trusted secret full body", body: "{{ secret.BINARY_PAYLOAD }}", wantIssue: false},
		{name: "partial template rejected", body: "AP8={{var.token}}", wantIssue: true},
		{name: "var full body rejected", body: "{{var.payload}}", wantIssue: true},
		{name: "invalid literal rejected", body: "not base64!", wantIssue: true},
		// Composite env/secret templates must be rejected: stripping the outer
		// braces leaves an env.-prefixed expr that smuggles content between spans.
		{name: "composite env+untrusted+secret rejected", body: "{{env.X}}AAAA{{secret.Y}}", wantIssue: true},
		{name: "composite env+secret rejected", body: "{{env.X}}{{secret.Y}}", wantIssue: true},
		{name: "composite env+var rejected", body: "{{env.X}}{{var.Y}}", wantIssue: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := &Script{
				Name:     "base64-template-validation",
				Protocol: "http",
				Config:   ScriptConfig{BaseURL: "https://example.test"},
				Actions: []Action{{
					Name:         "Upload",
					Type:         "request",
					Method:       "POST",
					Target:       "/upload",
					Body:         tc.body,
					BodyEncoding: "base64",
				}},
			}
			issues := ValidateScriptIssues(script)
			hasIssue := false
			for _, issue := range issues {
				if strings.Contains(issue, "body_encoding base64") {
					hasIssue = true
				}
			}
			if hasIssue != tc.wantIssue {
				t.Fatalf("base64 issue present = %v, want %v; issues=%v", hasIssue, tc.wantIssue, issues)
			}
		})
	}
}

func TestValidateScriptIssues_FileAssetReferences(t *testing.T) {
	assetRoot := t.TempDir()
	if err := fileasset.RegisterRoot(assetRoot); err != nil {
		t.Fatalf("RegisterRoot: %v", err)
	}
	assetPath := filepath.Join(assetRoot, "upload.bin")
	if err := os.WriteFile(assetPath, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	assetRef := (&url.URL{Scheme: "file", Path: assetPath}).String()
	outsidePath := filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside asset: %v", err)
	}
	outsideRef := (&url.URL{Scheme: "file", Path: outsidePath}).String()

	base := func(source DataSource) *Script {
		script := &Script{
			Name:     "file-assets",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name:   "Upload",
				Type:   "request",
				Method: "POST",
				Target: "/upload",
				Body:   "payload={{file.upload}}",
			}},
		}
		if source.Name != "" || source.Type != "" {
			script.DataSources = []DataSource{source}
		}
		return script
	}

	tests := []struct {
		name      string
		source    DataSource
		wantIssue string
	}{
		{
			name:      "unknown asset",
			wantIssue: `actions[0] "Upload": references unknown file asset "upload"`,
		},
		{
			name:      "declared without bytes",
			source:    DataSource{Name: "upload", Type: "file", FileName: "upload.bin"},
			wantIssue: `actions[0] "Upload": references file asset "upload" without inline_data, path, or storage_ref`,
		},
		{
			name:   "inline file asset",
			source: DataSource{Name: "upload", Type: "file", InlineData: "bytes", FileName: "upload.bin"},
		},
		{
			name:   "storage ref file asset",
			source: DataSource{Name: "upload", Type: "file", StorageRef: assetRef, FileName: "upload.bin"},
		},
		{
			name:      "storage ref outside asset root",
			source:    DataSource{Name: "upload", Type: "file", StorageRef: outsideRef, FileName: "upload.bin"},
			wantIssue: `data_sources[0] "upload": file data source storage_ref is not an allowed file asset`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateScriptIssues(base(tc.source))
			if tc.wantIssue == "" {
				for _, issue := range issues {
					if strings.Contains(issue, "file asset") || strings.Contains(issue, "file data source") {
						t.Fatalf("unexpected file issue: %v", issues)
					}
				}
				return
			}
			assertIssueContains(t, issues, tc.wantIssue)
		})
	}
}

func TestValidateScriptIssues_ProjectScopedFileAssetReferences(t *testing.T) {
	assetRoot := t.TempDir()
	if err := fileasset.RegisterProjectRoot(assetRoot); err != nil {
		t.Fatalf("RegisterProjectRoot: %v", err)
	}
	projectDir, err := fileasset.ProjectAssetDir(assetRoot, "project-a")
	if err != nil {
		t.Fatalf("ProjectAssetDir: %v", err)
	}
	assetPath := filepath.Join(projectDir, "import-1", "capability", "upload.bin")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("bytes"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	assetRef := (&url.URL{Scheme: "file", Path: assetPath}).String()
	base := func(projectID string) *Script {
		return &Script{
			Name:      "project-file-assets",
			Protocol:  "http",
			ProjectID: projectID,
			Config:    ScriptConfig{BaseURL: "https://example.test"},
			DataSources: []DataSource{{
				Name: "upload", Type: "file", StorageRef: assetRef, FileName: "upload.bin",
			}},
			Actions: []Action{{
				Name: "Upload", Type: "request", Method: "POST", Target: "/upload", Body: "{{file.upload}}",
			}},
		}
	}

	if issues := ValidateScriptIssues(base("project-a")); len(issues) != 0 {
		t.Fatalf("owner project issues = %v, want none", issues)
	}
	issues := ValidateScriptIssues(base("project-b"))
	assertIssueContains(t, issues, `data_sources[0] "upload": file data source storage_ref is not an allowed file asset: file asset does not belong to project "project-b"`)

	csvScript := base("project-b")
	csvScript.DataSources[0] = DataSource{Name: "rows", Type: "csv", Path: assetPath, FirstRowHeader: true}
	csvScript.Actions[0].Body = "payload"
	issues = ValidateScriptIssues(csvScript)
	assertIssueContains(t, issues, `data_sources[0] "rows": data source path is not an allowed file asset: file asset does not belong to project "project-b"`)
}

func TestValidateScriptIssues_ErrorMessageValidation(t *testing.T) {
	script := &Script{
		Name:     "error-message-valid",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:  "Login Error",
			Type:  "error_message",
			Value: "Login failed for {{var.userid}}",
		}, {
			Name: "checkout",
			Type: "request", Method: "GET",
			Target: "/checkout",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Value = ""
	script.Actions[0].Params = map[string]any{"format": "Login failed for {{var.userid}}"}
	issues = ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues with params.format = %v, want none", issues)
	}

	script.Actions[0].Params = nil
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "Login Error": error_message requires value or params.format`)
}

func TestValidateScriptIssues_SourceEngineActionsDoNotRequireTarget(t *testing.T) {
	script := &Script{
		Name:     "source-engine-actions",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "helper",
			Type:   "custom_call",
			Params: map[string]any{"function": "my_helper"},
		}, {
			Name:   "timer",
			Type:   "source_timer",
			Params: map[string]any{"function": "lr_start_timer"},
		}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestCanonicalActionTypeMapsLegacyTypes(t *testing.T) {
	cases := map[string]string{
		"loadrunner_transaction": "source_transaction",
		"loadrunner_message":     "source_message",
		"loadrunner_runtime":     "source_runtime",
		"loadrunner_parameter":   "source_parameter",
		"source_transaction":     "source_transaction",
		"http_sequence":          "http_sequence",
		"request":                "request",
		"":                       "",
	}
	for in, want := range cases {
		if got := CanonicalActionType(in); got != want {
			t.Errorf("CanonicalActionType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateScriptIssues_LegacyLoadRunnerActionTypesStillValidate(t *testing.T) {
	// A script persisted before the enterprise-archive rename carries
	// loadrunner_* engine action types; they must remain recognized engine
	// actions and not be rejected as unknown.
	script := &Script{
		Name:     "legacy-source-actions",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "txn",
			Type: "loadrunner_transaction",
		}, {
			Name: "timer",
			Type: "loadrunner_timer",
		}},
	}

	if !isEngineActionType("loadrunner_transaction") {
		t.Fatal("isEngineActionType should recognize legacy loadrunner_transaction")
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none for legacy loadrunner_* action types", issues)
	}
}

func TestValidateScriptIssues_BrowserRuntimeActionParity(t *testing.T) {
	script := &Script{
		Name:     "browser-runtime-actions",
		Protocol: "browser",
		Actions: []Action{
			{Name: "hover", Type: "browser_hover", Selector: "#menu"},
			{Name: "double click", Type: "browser_dblclick", Selector: "#quantity"},
			{Name: "drag", Type: "browser_drag_and_drop", Selector: "#source", Params: map[string]any{"target": "#target"}},
			{Name: "multi select", Type: "browser_select", Selector: "#shipping", Params: map[string]any{"values": []any{"standard", "express"}}},
			{Name: "scroll", Type: "browser_scroll", Params: map[string]any{"delta_y": 320}},
			{Name: "back", Type: "browser_back", Params: map[string]any{"wait_until": "domcontentloaded"}},
			{Name: "forward", Type: "browser_forward"},
			{Name: "reload", Type: "browser_reload"},
			{Name: "wait count", Type: "browser_wait", Selector: ".toast", Params: map[string]any{"operator": "==", "count": 0}},
			{Name: "wait response", Type: "browser_wait_response", Target: "/api/orders", Params: map[string]any{"method": "GET", "status": 200, "timeout_ms": 1000}},
			{Name: "download", Type: "browser_download", Selector: "#export", Params: map[string]any{"filename_contains": "report"}},
			{Name: "set geolocation", Type: "browser_set_geolocation", Params: map[string]any{"latitude": 47.6062, "longitude": -122.3321, "accuracy": 10}},
			{Name: "upload", Type: "browser_set_files", Selector: "#upload", Value: "/tmp/upload.txt"},
			{Name: "press enter", Type: "browser_press_key", Selector: "#search", Value: "Enter"},
			{Name: "evaluate", Type: "browser_evaluate_js", Target: "window.app.ready", Metadata: map[string]any{"review_required": true}},
			{Name: "switch frame by index", Type: "browser_switch_frame", Params: map[string]any{"frame": 0}},
			{Name: "sync click", Type: "browser_click", Selector: "#save", Sync: &BrowserSyncPolicy{Type: "network_idle", TimeoutMs: 5000}},
		},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions = []Action{{
		Name: "bad control",
		Type: "control_loop",
		Control: &ControlFlow{
			Count:         -1,
			MaxIterations: -2,
			Condition:     &Condition{Left: "{{var.count}}", Op: "roughly", Right: "5"},
			Steps:         []Action{{Name: "bad hover", Type: "browser_hover"}},
		},
	}}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "bad control": control count must be zero or greater`)
	assertIssue(t, issues, `actions[0] "bad control": control max_iterations must be zero or greater`)
	assertIssue(t, issues, `actions[0] "bad control": condition op "roughly" is not supported`)
	assertIssue(t, issues, `actions[0].control.steps[0] "bad hover": missing target or selector`)
}

func TestValidateScriptIssues_BrowserNavigationWaitUntil(t *testing.T) {
	script := &Script{
		Name:     "browser-navigation",
		Protocol: "browser",
		Actions: []Action{{
			Name:   "bad wait",
			Type:   "browser_back",
			Params: map[string]any{"wait_until": "interactive"},
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "bad wait": browser_back params.wait_until must be load, domcontentloaded, networkidle, or commit`)
}

func TestValidateScriptIssues_BrowserDownload(t *testing.T) {
	script := &Script{
		Name:     "browser-download",
		Protocol: "browser",
		Actions: []Action{{
			Name: "bad download",
			Type: "browser_download",
			Params: map[string]any{
				"filename_regex": "[",
				"timeout_ms":     -5,
				"save_as":        "/tmp/report.csv",
				"save_dir":       "/tmp/downloads",
			},
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "bad download": missing target or selector`)
	assertIssueContains(t, issues, `actions[0] "bad download": browser_download params.filename_regex is invalid`)
	assertIssue(t, issues, `actions[0] "bad download": browser_download params.timeout_ms must be >= 0`)
	assertIssue(t, issues, `actions[0] "bad download": browser_download params.save_as and params.save_dir are mutually exclusive`)
}

func TestValidateScriptIssues_BrowserWaitResponse(t *testing.T) {
	script := &Script{
		Name:     "browser-response-wait",
		Protocol: "browser",
		Actions: []Action{{
			Name: "bad response wait",
			Type: "browser_wait_response",
			Params: map[string]any{
				"url_regex":  "[",
				"method":     "BREW",
				"status":     -1,
				"timeout_ms": -5,
			},
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, `actions[0] "bad response wait": browser_wait_response params.url_regex is invalid`)
	assertIssue(t, issues, `actions[0] "bad response wait": browser_wait_response params.status must be >= 0`)
	assertIssue(t, issues, `actions[0] "bad response wait": browser_wait_response params.method must be a valid HTTP method`)
	assertIssue(t, issues, `actions[0] "bad response wait": browser_wait_response params.timeout_ms must be >= 0`)
}

func TestValidateScriptIssues_ControlConditionCELExpr(t *testing.T) {
	script := &Script{
		Name:     "cel-control",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "branch",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Expr: `var.role == "admin" && int(var.count) >= 2`},
				Steps:     []Action{{Name: "home", Type: "GET", Target: "/"}},
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Control.Condition = &Condition{Expr: `var.role ==`}
	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, `actions[0] "branch": condition expr is invalid`)

	script.Actions[0].Control.Condition = &Condition{Expr: `var.role`}
	issues = ValidateScriptIssues(script)
	assertIssueContains(t, issues, `actions[0] "branch": condition expr is invalid`)
	assertIssueContains(t, issues, `expression must return bool`)
}

func TestValidateScriptIssues_ControlSwitch(t *testing.T) {
	script := &Script{
		Name:     "switch",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "route",
			Type: "control_switch",
			Control: &ControlFlow{
				Cases: []SwitchCase{
					{Condition: &Condition{Expr: `var.role == "admin"`}, Steps: []Action{{Name: "admin", Type: "GET", Target: "/admin"}}},
					{Condition: &Condition{Left: "{{var.role}}", Op: "eq", Right: "member"}, Steps: []Action{{Name: "member", Type: "GET", Target: "/member"}}},
				},
				Default: []Action{{Name: "guest", Type: "GET", Target: "/guest"}},
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}

	script.Actions[0].Control.Cases[0].Condition = nil
	script.Actions[0].Control.Cases[1].Steps = nil
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0].control.cases[0]: condition is required`)
	assertIssue(t, issues, `actions[0].control.cases[1]: steps must not be empty`)

	script.Actions[0].Control.Cases = nil
	issues = ValidateScriptIssues(script)
	assertIssueContains(t, issues, `control_switch requires at least one case`)
}

func TestValidateScriptIssues_ControlFlowLifecyclePaths(t *testing.T) {
	script := &Script{
		Name:     "browser-lifecycle-control",
		Protocol: "browser",
		Init: []Action{{
			Name: "init branch",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Left: "{{var.phase}}", Op: "exists"},
				Steps: []Action{{
					Name: "bad init hover",
					Type: "browser_hover",
				}},
			},
		}},
		Actions: []Action{{
			Name:   "open",
			Type:   "browser_navigate",
			Target: "/",
		}},
		Teardown: []Action{{
			Name: "teardown loop",
			Type: "control_loop",
			Control: &ControlFlow{
				Count: -1,
				Steps: []Action{{
					Name: "bad teardown click",
					Type: "browser_click",
				}},
			},
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `init[0].control.steps[0] "bad init hover": missing target or selector`)
	assertIssue(t, issues, `teardown[0] "teardown loop": control count must be zero or greater`)
	assertIssue(t, issues, `teardown[0].control.steps[0] "bad teardown click": missing target or selector`)
}

func TestValidateScriptIssues_RunLogicGlobalGoals(t *testing.T) {
	script := &Script{
		Name:     "invalid-global-goals",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL:  "https://example.test",
			RunLogic: RunLogicConfig{GlobalIterations: -1, MaxTransactions: -1},
		},
		Actions: []Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, "config.run_logic.global_iterations must be zero or greater")
	assertIssue(t, issues, "config.run_logic.max_transactions must be zero or greater")
}

func TestValidateScriptIssues_BodyFileRequiresLocalFileLoad(t *testing.T) {
	script := &Script{
		Name:     "body-file-upload",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:     "create",
			Type:     "POST",
			Target:   "/orders",
			BodyFile: "payloads/order.json",
		}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "create": body_file is only supported when loading scripts from a local file; inline body is required for API uploads`)

	script.Actions[0].Body = "{}"
	issues = ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "create": body and body_file cannot both be set`)
}

func TestValidateScriptIssues_SOAPValidation(t *testing.T) {
	script := &Script{
		Name:     "soap-invalid",
		Protocol: "soap",
		Config: ScriptConfig{
			BaseURL: "https://soap.example.com",
			SOAP: SOAPConfig{
				Version: "1.3",
				WSSecurity: &SOAPWSSecurityConfig{
					UsernameToken: &SOAPUsernameTokenConfig{PasswordType: "binary"},
					Timestamp:     &SOAPTimestampConfig{TTLSeconds: -1},
				},
			},
		},
		Actions: []Action{{
			Name: "login",
			Type: "request", Method: "GET",
			Target: "/AuthService",
			SOAP:   &SOAPActionConfig{BodyMode: "templated"},
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.soap.version must be 1.1 or 1.2")
	assertIssue(t, issues, "config.soap.ws_security.username_token.username is required")
	assertIssue(t, issues, "config.soap.ws_security.username_token.password is required")
	assertIssue(t, issues, "config.soap.ws_security.username_token.password_type must be text or digest")
	assertIssue(t, issues, "config.soap.ws_security.timestamp.ttl_seconds must be zero or greater")
	assertIssue(t, issues, "actions[0] \"login\": soap.body_mode must be raw_xml or generated")
}

func TestValidateScriptIssues_SOAPValidationAllowsSupportedFields(t *testing.T) {
	script := &Script{
		Name:     "soap-valid",
		Protocol: "soap",
		Config: ScriptConfig{
			BaseURL: "https://soap.example.com",
			SOAP: SOAPConfig{
				Version: "1.1",
				WSSecurity: &SOAPWSSecurityConfig{
					UsernameToken: &SOAPUsernameTokenConfig{
						Username:     "svc-user",
						Password:     "svc-pass",
						PasswordType: "digest",
					},
					Timestamp: &SOAPTimestampConfig{TTLSeconds: 60},
				},
			},
		},
		Actions: []Action{{
			Name: "login",
			Type: "request", Method: "GET",
			Target: "/AuthService",
			SOAP:   &SOAPActionConfig{BodyMode: "generated"},
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_WebRuntimeValidation(t *testing.T) {
	tooManyRedirects := httpprotocol.MaxConfiguredRedirects + 1
	script := &Script{
		Name:     "web-invalid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			RunLogic: RunLogicConfig{
				BlockScheduling: "weighted",
				ActionSequence:  []string{"init", "middle", "teardown"},
			},
			Log: LogConfig{
				ExtendedOptions: []string{"advanced_trace", "wiretap"},
			},
			HTTP: HTTPConfig{
				Version:          "3",
				MaxConnsPerHost:  httpprotocol.MaxConfiguredConnsPerHost + 1,
				MaxIdleConns:     -1,
				ConnectTimeoutMs: -1,
				RequestTimeoutMs: int(httpprotocol.MaxConfiguredTimeout/time.Millisecond) + 1,
				TLSTimeoutMs:     -1,
				IdleTimeoutMs:    int(httpprotocol.MaxConfiguredTimeout/time.Millisecond) + 1,
				TLS:              TLSConfig{MinVersion: "1.3", MaxVersion: "1.2"},
				MaxRedirects:     &tooManyRedirects,
				ProxyMode:        "corp",
				Auth:             AuthConfig{Type: "hawk"},
			},
			Browser: BrowserConfig{
				DownloadResources: "css_only",
			},
			WebRuntime: WebRuntimeConfig{
				AdditionalAttributes: map[string]string{"": "bad"},
				DownloadFilters: DownloadFiltersConfig{
					Mode: "allow_list",
				},
				ContentChecks: []ContentCheckConfig{{
					Text:  "",
					Scope: "global",
				}},
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "preset",
					Preset:        "dialup",
					LatencyMs:     -1,
					JitterMs:      -5,
					PacketLossPct: 120,
				},
				Streaming: StreamingConfig{
					Mode:              "fixed",
					BufferSizeSeconds: -1,
				},
				DataFormatting: DataFormattingConfig{
					Chain: []DFETransform{{Type: ""}, {Type: "custom_dfe"}},
				},
			},
			Observability: ObservabilityConfig{
				ExternalMetrics: []ExternalMetricConfig{{
					URL:        "ftp://metrics.example.test/metrics",
					Format:     "json",
					MetricName: "",
				}},
			},
		},
		Actions: []Action{{
			Name: "home",
			Type: "request", Method: "GET",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.run_logic.block_scheduling must be sequential, random, or percentage")
	assertIssue(t, issues, "config.run_logic.action_sequence[1] must be init, action, teardown, vuser_init, or vuser_end")
	assertIssue(t, issues, "config.log.extended_options[1] must be parameter_substitution, data_returned, or advanced_trace")
	assertIssue(t, issues, `config.http.version: unsupported HTTP version "3"`)
	assertIssue(t, issues, "config.http.max_conns_per_host must be between 0 and 256")
	assertIssue(t, issues, "config.http.max_idle_conns must be between 0 and 32")
	assertIssue(t, issues, "config.http.connect_timeout_ms must be between 0 and 3600000")
	assertIssue(t, issues, "config.http.request_timeout_ms must be between 0 and 3600000")
	assertIssue(t, issues, "config.http.tls_timeout_ms must be between 0 and 3600000")
	assertIssue(t, issues, "config.http.idle_timeout_ms must be between 0 and 3600000")
	assertIssue(t, issues, "config.http.max_redirects must be between -1 and 50")
	assertIssue(t, issues, `config.http.auth.type: unsupported HTTP auth type "hawk"`)
	assertIssue(t, issues, "config.http.tls.max_version must not be lower than min_version")
	assertIssue(t, issues, "config.http.proxy_mode must be none, system, custom, or pac")
	assertIssue(t, issues, "config.browser.download_resources must be none, recorded, or all")
	assertIssue(t, issues, "config.web_runtime.additional_attributes keys must be non-empty")
	assertIssue(t, issues, "config.web_runtime.download_filters.hosts is required when download filter mode is allow_list or block_list")
	assertIssue(t, issues, "config.web_runtime.content_checks[0].text is required")
	assertIssue(t, issues, "config.web_runtime.content_checks[0].scope must be any_response, main_document, or resource")
	assertIssue(t, issues, "config.web_runtime.network_simulation.preset must be 56k, dsl, t1, or 10mbps when mode is preset")
	assertIssue(t, issues, "config.web_runtime.network_simulation.latency_ms must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.network_simulation.jitter_ms must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.network_simulation.packet_loss_pct must be between 0 and 100")
	assertIssue(t, issues, "config.web_runtime.streaming.buffer_size_seconds must be zero or greater")
	assertIssue(t, issues, "config.web_runtime.streaming.fixed_bitrate_kbps must be greater than zero when mode is fixed")
	assertIssue(t, issues, "config.observability.external_metrics[0].name is required")
	assertIssue(t, issues, "config.observability.external_metrics[0].url must be a valid http or https URL")
	assertIssue(t, issues, "config.observability.external_metrics[0].format must be prometheus")
	assertIssue(t, issues, "config.observability.external_metrics[0].metric_name is required")
	assertIssue(t, issues, "config.web_runtime.data_formatting.chain[0].type is required")
	assertIssue(t, issues, "config.web_runtime.data_formatting.chain[1].type \"custom_dfe\" is not supported")
}

func TestValidateScriptIssues_HTTPRejectsUnsupportedTLSAndProxy(t *testing.T) {
	script := &Script{
		Name:     "http-invalid-security-config",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: HTTPConfig{
				TLS:       TLSConfig{MinVersion: "ssl3", MaxVersion: "tls-next"},
				ProxyMode: "custom",
				Proxy:     ProxyConfig{URL: "file:///tmp/proxy.sock"},
			},
		},
		Actions: []Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, "config.http.tls.min_version must be 1.0, 1.1, 1.2, or 1.3")
	assertIssue(t, issues, "config.http.tls.max_version must be 1.0, 1.1, 1.2, or 1.3")
	assertIssue(t, issues, "config.http.proxy.url must be an absolute http, https, socks5, or socks5h URL")
}

func TestValidateScriptIssues_HTTPUsesRuntimeDefaultTLSMinimum(t *testing.T) {
	script := &Script{
		Name:     "http-invalid-default-tls-range",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			HTTP:    HTTPConfig{TLS: TLSConfig{MaxVersion: "1.1"}},
		},
		Actions: []Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}},
	}

	assertIssue(t, ValidateScriptIssues(script), "config.http.tls.max_version must not be lower than min_version")
}

func TestValidateScriptIssues_WebRuntimeValidationAllowsSupportedFields(t *testing.T) {
	customProxy := "http://proxy.example.test:8080"
	failIfFound := true
	cacheEnabled := true
	cacheContentURLs := true
	checkNewerVersions := true
	newUserPerIter := true

	script := &Script{
		Name:     "web-valid",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			RunLogic: RunLogicConfig{
				Iterations:       10,
				AutoTransactions: "per_action",
				BlockScheduling:  "sequential",
				ActionSequence:   []string{"init", "action", "teardown"},
			},
			Log: LogConfig{
				Level:           "extended",
				OnErrorOnly:     true,
				ExtendedOptions: []string{"parameter_substitution", "advanced_trace"},
			},
			HTTP: HTTPConfig{
				ProxyMode: "custom",
				Proxy: ProxyConfig{
					URL: customProxy,
				},
			},
			Browser: BrowserConfig{
				CacheEnabled:       &cacheEnabled,
				CacheContentURLs:   &cacheContentURLs,
				CheckNewerVersions: &checkNewerVersions,
				NewUserPerIter:     &newUserPerIter,
				DownloadResources:  "recorded",
			},
			WebRuntime: WebRuntimeConfig{
				AdditionalAttributes: map[string]string{"env": "perf", "region": "east"},
				DownloadFilters: DownloadFiltersConfig{
					Mode:  "block_list",
					Hosts: []string{"metrics.example.test", "cdn.example.test"},
				},
				ContentChecks: []ContentCheckConfig{{
					Text:        "Access Denied",
					Scope:       "any_response",
					FailIfFound: &failIfFound,
				}},
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "custom",
					BandwidthKbps: 2048,
					LatencyMs:     75,
					JitterMs:      12,
					PacketLossPct: 0.5,
				},
				Streaming: StreamingConfig{
					Mode:                "adaptive",
					BufferSizeSeconds:   30,
					BandwidthEstimation: "rolling",
				},
				DataFormatting: DataFormattingConfig{
					AutoDetect: true,
					Chain: []DFETransform{
						{Type: "base64"},
						{Type: "gzip"},
						{Type: "json_to_xml"},
						{Type: "xml_to_json"},
						{Type: "url_decode"},
						{Type: "url_encode"},
						{Type: "html_unescape"},
						{Type: "html_escape"},
					},
				},
			},
		},
		Actions: []Action{{
			Name: "home",
			Type: "request", Method: "GET",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_WebRuntimeSourceIPs(t *testing.T) {
	makeScript := func(ips []string) *Script {
		return &Script{
			Name:     "spoof",
			Protocol: "http",
			Config: ScriptConfig{
				BaseURL:    "https://example.test",
				WebRuntime: WebRuntimeConfig{SourceIPs: ips},
			},
			Actions: []Action{{Name: "home", Type: "request", Method: "GET", Target: "/"}},
		}
	}
	hasIssue := func(issues []string, substr string) bool {
		for _, issue := range issues {
			if strings.Contains(issue, substr) {
				return true
			}
		}
		return false
	}

	t.Run("valid IPv4 and IPv6 pass", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"10.0.0.1", "192.168.1.5", "::1", "fe80::1"}))
		if hasIssue(issues, "source_ips") {
			t.Fatalf("unexpected source_ips issue: %v", issues)
		}
	})

	t.Run("invalid IPs rejected", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"10.0.0.1", "10.0.0.300", "not-an-ip"}))
		if !hasIssue(issues, "source_ips[1]") || !hasIssue(issues, "source_ips[2]") {
			t.Fatalf("expected invalid source_ips issues, got %v", issues)
		}
		if hasIssue(issues, "source_ips[0]") {
			t.Fatalf("valid first IP should not be flagged, got %v", issues)
		}
	})

	t.Run("blank entry rejected", func(t *testing.T) {
		issues := ValidateScriptIssues(makeScript([]string{"   "}))
		if !hasIssue(issues, "source_ips[0] must not be empty") {
			t.Fatalf("expected blank source_ips issue, got %v", issues)
		}
	})
}

func TestValidateScriptIssues_WebRuntimeCustomNetworkSimulationAllowsLossOnly(t *testing.T) {
	script := &Script{
		Name:     "web-loss-only",
		Protocol: "http",
		Config: ScriptConfig{
			BaseURL: "https://example.test",
			WebRuntime: WebRuntimeConfig{
				NetworkSimulation: NetworkSimulationConfig{
					Mode:          "custom",
					PacketLossPct: 5,
				},
			},
		},
		Actions: []Action{{
			Name: "home",
			Type: "request", Method: "GET",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidation(t *testing.T) {
	latitude := 120.0
	longitude := -200.0
	accuracy := -1.0
	script := &Script{
		Name:     "browser-invalid",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "gecko",
				DeviceProfile: "unknown_phone",
				Orientation:   "diagonal",
				Timezone:      "Mars/Olympus",
				Viewport: BrowserViewport{
					Width:  -1,
					Height: -2,
				},
				Geolocation: BrowserGeolocation{
					Latitude:  &latitude,
					Longitude: &longitude,
					Accuracy:  &accuracy,
				},
				HTTPCredentials: BrowserHTTPCredentials{
					Origin: "ftp://secure.example",
					Send:   "eager",
				},
				Proxy: BrowserProxyConfig{
					Server:   "ftp://proxy.example:3128",
					Password: "proxy-pass",
				},
				ClientCertificates: []BrowserClientCertificate{{
					Origin:   "http://mtls.example",
					CertPath: "/certs/client.pem",
				}, {
					Origin:   "https://mtls.example",
					CertPath: "/certs/client.pem",
					KeyPath:  "/certs/client-key.pem",
					PfxPath:  "/certs/client.p12",
				}},
			},
		},
		Actions: []Action{
			{
				Name:   "missing scroll delta",
				Type:   "browser_scroll",
				Target: "#app",
			},
			{
				Name:   "clear fill",
				Type:   "browser_fill",
				Target: "#name",
			},
			{
				Name:   "missing select value",
				Type:   "browser_select",
				Target: "#country",
			},
			{
				Name:   "bad swipe",
				Type:   "browser_swipe",
				Target: "#carousel",
				Params: map[string]any{
					"direction": "northwest",
					"distance":  0,
				},
			},
			{
				Name:   "bad long press",
				Type:   "browser_long_press",
				Target: "#hold",
				Params: map[string]any{
					"duration_ms": 0,
				},
			},
			{
				Name:   "missing upload file",
				Type:   "browser_set_files",
				Target: "#upload",
			},
			{
				Name: "missing key",
				Type: "browser_press_key",
			},
			{
				Name: "bad geolocation",
				Type: "browser_set_geolocation",
				Params: map[string]any{
					"latitude":  95,
					"longitude": -185,
					"accuracy":  -1,
				},
			},
			{
				Name:   "empty select values",
				Type:   "browser_select",
				Target: "#shipping",
				Params: map[string]any{"values": []any{" ", ""}},
			},
			{
				Name:   "bad wait count",
				Type:   "browser_wait",
				Target: ".toast",
				Params: map[string]any{"operator": "~", "count": -1},
			},
			{
				Name:   "operator without count",
				Type:   "browser_wait",
				Target: ".toast",
				Params: map[string]any{"operator": ">="},
			},
		},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.browser.engine must be chromium or webkit")
	assertIssue(t, issues, `config.browser.device_profile "unknown_phone" is not recognized`)
	assertIssue(t, issues, "config.browser.orientation must be portrait or landscape")
	assertIssue(t, issues, "config.browser.viewport.width must be zero or greater")
	assertIssue(t, issues, "config.browser.viewport.height must be zero or greater")
	assertIssue(t, issues, `config.browser.timezone "Mars/Olympus" is not a valid IANA timezone`)
	assertIssue(t, issues, "config.browser.geolocation.latitude must be between -90 and 90")
	assertIssue(t, issues, "config.browser.geolocation.longitude must be between -180 and 180")
	assertIssue(t, issues, "config.browser.geolocation.accuracy must be zero or greater")
	assertIssue(t, issues, "config.browser.http_credentials.username is required when http_credentials is set")
	assertIssue(t, issues, "config.browser.http_credentials.password is required when http_credentials is set")
	assertIssue(t, issues, "config.browser.http_credentials.origin must be an HTTP(S) origin")
	assertIssue(t, issues, "config.browser.http_credentials.send must be unauthorized or always")
	assertIssue(t, issues, "config.browser.proxy.server must be an HTTP, HTTPS, SOCKS, or host:port proxy")
	assertIssue(t, issues, "config.browser.proxy.password requires username")
	assertIssue(t, issues, "config.browser.client_certificates[0].origin must be an HTTPS origin")
	assertIssue(t, issues, "config.browser.client_certificates[0] requires cert_path and key_path, or pfx_path")
	assertIssue(t, issues, "config.browser.client_certificates[1] must use either cert_path/key_path or pfx_path, not both")
	assertIssue(t, issues, `actions[0] "missing scroll delta": browser_scroll requires params.delta_x or params.delta_y`)
	assertIssue(t, issues, `actions[2] "missing select value": browser_select requires value`)
	assertIssue(t, issues, `actions[3] "bad swipe": browser_swipe direction must be left, right, up, or down`)
	assertIssue(t, issues, `actions[3] "bad swipe": browser_swipe distance must be greater than zero`)
	assertIssue(t, issues, `actions[4] "bad long press": browser_long_press duration_ms must be greater than zero`)
	assertIssue(t, issues, `actions[5] "missing upload file": browser_set_files requires value, params.file, params.path, or params.files`)
	assertIssue(t, issues, `actions[6] "missing key": browser_press_key requires value, params.key, or params.key_name`)
	assertIssue(t, issues, `actions[7] "bad geolocation": browser_set_geolocation params.latitude must be between -90 and 90`)
	assertIssue(t, issues, `actions[7] "bad geolocation": browser_set_geolocation params.longitude must be between -180 and 180`)
	assertIssue(t, issues, `actions[7] "bad geolocation": browser_set_geolocation params.accuracy must be zero or greater`)
	assertIssue(t, issues, `actions[8] "empty select values": browser_select requires value`)
	assertIssue(t, issues, `actions[9] "bad wait count": browser_wait params.operator must be one of =, ==, ===, !=, !==, >, >=, <, <=`)
	assertIssue(t, issues, `actions[9] "bad wait count": browser_wait params.count must be >= 0`)
	assertIssue(t, issues, `actions[10] "operator without count": browser_wait params.count is required when params.operator is set`)
}

func TestValidateScriptIssues_WebSocketValidation(t *testing.T) {
	script := &Script{
		Name:     "websocket-invalid",
		Protocol: "websocket",
		Config: ScriptConfig{
			BaseURL: "wss://example.test",
			WebSocket: WebSocketConfig{
				ReadTimeoutMs:  -1,
				WriteTimeoutMs: -2,
				MaxMessageSize: -3,
			},
		},
		Actions: []Action{{
			Name: "missing target",
			Type: "ws_connect",
		}, {
			Name: "missing body",
			Type: "ws_send",
		}, {
			Name:   "bad type",
			Type:   "ws_send",
			Body:   "hello",
			Params: map[string]any{"type": "stream"},
		}, {
			Name:   "bad timeout",
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": -1},
		}, {
			Name: "unsupported",
			Type: "ws_subscribe",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.websocket.read_timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.websocket.write_timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.websocket.max_message_size must be zero or greater")
	assertIssue(t, issues, `actions[0] "missing target": missing target`)
	assertIssue(t, issues, `actions[1] "missing body": ws_send requires body`)
	assertIssue(t, issues, `actions[2] "bad type": ws_send type must be text or binary`)
	assertIssue(t, issues, `actions[3] "bad timeout": ws_receive timeout_ms must be zero or greater`)
	assertIssue(t, issues, `actions[4] "unsupported": unsupported websocket action type "ws_subscribe"`)
}

func TestValidateScriptIssues_WebSocketRejectsOversizedMessageLimit(t *testing.T) {
	script := &Script{
		Name:     "websocket-oversized",
		Protocol: "websocket",
		Config: ScriptConfig{
			BaseURL:   "wss://example.test",
			WebSocket: WebSocketConfig{MaxMessageSize: MaxWebSocketMessageBytes + 1},
		},
		Actions: []Action{{Name: "connect", Type: "ws_connect", Target: "/ws"}},
	}

	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, fmt.Sprintf("config.websocket.max_message_size must not exceed %d", MaxWebSocketMessageBytes))
}

func TestValidateScriptIssues_WebSocketValidationAllowsSupportedFields(t *testing.T) {
	script := &Script{
		Name:     "websocket-valid",
		Protocol: "websocket",
		Config: ScriptConfig{
			BaseURL: "wss://example.test",
			WebSocket: WebSocketConfig{
				ReadTimeoutMs:  5000,
				WriteTimeoutMs: 2500,
				MaxMessageSize: 65536,
				Subprotocol:    "graphql-ws",
				Origin:         "https://app.example.test",
				Compression:    true,
			},
		},
		Actions: []Action{{
			Name:   "connect",
			Type:   "ws_connect",
			Target: "/ws",
		}, {
			Name:   "send",
			Type:   "ws_send",
			Body:   `{"type":"ping"}`,
			Params: map[string]any{"type": "text"},
		}, {
			Name:   "receive",
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": 1000},
		}, {
			Name: "ping",
			Type: "ws_ping",
		}, {
			Name: "close",
			Type: "ws_close",
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_GRPCValidation(t *testing.T) {
	script := &Script{
		Name:     "grpc-invalid",
		Protocol: "grpc",
		Config: ScriptConfig{
			BaseURL: "grpc.example.test:443",
			GRPC: GRPCConfig{
				ProtoFiles:     []string{""},
				MaxRecvMsgSize: -1,
				TimeoutMs:      -2,
			},
		},
		Actions: []Action{{
			Name: "missing target",
			Type: "grpc_unary",
		}, {
			Name:   "bad target",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl",
		}, {
			Name:   "bad timeout",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl/DistributeScript",
			Params: map[string]any{"timeout_ms": -1},
		}, {
			Name:   "streaming",
			Type:   "grpc_server_stream",
			Target: "apexload.v1.GeneratorControl/StreamMetrics",
			Params: map[string]any{"max_messages": -1},
		}, {
			Name: "unsupported",
			Type: "grpc_batch",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, "config.grpc.max_recv_msg_size must be zero or greater")
	assertIssue(t, issues, "config.grpc.timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.grpc.proto_files[0] must be non-empty")
	assertIssue(t, issues, `actions[0] "missing target": missing target`)
	assertIssue(t, issues, `actions[1] "bad target": grpc_unary target must be service/method`)
	assertIssue(t, issues, `actions[2] "bad timeout": grpc_unary timeout_ms must be zero or greater`)
	assertIssue(t, issues, `actions[3] "streaming": grpc_server_stream max_messages must be zero or greater`)
	assertIssue(t, issues, `actions[4] "unsupported": unsupported grpc action type "grpc_batch"`)
}

func TestValidateScriptIssues_GRPCValidationRejectsRuntimeLimitOverflow(t *testing.T) {
	script := &Script{
		Name: "grpc-oversized", Protocol: "grpc",
		Config: ScriptConfig{BaseURL: "grpc.example.test:443", GRPC: GRPCConfig{MaxRecvMsgSize: MaxGRPCStreamBytes + 1}},
		Actions: []Action{{
			Name: "stream", Type: "grpc_server_stream", Target: "apexload.v1.GeneratorControl/StreamMetrics",
			Params: map[string]any{"max_messages": MaxGRPCStreamMessages + 1},
		}},
	}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, fmt.Sprintf("config.grpc.max_recv_msg_size must not exceed %d", MaxGRPCStreamBytes))
	assertIssue(t, issues, fmt.Sprintf(`actions[0] "stream": grpc_server_stream max_messages must not exceed %d`, MaxGRPCStreamMessages))
}

func TestValidateScriptIssues_GRPCValidationAllowsUnaryReflection(t *testing.T) {
	script := &Script{
		Name:     "grpc-valid",
		Protocol: "grpc",
		Config: ScriptConfig{
			BaseURL: "grpc.example.test:443",
			GRPC: GRPCConfig{
				UseReflection:  true,
				PlainText:      true,
				MaxRecvMsgSize: 1048576,
				TimeoutMs:      5000,
				Metadata:       map[string]string{"x-env": "perf"},
				Authority:      "orders.example.test",
			},
		},
		Actions: []Action{{
			Name:   "cache",
			Type:   "grpc_unary",
			Target: "apexload.v1.GeneratorControl/DistributeScript",
			Body:   `{"scriptId":"script-1"}`,
			Params: map[string]any{"timeout_ms": 1000},
		}, {
			Name:   "heartbeat",
			Type:   "grpc_bidi_stream",
			Target: "apexload.v1.GeneratorControl/Heartbeat",
			Params: map[string]any{"max_messages": 1, "messages": []any{map[string]any{"generatorId": "gen-1"}}},
		}},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidationAllowsSupportedFields(t *testing.T) {
	touchMode := true
	clientMetrics := true
	script := &Script{
		Name:     "browser-valid",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "webkit",
				DeviceProfile: "iphone_se",
				TouchMode:     &touchMode,
				Orientation:   "portrait",
				ClientMetrics: &clientMetrics,
				HTTPCredentials: BrowserHTTPCredentials{
					Username: "browser-user",
					Password: "{{secret.browser_password}}",
					Origin:   "https://secure.example",
					Send:     "unauthorized",
				},
				Proxy: BrowserProxyConfig{
					Server:   "proxy.example:3128",
					Bypass:   ".internal,localhost",
					Username: "proxy-user",
					Password: "{{secret.proxy_password}}",
				},
				ClientCertificates: []BrowserClientCertificate{{
					Origin:     "https://mtls.example",
					PfxPath:    "/certs/client.p12",
					Passphrase: "{{secret.cert_passphrase}}",
				}},
				Viewport: BrowserViewport{
					Width:  375,
					Height: 667,
				},
			},
		},
		Actions: []Action{
			{
				Name:   "open",
				Type:   "browser_navigate",
				Target: "/",
			},
			{
				Name:   "tap",
				Type:   "browser_tap",
				Target: "#menu",
			},
			{
				Name:   "swipe",
				Type:   "browser_swipe",
				Target: "#carousel",
				Params: map[string]any{
					"direction": "left",
					"distance":  180,
				},
			},
			{
				Name:   "press",
				Type:   "browser_long_press",
				Target: "#hold",
				Params: map[string]any{
					"duration_ms": 350,
				},
			},
			{
				Name:   "clear fill",
				Type:   "browser_fill",
				Target: "#query",
				Value:  "",
			},
			{
				Name:   "select",
				Type:   "browser_select",
				Target: "#country",
				Value:  "US",
			},
		},
	}

	issues := ValidateScriptIssues(script)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptIssues_BrowserValidationRejectsMismatchedDeviceProfileEngine(t *testing.T) {
	script := &Script{
		Name:     "browser-mismatch",
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				Engine:        "chromium",
				DeviceProfile: "iphone_se",
			},
		},
		Actions: []Action{{
			Name:   "open",
			Type:   "browser_navigate",
			Target: "/",
		}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `config.browser.engine "chromium" is incompatible with device_profile "iphone_se" (requires webkit)`)
}

func TestValidateScriptIssues_DatabaseProtocolRequiresConfigAndSQL(t *testing.T) {
	script := &Script{
		Name:     "DB",
		Protocol: "database",
		Config: ScriptConfig{
			Database: DatabaseConfig{Driver: "sqlite", DSN: ":memory:", MaxRows: -1},
		},
		Actions: []Action{{Name: "Query", Type: "db_query"}},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `actions[0] "Query": database action requires target or body SQL`)
	assertIssue(t, issues, "config.database.max_rows must be zero or greater")
	for _, issue := range issues {
		if issue == "config.base_url is empty" {
			t.Fatalf("issues = %v, database should not require base_url", issues)
		}
	}
}

func TestValidateScriptIssues_DatabaseProtocolAllowsTransactionActionsWithoutTarget(t *testing.T) {
	script := &Script{
		Name:     "DB",
		Protocol: "database",
		Config:   ScriptConfig{Database: DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}},
		Actions:  []Action{{Name: "Begin", Type: "db_begin"}, {Name: "Query", Type: "db_query", Target: "select 1"}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
}

func TestValidateScriptIssues_NetworkProtocolValidatesBundleActions(t *testing.T) {
	script := &Script{
		Name:     "Network",
		Protocol: "network",
		Config: ScriptConfig{
			Network: NetworkConfig{Subprotocol: "smtp", Port: -1, TimeoutMs: -1, To: []string{""}},
		},
		Actions: []Action{
			{Name: "Lookup", Type: "dns_query", Params: map[string]any{"record_type": "SRV"}},
			{Name: "Socket", Type: "tcp_request", Params: map[string]any{"read_bytes": -1}},
			{Name: "Directory", Type: "ldap_search", Params: map[string]any{"max_entries": -1}},
		},
	}

	issues := ValidateScriptIssues(script)

	assertIssue(t, issues, `actions[0] "Lookup": dns action requires target host`)
	assertIssue(t, issues, `actions[0] "Lookup": dns record_type must be A, AAAA, CNAME, MX, NS, or TXT`)
	assertIssue(t, issues, `actions[1] "Socket": socket action requires target address`)
	assertIssue(t, issues, `actions[1] "Socket": read_bytes must be zero or greater`)
	assertIssue(t, issues, `actions[2] "Directory": ldap_search requires target base DN`)
	assertIssue(t, issues, `actions[2] "Directory": max_entries must be zero or greater`)
	assertIssue(t, issues, "config.network.port must be zero or greater")
	assertIssue(t, issues, "config.network.timeout_ms must be zero or greater")
	assertIssue(t, issues, "config.network.to[0] must be non-empty")
	for _, issue := range issues {
		if issue == "config.base_url is empty" {
			t.Fatalf("issues = %v, network should not require base_url", issues)
		}
	}
}

func TestValidateScriptIssues_NetworkProtocolAllowsSupportedActions(t *testing.T) {
	script := &Script{
		Name:     "Network",
		Protocol: "network",
		Config:   ScriptConfig{Network: NetworkConfig{Subprotocol: "dns", TimeoutMs: 1000}},
		Actions: []Action{
			{Name: "Lookup", Type: "dns_query", Target: "localhost", Params: map[string]any{"record_type": "A"}},
			{Name: "Socket", Type: "tcp_request", Target: "127.0.0.1:7", Body: "ping", Params: map[string]any{"read_bytes": 4}},
			{Name: "Bind", Type: "ldap_bind"},
		},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %#v, want none", issues)
	}
}

func TestValidateScriptIssues_NetworkProtocolBoundsTimeouts(t *testing.T) {
	tooLarge := int(networkprotocol.MaxConfiguredTimeout/time.Millisecond) + 1
	script := &Script{
		Name:     "Network",
		Protocol: "network",
		Config:   ScriptConfig{Network: NetworkConfig{Subprotocol: "tcp", TimeoutMs: tooLarge}},
		Actions: []Action{{
			Name: "Socket", Type: "tcp_request", Target: "127.0.0.1:7",
			Params: map[string]any{"timeout_ms": tooLarge},
		}},
	}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, fmt.Sprintf("config.network.timeout_ms must not exceed %d", networkprotocol.MaxConfiguredTimeout/time.Millisecond))
	assertIssue(t, issues, fmt.Sprintf(`actions[0] "Socket": timeout_ms must not exceed %d`, networkprotocol.MaxConfiguredTimeout/time.Millisecond))
}

func assertIssue(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if issue == want {
			return
		}
	}
	t.Fatalf("issues = %v, want %q", issues, want)
}

func assertIssueContains(t *testing.T, issues []string, want string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue, want) {
			return
		}
	}
	t.Fatalf("issues = %v, want issue containing %q", issues, want)
}

func TestValidateExtractorIssuesInSequenceChildren(t *testing.T) {
	script := &Script{
		Name:     "seq-child-extractors",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "seq",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "/a",
			Params: map[string]any{"requests": []any{
				map[string]any{
					"name": "child", "type": "GET", "method": "GET", "target": "/a",
					"extractors": []any{map[string]any{
						"name": "tok", "source": "body", "type": "boundary", "expression": "a|||b",
						"on_miss": "error", "mode": "ALL", "convert": "url-encode",
					}},
				},
			}},
		}},
	}

	issues := ValidateScriptIssues(script)
	var onMiss, mode, convert bool
	for _, issue := range issues {
		if strings.Contains(issue, "on_miss") {
			onMiss = true
		}
		if strings.Contains(issue, "mode must be") {
			mode = true
		}
		if strings.Contains(issue, "convert must be") {
			convert = true
		}
	}
	if !onMiss || !mode || !convert {
		t.Errorf("sequence-child extractor typos must fail validation; issues = %v", issues)
	}
}

func TestValidateScriptIssues_SequenceChildBodyFile(t *testing.T) {
	newScript := func(child map[string]any) *Script {
		return &Script{
			Name:     "seq-child-body-file",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name:   "seq",
				Type:   "http_sequence",
				Method: "SEQUENCE",
				Target: "/a",
				Params: map[string]any{"requests": []any{child}},
			}},
		}
	}

	tests := []struct {
		name  string
		child map[string]any
		want  string
	}{
		{
			name:  "body_file alone is rejected for API uploads",
			child: map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body_file": "payloads/order.json"},
			want:  `actions[0] "seq".params.requests[0] "create": body_file is only supported when loading scripts from a local file; inline body is required for API uploads`,
		},
		{
			name:  "body and body_file conflict",
			child: map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body": "{}", "body_file": "payloads/order.json"},
			want:  `actions[0] "seq".params.requests[0] "create": body and body_file cannot both be set`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issues := ValidateScriptIssues(newScript(tc.child))
			assertIssue(t, issues, tc.want)
		})
	}
}

func TestValidateScriptIssues_SequenceChildWithoutBodyFileValid(t *testing.T) {
	script := &Script{
		Name:     "seq-child-ok",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "seq",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "/a",
			Params: map[string]any{"requests": []any{
				map[string]any{"name": "create", "type": "POST", "method": "POST", "target": "/orders", "body": "{}"},
			}},
		}},
	}
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "body_file") {
			t.Fatalf("unexpected body_file issue for inline-body child: %q", issue)
		}
	}
}

func TestValidateExtractorIssuesAcceptsNewEnums(t *testing.T) {
	script := &Script{
		Name:     "extractor-enums",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "a", Type: "GET", Method: "GET", Target: "/a",
			Extractors: []Extractor{
				{Name: "x", Source: "body", Type: "boundary", Expression: "a|||b", OnMiss: "empty", Convert: "html_url_encode"},
				{Name: "decoded", Source: "body", Type: "regex", Expression: "token=([^&]+)", Convert: "url_decode"},
				{Name: "y", Source: "body", Type: "regex", Expression: "v=(.+?);", Mode: "all", OnMiss: "fail"},
			},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) > 0 {
		t.Errorf("valid enum values rejected: %v", issues)
	}
}

func TestValidateExtractorIssuesRejectsUnsupportedTypes(t *testing.T) {
	script := &Script{
		Name:     "bad-extractor",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "a", Type: "GET", Method: "GET", Target: "/a",
			Extractors: []Extractor{
				{Name: "bad_header", Source: "header", Type: "header_query", Expression: "Location?code"},
				{Name: "bad_source", Source: "cookie", Type: "regex", Expression: "sid=(.+)"},
			},
		}},
	}
	issues := ValidateScriptIssues(script)
	assertIssueContains(t, issues, `extractors[0] type "header_query" is not supported for header extraction`)
	assertIssueContains(t, issues, `extractors[1] source must be "", "body", or "header"`)
}

// TestValidateExtractorIssuesSkipsSourceTypeAllowlistForNonHTTPProtocols locks
// in that non-HTTP protocol runtimes keep their own extractor source/type
// vocabularies: the HTTP body/header allowlist must not reject Kafka key,
// JMS property, database scalar/rows, network record_count, WebSocket/gRPC
// json, etc. Regression guard for the DSL-wide allowlist over-reach.
func TestValidateExtractorIssuesSkipsSourceTypeAllowlistForNonHTTPProtocols(t *testing.T) {
	cases := []struct {
		protocol string
		ext      Extractor
	}{
		{"kafka", Extractor{Name: "k", Source: "key", Type: "", Expression: ""}},
		{"kafka", Extractor{Name: "off", Source: "offset", Type: "", Expression: ""}},
		{"jms-solace", Extractor{Name: "p", Source: "property", Type: "", Expression: "JMSXGroupID"}},
		{"jms", Extractor{Name: "cid", Source: "correlation_id", Type: "", Expression: ""}},
		{"database", Extractor{Name: "s", Source: "scalar", Type: "", Expression: "$.scalar"}},
		{"database", Extractor{Name: "rows", Source: "rows", Type: "json", Expression: "$.rows[0].id"}},
		{"network", Extractor{Name: "rc", Source: "record_count", Type: "", Expression: "$.record_count"}},
		{"websocket", Extractor{Name: "m", Source: "message", Type: "json", Expression: "$.id"}},
		{"grpc", Extractor{Name: "g", Source: "body", Type: "json", Expression: "$.id"}},
	}
	for _, tc := range cases {
		action := Action{Name: "a", Extractors: []Extractor{tc.ext}}
		if issues := validateExtractorIssues(tc.protocol, "actions[0]", action); len(issues) > 0 {
			t.Errorf("protocol %s source %q type %q rejected: %v", tc.protocol, tc.ext.Source, tc.ext.Type, issues)
		}
	}

	// HTTP still enforces the allowlist.
	httpAction := Action{Name: "a", Extractors: []Extractor{{Name: "k", Source: "key", Type: "", Expression: ""}}}
	if issues := validateExtractorIssues("http", "actions[0]", httpAction); len(issues) == 0 {
		t.Errorf("http source %q should still be rejected by the allowlist", "key")
	}
}

func TestValidateExtractorIssuesRejectsUnsupportedAllModeForNonHTTPProtocols(t *testing.T) {
	for _, protocolName := range []string{"browser", "bruno", "websocket", "grpc", "network", "database", "sap_gui", "java_vuser", "kafka", "jms", "eventhubs"} {
		action := Action{Name: "a", Extractors: []Extractor{{
			Name: "values",
			Mode: "all",
		}}}
		issues := validateExtractorIssues(protocolName, "actions[0]", action)
		assertIssueContains(t, issues, `mode "all" is supported only by HTTP-family protocols`)
	}
}

// TestValidateScriptIssues_NestedJMSHonorsConfigDestination locks in that a JMS
// action nested inside a control block is NOT falsely flagged "missing target"
// when config.jms.destination supplies the destination.
func TestValidateScriptIssues_NestedJMSHonorsConfigDestination(t *testing.T) {
	script := &Script{
		Name:     "jms-control",
		Protocol: "jms-solace",
		Config:   ScriptConfig{JMS: JMSConfig{Provider: "solace", Destination: "orders.in", Mock: true}},
		Actions: []Action{{
			Name: "loop",
			Type: "control_loop",
			Control: &ControlFlow{
				Count: 1,
				Steps: []Action{{Name: "pub", Type: "jms_publish"}},
			},
		}},
	}
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "missing target") {
			t.Fatalf("nested jms_publish with config destination flagged missing target: %v", ValidateScriptIssues(script))
		}
	}
}

func TestValidateParallelBranchRuntimeSafetyRejectsUnsafeNestedControl(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{name: "nested parallel", action: Action{Type: "control_parallel"}, want: "control_parallel is not permitted"},
		{name: "rendezvous", action: Action{Type: "rendezvous"}, want: "rendezvous is not permitted"},
		{name: "unowned break", action: Action{Type: "control_break"}, want: "control_break is not permitted"},
		{name: "continue nested in condition", action: Action{Type: "control_if", Control: &ControlFlow{Condition: &Condition{Left: "x", Op: "exists"}, Steps: []Action{{Type: "control_continue"}}}}, want: "control_continue is not permitted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateParallelBranchRuntimeSafety([]Action{test.action}, "actions[0].control.branches[0].steps")
			assertIssueContains(t, issues, test.want)
		})
	}
}

func TestValidateParallelBranchRuntimeSafetyAllowsBoundedControl(t *testing.T) {
	actions := []Action{{
		Type: "control_if",
		Control: &ControlFlow{
			Condition: &Condition{Left: "{{var.ready}}", Op: "exists"},
			Steps:     []Action{{Name: "request", Type: "GET", Method: "GET", Target: "/ready"}},
		},
	}}
	if issues := validateParallelBranchRuntimeSafety(actions, "actions[0].control.branches[0].steps"); len(issues) != 0 {
		t.Fatalf("safe nested control rejected: %v", issues)
	}
}

func TestParallelBranchLocalLoopControl(t *testing.T) {
	for _, kind := range []string{"control_break", "control_continue"} {
		a := Action{Type: "control_loop", Control: &ControlFlow{Count: 3, Steps: []Action{{Type: "control_if", Control: &ControlFlow{Steps: []Action{{Type: kind}}}}}}}
		if issues := ValidateParallelBranchRuntimeSafety([]Action{a}, "branch"); len(issues) > 0 {
			t.Fatal(issues)
		}
		// A loop's else/default bodies are not iteration bodies and cannot own control.
		a.Control.Steps = nil
		a.Control.Default = []Action{{Type: kind}}
		if len(ValidateParallelBranchRuntimeSafety([]Action{a}, "branch")) == 0 {
			t.Fatal("non-body loop control admitted")
		}
	}
}
