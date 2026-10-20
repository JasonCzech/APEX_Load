package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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

// RuntimeHARWorkingPathActive is a process-local diagnostic hint only. It does
// not authorize retention to delete a raw file in any process.
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

// Runtime registrations are diagnostic only. Unknown legacy raw files have no
// cross-process writer proof and are deliberately never purged here.
func purgeInactiveRuntimeHARWorkingFiles(dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = ReclaimRuntimeHARWorking(ctx, dir, 32)
}

// ReclaimRuntimeHARWorking retires a HAR directory's working generations
// that are writer-joined or provably abandoned by a killed generator (see
// harAbandonmentProven), at most limit of them, and returns how many it
// retired. Everything else is preserved.
func ReclaimRuntimeHARWorking(ctx context.Context, dir string, limit int) (int, error) {
	plans, err := PlanRuntimeHARWorkingCleanupContext(ctx, dir, limit)
	reclaimed := 0
	for _, c := range plans {
		if ctx.Err() != nil {
			return reclaimed, errors.Join(err, ctx.Err())
		}
		if pruneErr := PruneRuntimeHARWorkingCandidate(ctx, c); pruneErr != nil {
			err = errors.Join(err, pruneErr)
			continue
		}
		reclaimed++
	}
	return reclaimed, err
}

func (b *Browser) discardHARWorkingFile() error {
	b.mu.Lock()
	owner := b.harOwner
	b.mu.Unlock()
	if owner == nil {
		return nil
	}
	owner.startFinish(false, nil)
	_, err := owner.waitFinish(context.Background())
	b.mu.Lock()
	if b.harOwner == owner && owner.retiredSuccessfully() {
		b.harOwner = nil
		b.harWorkingPath = ""
		unregisterRuntimeHARWorkingPath(owner.path)
	}
	b.mu.Unlock()
	browserEvidenceBudget.releaseHARWriter(b)
	return err
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
func (b *Browser) applyHARCaptureOptions(contextOptions *playwright.BrowserNewContextOptions) error {
	if b.harCapturePolicy == HARCaptureOff || b.harCapturePolicy == "" || b.hasRuntimeSecretMaterial() {
		return nil
	}
	if contextOptions == nil {
		return errors.New("browser HAR context options are required")
	}
	b.mu.Lock()
	previous := b.harOwner
	b.mu.Unlock()
	if previous != nil && !previous.retiredSuccessfully() {
		return errHAROwnershipUnproven
	}
	if !NativeHARSupported() {
		// As with an exhausted budget, the session runs on without HAR; the
		// platform's answer never changes, so it is logged once.
		b.mu.Lock()
		b.harUnavailableReason = harUnavailablePlatform
		b.mu.Unlock()
		harPlatformUnsupportedLogged.Do(func() {
			logEvidenceEvent("browser HAR capture is not supported on this platform; browser sessions run without HAR", "reason", harUnavailablePlatform, "platform", runtime.GOOS)
		})
		return nil
	}
	purgeInactiveRuntimeHARWorkingFiles(b.harDir)
	if reason := browserEvidenceBudget.reserveHARWriter(b, b.harDir); reason != "" {
		// HAR is optional evidence: record why it is missing and run on.
		b.mu.Lock()
		b.harUnavailableReason = reason
		b.mu.Unlock()
		logEvidenceEvent("browser HAR writer refused; the session runs without HAR", "reason", reason)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	defer cancel()
	owner, err := newHARCaptureOwner(ctx, b.harDir)
	if err != nil {
		browserEvidenceBudget.releaseHARWriter(b)
		return err
	}
	b.mu.Lock()
	b.harOwner = owner
	b.harWorkingPath = owner.path
	b.mu.Unlock()
	registerRuntimeHARWorkingPath(owner.path)
	contextOptions.RecordHarPath = playwright.String(owner.path)
	contextOptions.RecordHarContent = playwright.HarContentPolicyEmbed
	return nil
}

func (b *Browser) startHARMonitor() {
	if b.harWorkingPath == "" {
		return
	}
	b.mu.Lock()
	b.harMonitorStop = make(chan struct{})
	b.harLimitExceeded = make(chan struct{})
	b.harLimitOnce = sync.Once{}
	b.harObservedBytes = 0
	b.harObservedEntries = 0
	b.harInFlightExceeded = false
	b.harMonitorWG.Add(1)
	workingPath := b.harWorkingPath
	owner := b.harOwner
	stop, exceeded, generation := b.harMonitorStop, b.harLimitExceeded, b.evidence.generation
	b.mu.Unlock()
	go func(stop, exceeded <-chan struct{}) {
		defer b.harMonitorWG.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-exceeded:
				b.closeOwnedOversizedHARContext(owner, generation)
				return
			case <-ticker.C:
				if info, err := os.Stat(workingPath); err == nil && info.Size() > maxRuntimeHARBytes {
					b.mu.Lock()
					if generation == b.evidence.generation && owner == b.harOwner && stop == b.harMonitorStop && b.harLimitExceeded != nil {
						b.harLimitOnce.Do(func() { close(b.harLimitExceeded) })
					}
					b.mu.Unlock()
				}
			}
		}
	}(stop, exceeded)
}

