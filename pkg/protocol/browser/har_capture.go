package browser

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
	playwright "github.com/playwright-community/playwright-go"
)

// HAR capture policies for runtime browser sessions (experience canaries and
// browser VUsers). The session's network traffic is recorded by the context
// and persisted on close according to the policy, always passing through
// scrub-on-write first.
const (
	HARCaptureOff         = "off"
	HARCaptureOnError     = "on_error"
	HARCaptureOnSLABreach = "on_sla_breach"
	HARCaptureManual      = "manual"
	HARCaptureAlways      = "always"
)

func normalizeHARCapturePolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case HARCaptureOnError:
		return HARCaptureOnError
	case HARCaptureOnSLABreach:
		return HARCaptureOnSLABreach
	case HARCaptureManual:
		return HARCaptureManual
	case HARCaptureAlways:
		return HARCaptureAlways
	default:
		return HARCaptureOff
	}
}

// applyHARCaptureOptions enables context-level HAR recording into a working
// file when the policy requires it. The recording always runs for on_error so
// the evidence exists by the time the error happens; the file is discarded at
// close when no error occurred.
func (b *Browser) applyHARCaptureOptions(contextOptions *playwright.BrowserNewContextOptions) {
	if b.harCapturePolicy == HARCaptureOff || b.harCapturePolicy == HARCaptureManual || b.harCapturePolicy == "" {
		return
	}
	dir := b.harDir
	if dir == "" {
		dir = os.TempDir()
	}
	b.harWorkingPath = filepath.Join(dir, fmt.Sprintf("apexload_browser_%d.har.tmp", time.Now().UnixNano()))
	contextOptions.RecordHarPath = playwright.String(b.harWorkingPath)
	contextOptions.RecordHarContent = playwright.HarContentPolicyEmbed
}

// noteHARError marks the session so an on_error policy keeps the recording.
func (b *Browser) noteHARError() {
	if b.harCapturePolicy == HARCaptureOnError {
		b.mu.Lock()
		b.harErrorSeen = true
		b.mu.Unlock()
	}
}

func (b *Browser) noteHARSLABreach() {
	if b.harCapturePolicy == HARCaptureOnSLABreach {
		b.mu.Lock()
		b.harSLABreachSeen = true
		b.mu.Unlock()
	}
}

// finalizeHARCapture runs after the context closed (which flushes the working
// HAR): it discards the recording when the policy does not require it, and
// otherwise scrubs it and persists the artifact with a sensitivity-labeled
// name. Returns the artifact path when one was kept.
func (b *Browser) finalizeHARCapture() string {
	workingPath := b.harWorkingPath
	if workingPath == "" {
		return ""
	}
	b.harWorkingPath = ""

	b.mu.Lock()
	errorSeen := b.harErrorSeen
	slaBreachSeen := b.harSLABreachSeen
	b.harErrorSeen = false
	b.harSLABreachSeen = false
	b.mu.Unlock()

	if b.harCapturePolicy == HARCaptureOnError && !errorSeen {
		_ = os.Remove(workingPath)
		return ""
	}
	if b.harCapturePolicy == HARCaptureOnSLABreach && !slaBreachSeen {
		_ = os.Remove(workingPath)
		return ""
	}

	data, err := os.ReadFile(workingPath)
	if err != nil {
		slog.Warn("browser HAR capture unavailable", "policy", b.harCapturePolicy, "error", err)
		return ""
	}
	_ = os.Remove(workingPath)

	scrubbed, err := scrub.HAR(data, scrub.Policy{})
	if err != nil {
		slog.Warn("browser HAR scrub-on-write failed; discarding capture to avoid persisting secrets",
			"policy", b.harCapturePolicy, "error", err)
		return ""
	}

	artifactPath := strings.TrimSuffix(workingPath, ".har.tmp") + "_" + scrub.SensitivityScrubbed + ".har"
	if err := os.WriteFile(artifactPath, scrubbed, 0o600); err != nil {
		slog.Warn("browser HAR artifact write failed", "path", artifactPath, "error", err)
		return ""
	}
	slog.Info("browser HAR artifact captured",
		"policy", b.harCapturePolicy,
		"path", artifactPath,
		"sensitivity", scrub.SensitivityScrubbed,
		"error_seen", errorSeen,
		"sla_breach_seen", slaBreachSeen)
	return artifactPath
}
