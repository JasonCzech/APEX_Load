package recordbridge

import (
	"context"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type BrowserReplayStartRequest struct {
	// BrowserAuthoredDefinition is set by the host before runtime overlays.
	BrowserAuthoredDefinition protocol.BrowserAuthoredDefinition `json:"-"`
	LiveTableSeeds            []engine.LiveTableSandboxSeed      `json:"live_table_seeds,omitempty"`
	GroupName                 string                             `json:"group_name,omitempty"`
	ProjectID                 string                             `json:"project_id,omitempty"`
	ReplaySessionID           string                             `json:"replay_session_id,omitempty"`
	Variables                 map[string]string                  `json:"variables,omitempty"`
	RedactionRules            []engine.RedactionRule             `json:"redaction_rules,omitempty"`
	StartPaused               bool                               `json:"start_paused,omitempty"`
	Breakpoints               []int                              `json:"breakpoints,omitempty"`
	Browser                   *dsl.BrowserConfig                 `json:"browser,omitempty"`
	IgnoreThinkTime           *bool                              `json:"ignore_think_time,omitempty"`
	RunID                     string                             `json:"run_id,omitempty"`
	ContinueOnError           *bool                              `json:"continue_on_error,omitempty"`
	// RecordHAR records a native HAR of the replay. It is opt-in: a
	// recording runs under the HAR size, memory and owner limits, which close
	// the browser context, so an unrequested one could fail a replay that
	// would otherwise pass. Without it the replay serves the native HAR its
	// script's own capture policy recorded, if any, or else the synthetic HAR
	// built from the report's network entries. Where native HAR capture is
	// not supported (browser.NativeHARSupported) it is refused with 422.
	RecordHAR               *bool                         `json:"record_har,omitempty"`
	Script                  *dsl.Script                   `json:"script,omitempty"`
	Preflight               *importreview.ReplayPreflight `json:"-"`
	SecretResolver          engine.SecretResolver         `json:"-"`
	ExecutionContext        context.Context               `json:"-"`
	ReleaseExecutionContext context.CancelFunc            `json:"-"`
}