func (b *Browser) closeOversizedHARContext(expectedGeneration ...string) {
	b.mu.Lock()
	owner := b.harOwner
	b.mu.Unlock()
	b.closeOwnedOversizedHARContext(owner, expectedGeneration...)
}

func (b *Browser) closeOwnedOversizedHARContext(owner *harCaptureOwner, expectedGeneration ...string) {
	b.mu.Lock()
	if owner != b.harOwner || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation {
		b.mu.Unlock()
		return
	}
	b.harErrorSeen = true
	overflow := b.resourceLimit == ""
	if overflow {
		b.resourceLimit = resourceLimitHARCapture
		if b.harInFlightExceeded {
			b.resourceLimit = resourceLimitHARInFlight
		}
	}
	reason := b.resourceLimit
	browserContext := b.pwContext
	if owner == nil {
		b.pwContext = nil
	}
	b.mu.Unlock()
	if overflow {
		browserEvidenceBudget.counters.harOverflow.Add(1)
		logEvidenceEvent("browser HAR recording closed at its resource limit", "reason", reason)
	}
	// Unfinished observations end at the limit, not at an ordinary close.
	b.censorDocumentEvidence(nil, "resource_limit")
	if owner != nil {
		owner.startClose()
	} else if browserContext != nil {
		_ = browserContext.Close()
	}
}

const maxRuntimeHAREntries = 100_000

// Outstanding issued requests include browser-queued work; the limit protects
// native HAR buffering before completion or file growth can reveal its size.
const maxRuntimeHARInFlightRequests = 256

func (b *Browser) noteHARRequest(request playwright.Request, expectedGeneration ...string) {
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
	b.noteHARNetworkUsage(0, expectedGeneration...)
}

func (b *Browser) noteHARNetworkUsage(bytes int64, expectedGeneration ...string) {
	b.mu.Lock()
	if b.harLimitExceeded == nil || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation {
		b.mu.Unlock()
		return
	}
	b.harObservedBytes += bytes
	b.harObservedEntries++
	exceeded := b.harObservedBytes > maxRuntimeHARBytes || b.harObservedEntries > maxRuntimeHAREntries
	if exceeded {
		b.harLimitOnce.Do(func() { close(b.harLimitExceeded) })
	}
	b.mu.Unlock()
}

func (b *Browser) noteHARBytes(bytes int64, expectedGeneration ...string) {
	if bytes <= 0 {
		return
	}
	b.mu.Lock()
	if b.harLimitExceeded == nil || len(expectedGeneration) > 0 && expectedGeneration[0] != b.evidence.generation {
		b.mu.Unlock()
		return
	}
	b.harObservedBytes += bytes
	exceeded := b.harObservedBytes > maxRuntimeHARBytes
	if exceeded {
		b.harLimitOnce.Do(func() { close(b.harLimitExceeded) })
	}
	b.mu.Unlock()
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
	path, _ := b.finalizeHARCaptureContext(context.Background())
	return path
}

