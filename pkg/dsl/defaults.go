package dsl

import (
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// DefaultScriptConfig returns a ScriptConfig populated with the load engine's
// built-in default values — the values the runtime applies when a script does not
// set a field. The dashboard's per-group runtime editor uses this (served via
// GET /api/v1/dsl/config-defaults) as the lowest "inherited" layer so it can show
// the actual effective value — never a blank placeholder — for fields a script
// leaves unset.
//
// HTTP and browser defaults come from the same pure contracts as their runtimes.
// Engine pacing defaults remain defined by pkg/engine (pacing.go, vuser*.go). Fields whose default is "behavioral" rather than a
// concrete value (e.g. pacing/think-time unset = none, run_logic.iterations 0 =
// duration-driven) are left at their zero/none value; the dashboard renders those
// with a descriptive label.
func DefaultScriptConfig() ScriptConfig {
	return ScriptConfig{
		ThinkTime: ThinkTimeConfig{Type: "none"}, // no scripted think time by default
		Pacing:    PacingConfig{Type: "none"},    // duration-driven, no inter-iteration pacing
		HTTP: HTTPConfig{
			MaxConnsPerHost:  httpcontract.DefaultMaxConnsPerHost,
			MaxIdleConns:     httpcontract.DefaultMaxIdleConns,
			KeepAlive:        defaultBoolPtr(httpcontract.DefaultKeepAliveEnabled),
			ConnectTimeoutMs: int(httpcontract.DefaultConnectTimeout / time.Millisecond),
			RequestTimeoutMs: int(httpcontract.DefaultRequestTimeout / time.Millisecond),
			TLSTimeoutMs:     int(httpcontract.DefaultTLSTimeout / time.Millisecond),
			IdleTimeoutMs:    int(httpcontract.DefaultIdleTimeout / time.Millisecond),
			TLS:              TLSConfig{MinVersion: httpcontract.DefaultTLSMinVersion},
			MaxRedirects:     defaultIntPtr(httpcontract.DefaultMaxRedirects),
			CookiesEnabled:   defaultBoolPtr(httpcontract.DefaultCookiesEnabled),
		},
		Browser: BrowserConfig{
			Engine:            browsercontract.DefaultEngine,
			Headless:          defaultBoolPtr(browsercontract.DefaultHeadless),
			Orientation:       browsercontract.DefaultOrientation,
			Viewport:          BrowserViewport{Width: browsercontract.DefaultViewportWidth, Height: browsercontract.DefaultViewportHeight},
			CacheEnabled:      defaultBoolPtr(browsercontract.DefaultCacheEnabled),
			CacheContentURLs:  defaultBoolPtr(browsercontract.DefaultCacheContentURLs),
			DownloadResources: browsercontract.DefaultDownloadResources,
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
			// ContinueOnError stays unset on purpose. The engine's own default is
			// stop-on-error, but that is a behavioral default, not a value this
			// layer may state: callers materialize this config as the base layer of
			// "apply runtime settings to groups", so an explicit false here would be
			// copied onto target groups as a real opt-out and would silently defeat
			// the scenario's enabled-by-default continue-on-error policy. Surfaces
			// that need a concrete value to display resolve the scenario policy
			// themselves.
			MaxRetries:   0,
			RetryDelayMs: 1000,
			RetryBackoff: "fixed",
		},
		Log: LogConfig{Level: "standard", RedactRunEvidence: defaultBoolPtr(false)},
	}
}

func defaultBoolPtr(b bool) *bool { return &b }
func defaultIntPtr(i int) *int    { return &i }
