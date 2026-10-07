package protocol

// MaxBrowserHARArtifactsPerRun bounds the native HAR artifacts one run
// transfers, matching the per-run HAR artifact limit.
const MaxBrowserHARArtifactsPerRun = 128

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
