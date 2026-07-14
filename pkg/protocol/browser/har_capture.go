package browser

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	playwright "github.com/playwright-community/playwright-go"
)

// HAR capture policies for runtime browser sessions (experience canaries and
// browser VUsers). The session's network traffic is recorded by the context
// and persisted on close according to the policy, always passing through
// scrub-on-write first.
const (
	maxRuntimeHARBytes    = 100 << 20
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
	if b.harCapturePolicy == HARCaptureOff || b.harCapturePolicy == "" {
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

func (b *Browser) startHARMonitor() {
	if b.harWorkingPath == "" {
		return
	}
	b.harMonitorStop = make(chan struct{})
	b.harMonitorWG.Add(1)
	go func(path string, stop <-chan struct{}) {
		defer b.harMonitorWG.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				info, err := os.Stat(path)
				if err != nil || info.Size() <= maxRuntimeHARBytes {
					continue
				}
				b.mu.Lock()
				b.harErrorSeen = true
				context := b.pwContext
				b.mu.Unlock()
				if context != nil {
					_ = context.Close()
				}
				return
			}
		}
	}(b.harWorkingPath, b.harMonitorStop)
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

func (b *Browser) noteHARManual() {
	if b.harCapturePolicy == HARCaptureManual {
		b.mu.Lock()
		b.harManualSeen = true
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
	manualSeen := b.harManualSeen
	b.harErrorSeen = false
	b.harSLABreachSeen = false
	b.harManualSeen = false
	b.mu.Unlock()

	if b.harCapturePolicy == HARCaptureOnError && !errorSeen {
		_ = os.Remove(workingPath)
		return ""
	}
	if b.harCapturePolicy == HARCaptureOnSLABreach && !slaBreachSeen {
		_ = os.Remove(workingPath)
		return ""
	}
	if b.harCapturePolicy == HARCaptureManual && !manualSeen {
		_ = os.Remove(workingPath)
		return ""
	}

	info, err := os.Stat(workingPath)
	if err != nil {
		slog.Warn("browser HAR capture unavailable", "policy", b.harCapturePolicy, "error", err)
		return ""
	}
	if info.Size() > maxRuntimeHARBytes {
		slog.Warn("browser HAR capture exceeded size limit", "policy", b.harCapturePolicy, "bytes", info.Size())
		_ = os.Remove(workingPath)
		return ""
	}
	f, err := os.Open(workingPath)
	if err != nil {
		slog.Warn("browser HAR capture unavailable", "policy", b.harCapturePolicy, "error", err)
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRuntimeHARBytes+1))
	_ = f.Close()
	if err != nil || int64(len(data)) > maxRuntimeHARBytes {
		slog.Warn("browser HAR capture exceeded size limit", "policy", b.harCapturePolicy)
		_ = os.Remove(workingPath)
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
		"sla_breach_seen", slaBreachSeen,
		"manual_seen", manualSeen)
	return artifactPath
}

func browserActionRequestsHARCapture(action *protocol.Action) bool {
	if action == nil || action.Params == nil {
		return false
	}
	for _, key := range []string{"capture_har", "har_capture", "save_har"} {
		switch value := action.Params[key].(type) {
		case bool:
			if value {
				return true
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true", "yes", "manual", "capture", "save":
				return true
			}
		}
	}
	return false
}
