package java_http

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The preflight request snapshot is the authored request: with
// config.log.redact_run_evidence off its credential-named headers and
// body-shape heuristics no longer mask authored literals; exact platform values
// still do.
func TestJavaHTTPPreflightSnapshotFollowsRunEvidenceSetting(t *testing.T) {
	const (
		literal  = "lit-java-pwd-8001"
		platform = "platform-java-val-8002"
	)
	exact := func(value string) string { return strings.ReplaceAll(value, platform, "[REDACTED]") }
	body := base64.StdEncoding.EncodeToString([]byte(`{"password":"` + literal + `"}`))
	for _, mode := range []struct {
		name   string
		retain bool
	}{
		{name: "redaction_on", retain: false},
		{name: "redaction_off", retain: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			j := &JavaHTTP{}
			if err := j.Init(context.Background(), map[string]any{protocol.RedactAuthoredEvidenceConfigKey: !mode.retain}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = j.Teardown(context.Background()) })
			j.SetRuntimeTextRedactor(func(value string) string { return strings.ReplaceAll(exact(value), "password", "[REDACTED]") })
			j.SetRuntimeAuthoredTextRedactor(exact)
			snap := j.buildPreflightSnapshot(&protocol.Action{
				Type: "POST", Target: "https://svc.example.test/rpc",
				Headers: map[string]string{"Authorization": "Basic " + literal, "X-Api-Key": platform},
				Body:    []byte(body),
			})
			if snap.RequestHeaders["X-Api-Key"] != "[REDACTED]" {
				t.Fatalf("platform-managed header = %q", snap.RequestHeaders["X-Api-Key"])
			}
			if strings.Contains(snap.RequestHeaders["Authorization"], literal) != mode.retain {
				t.Fatalf("Authorization = %q, want visible=%v", snap.RequestHeaders["Authorization"], mode.retain)
			}
			if (snap.RequestBody == body) != mode.retain {
				t.Fatalf("request body = %q, want authored=%v", snap.RequestBody, mode.retain)
			}
			if !mode.retain {
				return
			}
			platformBody := base64.StdEncoding.EncodeToString([]byte("k=" + platform))
			if !j.requestPayloadContainsSecret([]byte("k="+platform), platformBody) {
				t.Fatal("a platform-managed value in an authored payload must still withhold it")
			}
		})
	}
}
