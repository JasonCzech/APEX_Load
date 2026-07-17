package browser

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	playwright "github.com/mxschmitt/playwright-go"
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

var activeRuntimeHARWorkingPaths sync.Map

func normalizedRuntimeHARWorkingPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

// RuntimeHARWorkingPathActive lets the controller retention sweep distinguish
// a live Playwright writer from an orphan left by a crashed/legacy session.
func RuntimeHARWorkingPathActive(path string) bool {
	_, ok := activeRuntimeHARWorkingPaths.Load(normalizedRuntimeHARWorkingPath(path))
	return ok
}

func registerRuntimeHARWorkingPath(path string) {
	activeRuntimeHARWorkingPaths.Store(normalizedRuntimeHARWorkingPath(path), struct{}{})
}

func unregisterRuntimeHARWorkingPath(path string) {
	activeRuntimeHARWorkingPaths.Delete(normalizedRuntimeHARWorkingPath(path))
}

func (b *Browser) discardHARWorkingFile() {
	workingPath := b.harWorkingPath
	if workingPath == "" {
		return
	}
	b.harWorkingPath = ""
	unregisterRuntimeHARWorkingPath(workingPath)
	_ = os.Remove(workingPath)
}

func purgeInactiveRuntimeHARWorkingFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasPrefix(name, "apexload_browser_") || !strings.HasSuffix(name, ".har.tmp") {
			continue
		}
		path := filepath.Join(dir, name)
		if RuntimeHARWorkingPathActive(path) {
			continue
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		_ = os.Remove(path)
	}
}

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
	if b.harCapturePolicy == HARCaptureOff || b.harCapturePolicy == "" || b.hasRuntimeSecretMaterial() {
		return
	}
	dir := b.harDir
	if dir == "" {
		dir = os.TempDir()
	}
	// Raw HARs are only working files. Clear legacy/crash leftovers before a
	// new session starts; active writers are registered and skipped.
	purgeInactiveRuntimeHARWorkingFiles(dir)
	b.harWorkingPath = filepath.Join(dir, fmt.Sprintf("apexload_browser_%d.har.tmp", time.Now().UnixNano()))
	registerRuntimeHARWorkingPath(b.harWorkingPath)
	contextOptions.RecordHarPath = playwright.String(b.harWorkingPath)
	contextOptions.RecordHarContent = playwright.HarContentPolicyEmbed
}

func (b *Browser) startHARMonitor() {
	if b.harWorkingPath == "" {
		return
	}
	b.harMonitorStop = make(chan struct{})
	b.harLimitExceeded = make(chan struct{})
	b.harLimitOnce = sync.Once{}
	b.harObservedBytes = 0
	b.harObservedEntries = 0
	b.harMonitorWG.Add(1)
	workingPath := b.harWorkingPath
	go func(stop, exceeded <-chan struct{}) {
		defer b.harMonitorWG.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-exceeded:
				b.closeOversizedHARContext()
				return
			case <-ticker.C:
				if info, err := os.Stat(workingPath); err == nil && info.Size() > maxRuntimeHARBytes {
					b.harLimitOnce.Do(func() { close(b.harLimitExceeded) })
				}
			}
		}
	}(b.harMonitorStop, b.harLimitExceeded)
}

func (b *Browser) closeOversizedHARContext() {
	b.mu.Lock()
	b.harErrorSeen = true
	context := b.pwContext
	b.pwContext = nil
	b.mu.Unlock()
	if context != nil {
		_ = context.Close()
	}
}

const maxRuntimeHAREntries = 100_000

func (b *Browser) noteHARRequest(request playwright.Request) {
	b.mu.Lock()
	active := b.harLimitExceeded != nil
	b.mu.Unlock()
	if !active {
		return
	}
	// Count entries synchronously in the Playwright callback so the separately
	// bounded diagnostics queue cannot bypass the entry limiter. The monitor
	// independently checks the actual HAR file size without calling Playwright.
	_ = request
	b.noteHARNetworkUsage(0)
}

func (b *Browser) noteHARNetworkUsage(bytes int64) {
	b.mu.Lock()
	if b.harLimitExceeded == nil {
		b.mu.Unlock()
		return
	}
	b.harObservedBytes += bytes
	b.harObservedEntries++
	exceeded := b.harObservedBytes > maxRuntimeHARBytes || b.harObservedEntries > maxRuntimeHAREntries
	signal := b.harLimitExceeded
	b.mu.Unlock()
	if exceeded {
		b.harLimitOnce.Do(func() { close(signal) })
	}
}

func (b *Browser) noteHARBytes(bytes int64) {
	if bytes <= 0 {
		return
	}
	b.mu.Lock()
	if b.harLimitExceeded == nil {
		b.mu.Unlock()
		return
	}
	b.harObservedBytes += bytes
	exceeded := b.harObservedBytes > maxRuntimeHARBytes
	signal := b.harLimitExceeded
	b.mu.Unlock()
	if exceeded {
		b.harLimitOnce.Do(func() { close(signal) })
	}
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
	defer unregisterRuntimeHARWorkingPath(workingPath)
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
	if redactor := b.currentRuntimeTextRedactor(); redactor != nil {
		scrubbed, err = redactRuntimeHARText(scrubbed, redactor)
		if err != nil {
			slog.Warn("browser HAR runtime secret redaction failed; discarding capture to avoid persisting secrets",
				"policy", b.harCapturePolicy, "error", err)
			return ""
		}
	}

	artifactPath := strings.TrimSuffix(workingPath, ".har.tmp") + "_" + scrub.SensitivityScrubbed + ".har"
	if err := os.WriteFile(artifactPath, scrubbed, 0o600); err != nil {
		slog.Warn("browser HAR artifact write failed", "path", artifactPath, "error", err)
		return ""
	}
	if err := enforceRuntimeHARRetention(filepath.Dir(artifactPath)); err != nil {
		slog.Warn("browser HAR artifact retention cleanup failed", "path", artifactPath, "error", err)
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

func (b *Browser) currentRuntimeTextRedactor() protocol.TextRedactor {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runtimeTextRedactor
}

func redactRuntimeHARText(data []byte, redactor protocol.TextRedactor) ([]byte, error) {
	if redactor == nil {
		return data, nil
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode scrubbed HAR: %w", err)
	}
	document = redactRuntimeHARNode(document, redactor)
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode scrubbed HAR: %w", err)
	}
	return encoded, nil
}

func redactRuntimeHARNode(value any, redactor protocol.TextRedactor) any {
	switch typed := value.(type) {
	case string:
		return redactor(typed)
	case []any:
		for index := range typed {
			typed[index] = redactRuntimeHARNode(typed[index], redactor)
		}
		return typed
	case map[string]any:
		for key, nested := range typed {
			typed[key] = redactRuntimeHARNode(nested, redactor)
		}
		return typed
	default:
		return value
	}
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