func (b *Browser) finalizeHARCaptureContext(ctx context.Context) (string, error) {
	b.mu.Lock()
	owner := b.harOwner
	if owner == nil {
		b.mu.Unlock()
		return "", nil
	}
	keep := b.harCapturePolicy == HARCaptureAlways || b.harCapturePolicy == HARCaptureOnError && b.harErrorSeen || b.harCapturePolicy == HARCaptureOnSLABreach && b.harSLABreachSeen || b.harCapturePolicy == HARCaptureManual && b.harManualSeen
	redactor := b.runtimeTextRedactor
	b.mu.Unlock()
	owner.startFinish(keep, redactor)
	path, err := owner.waitFinish(ctx)
	b.mu.Lock()
	if b.harOwner == owner && owner.retiredSuccessfully() {
		b.harOwner = nil
		b.harWorkingPath = ""
		b.harErrorSeen = false
		b.harSLABreachSeen = false
		b.harManualSeen = false
		unregisterRuntimeHARWorkingPath(owner.path)
	}
	b.mu.Unlock()
	return path, err
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

// TakeBrowserHARArtifacts hands over the HAR artifacts this session published
// since the last call. The engine transfers them into the run archive.
func (b *Browser) TakeBrowserHARArtifacts() []protocol.BrowserHARArtifact {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.publishedHARArtifacts
	b.publishedHARArtifacts = nil
	return out
}

// ReadRuntimeHARArtifact streams a published scrubbed HAR artifact to
// consume and returns the file's identity, so a caller that copies it can
// retire it later (RetireRuntimeHARArtifact) once the copy is final. Nothing
// in the directory is changed. The namespace lease is held only to open the
// file and prove it is the published artifact: a published HAR never
// changes, so the copy streams from the open descriptor without holding up
// the directory's other sessions (whose closes wait on the lease).
func ReadRuntimeHARArtifact(ctx context.Context, path string, consume func(r io.Reader, size int64) error) (os.FileInfo, error) {
	name := filepath.Base(path)
	if consume == nil || !isRetainedRuntimeHARArtifact(name) || name != filepath.Base(filepath.Clean(path)) {
		return nil, errHAROwnershipUnproven
	}
	var identity os.FileInfo
	var file *os.File
	err := WithRuntimeHARArtifactNamespace(ctx, filepath.Dir(path), func(root *os.Root) error {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errHAROwnershipUnproven
		}
		opened, err := root.Open(name)
		if err != nil {
			return err
		}
		stat, statErr := opened.Stat()
		if statErr != nil || !os.SameFile(info, stat) {
			_ = opened.Close()
			return errors.Join(statErr, errHAROwnershipUnproven)
		}
		file, identity = opened, info
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := errors.Join(consume(file, identity.Size()), file.Close()); err != nil {
		return nil, err
	}
	return identity, nil
}

// RetireRuntimeHARArtifact retires a published HAR artifact read earlier,
// under the namespace lease, only while it is still that same file.
func RetireRuntimeHARArtifact(ctx context.Context, path string, identity os.FileInfo) error {
	name := filepath.Base(path)
	if identity == nil || !isRetainedRuntimeHARArtifact(name) || name != filepath.Base(filepath.Clean(path)) {
		return errHAROwnershipUnproven
	}
	return WithRuntimeHARArtifactNamespace(ctx, filepath.Dir(path), func(root *os.Root) error {
		return RemoveRuntimeHARArtifactAt(root, name, identity)
	})
}

// TakeRuntimeHARArtifact streams a published scrubbed HAR artifact to
// consume and, once consume succeeds, retires it from its HAR directory
// under the namespace lease. The artifact must still be the same published
// file; nothing else in the directory is touched.
func TakeRuntimeHARArtifact(ctx context.Context, path string, consume func(r io.Reader, size int64) error) error {
	name := filepath.Base(path)
	if consume == nil || !isRetainedRuntimeHARArtifact(name) || name != filepath.Base(filepath.Clean(path)) {
		return errHAROwnershipUnproven
	}
	return WithRuntimeHARArtifactNamespace(ctx, filepath.Dir(path), func(root *os.Root) error {
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errHAROwnershipUnproven
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !os.SameFile(info, opened) {
			_ = file.Close()
			return errors.Join(statErr, errHAROwnershipUnproven)
		}
		consumeErr := consume(file, info.Size())
		if err := errors.Join(consumeErr, file.Close()); err != nil {
			return err
		}
		return RemoveRuntimeHARArtifactAt(root, name, info)
	})
}
