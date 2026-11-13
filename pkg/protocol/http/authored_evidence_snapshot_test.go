package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The request snapshot's credential-named headers follow
// config.log.redact_run_evidence (protocol.RedactAuthoredEvidenceConfigKey): off
// keeps the authored value (the engine's exact matcher handles platform-managed
// values), on or unset masks it as before. Response headers keep the name mask.
func TestHTTPSnapshotAuthoredRequestHeadersFollowRunEvidenceSetting(t *testing.T) {
	const literal = "Bearer lit-http-tok-7901"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "sid=server-sid-7902")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "fail")
	}))
	defer server.Close()
	for _, mode := range []struct {
		name    string
		config  map[string]any
		visible bool
	}{
		{name: "redaction_off", config: map[string]any{protocol.RedactAuthoredEvidenceConfigKey: false}, visible: true},
		{name: "redaction_on", config: map[string]any{protocol.RedactAuthoredEvidenceConfigKey: true}, visible: false},
		{name: "direct_caller_without_key", config: map[string]any{}, visible: false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			mode.config["base_url"] = server.URL
			h := &HTTP{}
			if err := h.Init(context.Background(), mode.config); err != nil {
				t.Fatal(err)
			}
			result, err := h.Execute(context.Background(), &protocol.Action{
				Name: "login", Type: "POST", Target: server.URL + "/login",
				Headers:         map[string]string{"Authorization": literal, "X-Platform": "[REDACTED]"},
				RedactedHeaders: map[string]string{"Authorization": literal, "X-Platform": "[REDACTED]"},
				Body:            []byte("{}"),
			})
			if err != nil || result.Snapshot == nil {
				t.Fatalf("result %+v err %v", result, err)
			}
			if got := result.Snapshot.RequestHeaders["Authorization"]; (got == literal) != mode.visible {
				t.Fatalf("Authorization = %q, want visible=%v", got, mode.visible)
			}
			if got := result.Snapshot.RequestHeaders["X-Platform"]; got != "[REDACTED]" {
				t.Fatalf("platform-managed placeholder = %q", got)
			}
			if got := result.Snapshot.ResponseHeaders["Set-Cookie"]; strings.Contains(got, "server-sid-7902") {
				t.Fatalf("response Set-Cookie lost its mask: %q", got)
			}
		})
	}
}
