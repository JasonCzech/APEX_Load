package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func engineVersion(t *testing.T, value string) dsl.OptionalHTTPVersion {
	t.Helper()
	version, err := dsl.NewHTTPVersionOverride(value)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
func TestHTTPVersionPreparationConfigurationAndReplayHash(t *testing.T) {
	for _, tc := range []struct{ alias, config, want string }{{"http/2", "", "2"}, {"http/1.1", "", "1.1"}, {"http/2", "1.1", "1.1"}, {"http/1.1", "2", "2"}, {"http/2", " ", " "}} {
		script := &dsl.Script{Protocol: tc.alias, Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Version: tc.config}}}
		if got := buildProtoConfig(script)["http_version"]; got != tc.want {
			t.Fatalf("alias=%s config=%q got=%q", tc.alias, tc.config, got)
		}
	}
	action := dsl.Action{Name: "leaf", Type: "GET", Target: "/"}
	script := &dsl.Script{Name: "policy hash", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{action}}
	inheritedHash := replayScriptHash(script)
	script.Actions[0].HTTPVersion = engineVersion(t, "auto")
	if got := replayScriptHash(script); got == "" || got == inheritedHash {
		t.Fatal("explicit auto changed neither source hash nor identity")
	}
	vu := &VUser{Script: script, Variables: map[string]string{}}
	if got := vu.resolveAction(script.Actions[0]); got.HTTPVersion != script.Actions[0].HTTPVersion {
		t.Fatal("engine dropped scalar forwarding")
	}
}

func TestHTTPVersionLoadAndReplayMixedSequenceSession(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic", Path: "/"})
		} else if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "synthetic" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprintf(w, "%d", r.ProtoMajor)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	for _, mode := range []string{"sequential", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			script := &dsl.Script{Name: "mixed sequence", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL, HTTP: dsl.HTTPConfig{Version: "2", MaxConnsPerHost: 1, TLS: dsl.TLSConfig{InsecureSkipVerify: true}}}, Actions: []dsl.Action{
				{Name: "Login", Type: "GET", Target: "/login", HTTPVersion: engineVersion(t, "1.1")},
				{Name: "Mixed", Type: "http_sequence", Params: map[string]any{"execution_mode": mode, "requests": []dsl.Action{
					{Name: "H1", Type: "GET", Target: "/one", HTTPVersion: engineVersion(t, "1.1"), Extractors: []dsl.Extractor{{Name: "first_version", Type: "regex", Source: "body", Expression: "([12])"}}},
					{Name: "H2", Type: "GET", Target: "/two", HTTPVersion: engineVersion(t, "2"), Extractors: []dsl.Extractor{{Name: "second_version", Type: "regex", Source: "body", Expression: "([12])"}}},
				}}},
			}}
			script.Config.RunLogic.Iterations = 1
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("issues=%v", issues)
			}
			instance, err := protocol.Get("http")
			if err != nil {
				t.Fatal(err)
			}
			if err := instance.Init(context.Background(), buildProtoConfig(script)); err != nil {
				t.Fatal(err)
			}
			vu := &VUser{ID: 1, Script: script, Proto: instance, Variables: make(map[string]string), Metrics: NewMetricsCollector(32), SnapshotStore: NewSnapshotStore(8)}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			vu.Run(ctx)
			if vu.Variables["first_version"] != "1" || vu.Variables["second_version"] != "2" {
				t.Fatalf("load variables=%v", vu.Variables)
			}
			_ = instance.Teardown(context.Background())
			report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true})
			if err != nil || report == nil || !report.Success {
				t.Fatalf("replay err=%v report=%v", err, report)
			}
			if report.FinalVariables["first_version"] != "1" || report.FinalVariables["second_version"] != "2" {
				t.Fatalf("replay variables=%v", report.FinalVariables)
			}
			var versions []string
			for _, event := range report.Events {
				if event.ActionName == "H1" || event.ActionName == "H2" {
					versions = append(versions, fmt.Sprint(event.ProtocolEvidence["http_version_negotiated"]))
				}
			}
			if strings.Join(versions, ",") != "1.1,2" {
				t.Fatalf("leaf receipts=%v", versions)
			}
		})
	}
}
