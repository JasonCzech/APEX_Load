package engine

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestErrorContextBufferSuppressionEvictionRedactionAndRelease(t *testing.T) {
	baseline := errorLogProcessBytes.Load()
	var output bytes.Buffer
	b := &errorLogBuffer{limit: 1200}
	defer b.close()
	h := &errorLogHandler{next: slog.NewTextHandler(&output, nil), buffer: b, redact: func(s string) string { return strings.ReplaceAll(s, "credential", "[REDACTED]") }}
	logger := slog.New(h)
	for i := 0; i < 20; i++ {
		logger.Info("old history", "payload", strings.Repeat("x", 100))
	}
	logger.Info("recent credential", "secret", "credential")
	if output.Len() != 0 {
		t.Fatal("successful diagnostics emitted before failure")
	}
	if b.bytes > b.limit || errorLogProcessBytes.Load()-baseline > int64(b.limit) {
		t.Fatal("memory limit exceeded")
	}
	logger.Error("failure credential")
	if strings.Contains(output.String(), "credential") || !strings.Contains(output.String(), "recent [REDACTED]") || !strings.Contains(output.String(), "failure [REDACTED]") {
		t.Fatalf("bad output: %s", &output)
	}
	if strings.Count(output.String(), "old history") >= 20 {
		t.Fatal("history not evicted")
	}
	if b.bytes != 0 || errorLogProcessBytes.Load() != baseline {
		t.Fatal("flush retained memory")
	}
	output.Reset()
	logger.Error("second failure")
	if strings.Contains(output.String(), "recent") {
		t.Fatal("history flushed twice")
	}
	logger.Info("retained")
	b.close()
	if errorLogProcessBytes.Load() != baseline {
		t.Fatal("close leaked reservation")
	}
}
func TestErrorContextProcessBudget(t *testing.T) {
	baseline := errorLogProcessBytes.Load()
	errorLogProcessBytes.Store(maxErrorLogProcessBytes)
	defer errorLogProcessBytes.Store(baseline)
	b := &errorLogBuffer{limit: 1024}
	var out bytes.Buffer
	h := &errorLogHandler{next: slog.NewTextHandler(&out, nil), buffer: b, redact: func(s string) string { return s }}
	l := slog.New(h)
	l.Info("must not retain")
	if b.bytes != 0 {
		t.Fatal("process budget ignored")
	}
	l.Error("still report failure")
	if !strings.Contains(out.String(), "still report failure") {
		t.Fatal("failure suppressed at memory limit")
	}
}
