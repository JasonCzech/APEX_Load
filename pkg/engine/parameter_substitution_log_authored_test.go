package engine

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// The parameter_substitution action log's target and request body are the
// authored request: they follow config.log.redact_run_evidence through both
// VUser log handlers. data_returned (the response body) keeps the full scrub and
// a platform-managed value is masked in both modes.
func TestParameterSubstitutionLogFollowsRunEvidenceSetting(t *testing.T) {
	const responseSecret = "resp-log-cs-9402"
	action := &protocol.Action{
		Target: "https://api.example.test/login?password=" + authoredLiteralPassword,
		Body:   []byte(`{"client_secret":"` + authoredLiteralSecret + `","platform":"` + authoredPlatformSecret + `"}`),
	}
	result := &protocol.Result{StatusCode: 500, ErrorMessage: "failed", ResponseBody: `{"client_secret":"` + responseSecret + `"}`}
	logCfg := dsl.LogConfig{ExtendedOptions: []string{"parameter_substitution", "data_returned"}}
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			config := authoredEvidenceRootConfig(t, mode.redact)
			vu := &VUser{RedactionConfig: config, errorLogs: &errorLogBuffer{limit: 1 << 20}}
			defer vu.errorLogs.close()
			var publication, buffered bytes.Buffer
			slog.New(&runtimePublicationLogHandler{next: slog.NewTextHandler(&publication, nil), vu: vu}).
				Error("action failed", actionLogAttrs("login", action, result, logCfg, true, config)...)
			errorLogger := slog.New(&errorLogHandler{
				next: slog.NewTextHandler(&buffered, nil), buffer: vu.errorLogs,
				redact:         func(s string) string { return scrubSensitiveTextWithConfig(s, config) },
				redactAuthored: func(s string) string { return scrubAuthoredTextWithConfig(s, config) },
			})
			// A buffered (info) record is re-projected when the error flushes it.
			errorLogger.Info("action ok", actionLogAttrs("login", action, result, logCfg, false, config)...)
			errorLogger.Error("action failed", actionLogAttrs("login", action, result, logCfg, true, config)...)
			for label, output := range map[string]string{"publication": publication.String(), "error buffer": buffered.String()} {
				if !strings.Contains(output, "request_body") {
					t.Fatalf("%s: no parameter_substitution attrs: %s", label, output)
				}
				for _, value := range []string{authoredPlatformSecret, responseSecret} {
					if strings.Contains(output, value) {
						t.Fatalf("%s: platform/response value %q logged: %s", label, value, output)
					}
				}
				for _, value := range []string{authoredLiteralPassword, authoredLiteralSecret} {
					if strings.Contains(output, value) == mode.redact {
						t.Fatalf("%s: authored %q visible=%v, want %v: %s", label, value, !mode.redact, !mode.redact, output)
					}
				}
			}
			if strings.Count(buffered.String(), authoredLiteralSecret) != 2 && !mode.redact {
				t.Fatalf("flushed buffered record lost the authored value: %s", buffered.String())
			}
		})
	}
}
