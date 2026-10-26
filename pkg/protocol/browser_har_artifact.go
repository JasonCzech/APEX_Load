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
	BrowserHAROmittedReceipt    = "receipt_refused"
)

// BrowserHARArtifact is a completed, scrubbed native HAR that a browser
// session published into its HAR directory: the artifact's path, the
// working generation that recorded it and the browser evidence generation
// of the context it recorded (the SourceGeneration of that context's
// resource evidence), which tells a synthetic HAR which entries a native one
// already holds.
type BrowserHARArtifact struct {
	Path               string
	Generation         string
	EvidenceGeneration string
}

// BrowserHARArtifactProvider hands over each HAR artifact a session
// published, once, after the session that recorded it closed.
type BrowserHARArtifactProvider interface {
	TakeBrowserHARArtifacts() []BrowserHARArtifact
}

// BrowserNativeHARSupportProvider reports whether a browser runtime instance
// can own and publish native HAR capture on this platform.
type BrowserNativeHARSupportProvider interface {
	SupportsBrowserNativeHAR() bool
}
