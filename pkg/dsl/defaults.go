package dsl

// DefaultScriptConfig returns a ScriptConfig populated with the load engine's
// built-in default values — the values the runtime applies when a script does not
// set a field. The dashboard's per-group runtime editor uses this (served via
// GET /api/v1/dsl/config-defaults) as the lowest "inherited" layer so it can show
// the actual effective value — never a blank placeholder — for fields a script
// leaves unset.
//
// These mirror the defaults applied at runtime in pkg/protocol/http/config.go,
// pkg/protocol/browser/config.go, and pkg/engine (pacing.go, vuser*.go). Keep them
// in sync with those sources. Fields whose default is "behavioral" rather than a
// concrete value (e.g. pacing/think-time unset = none, run_logic.iterations 0 =
// duration-driven) are left at their zero/none value; the dashboard renders those
// with a descriptive label.
func DefaultScriptConfig() ScriptConfig {
	return ScriptConfig{
		ThinkTime: ThinkTimeConfig{Type: "none"}, // no scripted think time by default
		Pacing:    PacingConfig{Type: "none"},    // duration-driven, no inter-iteration pacing
		HTTP: HTTPConfig{
			MaxConnsPerHost:  100,
			MaxIdleConns:     4,
			KeepAlive:        defaultBoolPtr(true),
			ConnectTimeoutMs: 30000,
			RequestTimeoutMs: 60000,
			TLSTimeoutMs:     10000,
			IdleTimeoutMs:    90000,
			TLS:              TLSConfig{MinVersion: "1.2"},
			MaxRedirects:     defaultIntPtr(10),
			CookiesEnabled:   defaultBoolPtr(true),
		},
		Browser: BrowserConfig{
			Engine:            "chromium",
			Headless:          defaultBoolPtr(true),
			Orientation:       "landscape",
			Viewport:          BrowserViewport{Width: 1440, Height: 900},
			CacheEnabled:      defaultBoolPtr(true),
			CacheContentURLs:  defaultBoolPtr(true),
			DownloadResources: "all",
		},
		// BrowserEmulation stays nil by design: nil means "inherit the http.*
		// fields" (user_agent, accept_language, cache_enabled). The config-defaults
		// payload therefore intentionally has no browser_emulation block.
		RunLogic: RunLogicConfig{
			Iterations:       0, // 0 = run until the test duration expires (duration-driven)
			WorkloadModel:    "closed",
			AutoTransactions: "none",
		},
		ErrorHandling: ErrorHandlingConfig{
			ContinueOnError: false, // stop the iteration on the first error
			MaxRetries:      0,
			RetryDelayMs:    1000,
			RetryBackoff:    "fixed",
		},
		Log: LogConfig{Level: "standard", RedactRunEvidence: defaultBoolPtr(true)},
	}
}

func defaultBoolPtr(b bool) *bool { return &b }
func defaultIntPtr(i int) *int    { return &i }
