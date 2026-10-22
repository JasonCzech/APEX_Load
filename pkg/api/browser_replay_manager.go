package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"time"
)

type BrowserReplaySession = recordbridge.BrowserReplaySession
type replayCompletionHook = recordbridge.ReplayCompletionHook
type BrowserReplayManager struct {
	*recordbridge.BrowserReplayManager
}

const BrowserReplayQueued = recordbridge.BrowserReplayQueued
const BrowserReplayRunning = recordbridge.BrowserReplayRunning
const BrowserReplayPaused = recordbridge.BrowserReplayPaused
const BrowserReplayCompleted = recordbridge.BrowserReplayCompleted
const BrowserReplayFailed = recordbridge.BrowserReplayFailed
const BrowserReplayStopped = recordbridge.BrowserReplayStopped

var errBrowserReplaySessionIDConflict = recordbridge.ErrBrowserReplaySessionIDConflict
var errBrowserReplaySessionCapacity = recordbridge.ErrBrowserReplaySessionCapacity
var errBrowserReplayManagerClosed = recordbridge.ErrBrowserReplayManagerClosed

func NewBrowserReplayManager(hub *Hub, options ...recordbridge.ReplayManagerOptions) *BrowserReplayManager {
	option := recordbridge.ReplayManagerOptions{ExecutionContextError: errNotExecutionLeader}
	if len(options) > 0 {
		option = options[0]
		if option.ExecutionContextError == nil {
			option.ExecutionContextError = errNotExecutionLeader
		}
	}
	var sink recordbridge.EventSink
	if hub != nil {
		sink = hub
	}
	return &BrowserReplayManager{recordbridge.NewBrowserReplayManager(sink, option)}
}
func (m *BrowserReplayManager) setCompletionHook(hook replayCompletionHook) {
	m.SetCompletionHook(hook)
}
func (m *BrowserReplayManager) setSessionIDReserver(reserve replaySessionIDReserver) {
	if reserve == nil {
		m.SetSessionIDReserver(nil)
		return
	}
	m.SetSessionIDReserver(func(ctx context.Context, id, project string) (string, func(), recordbridge.SessionLease, error) {
		id, release, signal, err := reserve(ctx, id, project)
		if signal == nil {
			return id, release, nil, err
		}
		return id, release, signal, err
	})
}
func (m *BrowserReplayManager) Start(scriptID string, script *dsl.Script, req BrowserReplayStartRequest) (*BrowserReplaySession, error) {
	return m.BrowserReplayManager.Start(scriptID, script, recordbridge.BrowserReplayStartRequest{
		BrowserAuthoredDefinition: req.browserAuthoredDefinition,
		LiveTableSeeds:            req.LiveTableSeeds, GroupName: req.GroupName, ProjectID: req.ProjectID, ReplaySessionID: req.ReplaySessionID,
		Variables: req.Variables, RedactionRules: req.RedactionRules, StartPaused: req.StartPaused, Breakpoints: req.Breakpoints,
		Browser: req.Browser, IgnoreThinkTime: req.IgnoreThinkTime, RunID: req.RunID, ContinueOnError: req.ContinueOnError,
		RecordHAR: req.RecordHAR, Script: req.Script, Preflight: req.Preflight, SecretResolver: req.SecretResolver,
		ExecutionContext: req.executionContext, ReleaseExecutionContext: req.releaseExecutionContext,
	})
}
func cloneBrowserReplayReport(report *engine.ReplayReport) *engine.ReplayReport {
	return recordbridge.CloneBrowserReplayReport(report)
}
func retainReplaySessionEvent(
	events *[]engine.ReplayActionEvent,
	eventBytes *[]int,
	retainedBytes *int,
	droppedEvents *int,
	event engine.ReplayActionEvent,
) engine.ReplayActionEvent {
	return recordbridge.RetainReplaySessionEvent(events, eventBytes, retainedBytes, droppedEvents, event)
}
func cloneBrowserErrorSnapshot(snapshot *protocol.ErrorSnapshot) *protocol.ErrorSnapshot {
	return recordbridge.CloneBrowserErrorSnapshot(snapshot)
}
func cloneBrowserReplayEvent(event engine.ReplayActionEvent) engine.ReplayActionEvent {
	return recordbridge.CloneBrowserReplayEvent(event)
}
func cloneBrowserReplayArtifactManifest(manifest *engine.ReplayArtifactManifest) *engine.ReplayArtifactManifest {
	return recordbridge.CloneBrowserReplayArtifactManifest(manifest)
}
func cloneReplaySessionTime(value *time.Time) *time.Time {
	return recordbridge.CloneReplaySessionTime(value)
}
func cloneBrowserReplayDiagnosis(diagnosis *engine.ReplayDiagnosis) *engine.ReplayDiagnosis {
	return recordbridge.CloneBrowserReplayDiagnosis(diagnosis)
}
func cloneBrowserReplayRepairSuggestions(suggestions []engine.ReplayRepairSuggestion) []engine.ReplayRepairSuggestion {
	return recordbridge.CloneBrowserReplayRepairSuggestions(suggestions)
}
func cloneBrowserReplayDiagnosisSummary(summary *engine.ReplayDiagnosisSummary) *engine.ReplayDiagnosisSummary {
	return recordbridge.CloneBrowserReplayDiagnosisSummary(summary)
}
func cloneBrowserStringMap(input map[string]string) map[string]string {
	return recordbridge.CloneBrowserStringMap(input)
}
func cloneBrowserConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	return recordbridge.CloneBrowserConsoleEntries(entries)
}
func cloneBrowserNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	return recordbridge.CloneBrowserNetworkEntries(entries)
}
func cloneBrowserDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	return recordbridge.CloneBrowserDOMSummary(summary)
}
func cloneBrowserClientMetrics(metrics *protocol.ClientMetrics) *protocol.ClientMetrics {
	return recordbridge.CloneBrowserClientMetrics(metrics)
}
