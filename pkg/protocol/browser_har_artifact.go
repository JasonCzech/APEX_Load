package protocol

// MaxBrowserHARArtifactsPerRun bounds the native HAR artifacts one run
// transfers, matching the per-run HAR artifact limit.
const MaxBrowserHARArtifactsPerRun = 128

// Reasons a run archive omits a native HAR, counted in its manifest. The
// generator and the controller (remote transfer or its own runs) share them.
const (
	BrowserHAROmittedArtifactLimit     = "artifact_limit"
	BrowserHAROmittedTooLarge          = "artifact_too_large"
	BrowserHAROmittedRunBytes          = "run_byte_limit"
	BrowserHAROmittedArchiveQuota      = "archive_quota"
	BrowserHAROmittedTransfer          = "transfer_failed"
	BrowserHAROmittedInvalid           = "invalid_artifact"
	BrowserHAROmittedDuplicate         = "duplicate"
	BrowserHAROmittedSourceUnavailable = "source_unavailable"
	// BrowserHAROmittedSafeExport: a safe archive export leaves HARs out.
	BrowserHAROmittedSafeExport = "safe_export"
	BrowserHAROmittedReceipt           = "receipt_refused"
)

// BrowserHARArtifact is a completed, scrubbed native HAR that a browser
// session published into its HAR directory: the artifact's path and the
// working generation that recorded it.
type BrowserHARArtifact struct {
	Path       string
	Generation string
}

// BrowserHARArtifactProvider hands over each HAR artifact a session
// published, once, after the session that recorded it closed.
type BrowserHARArtifactProvider interface {
	TakeBrowserHARArtifacts() []BrowserHARArtifact
}
