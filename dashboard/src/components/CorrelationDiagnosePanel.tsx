import { useState } from 'react'
import type {
  CorrelationDiagnoseResponse,
  CorrelationRepairSuggestion,
  CorrelationStep,
  ScriptExtractorDefinition,
  ScriptReplayActionEvent,
} from '../types/scripts'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import { useCorrelationRulesetApi } from '../hooks/api/useCorrelationRulesetApi'
import { harToSteps, replayEventsToSteps } from './correlationDiagnose'
import type { CorrelationExtractorRepair } from './scriptCorrelationRepair'

interface CorrelationDiagnosePanelProps {
  scriptId: string | null
  projectId?: string
  events: ScriptReplayActionEvent[] | undefined
  recordingStatus: ScriptRecordingStatus | null
  actionCount: number
  diagnoseWithBaseline: (scriptId: string, replay: CorrelationStep[]) => Promise<CorrelationDiagnoseResponse>
  diagnoseWithUpload: (recorded: CorrelationStep[], replay: CorrelationStep[]) => Promise<CorrelationDiagnoseResponse>
  onAddExtractor: (actionIndex: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair) => void
}

function truncate(value: string, max = 80): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

// Record-vs-replay correlation diagnosis: diff the current replay against the
// recorded baseline (stored server-side, or an uploaded HAR) and surface ranked,
// one-click-applicable extractor fixes for values that rotated on replay.
export function CorrelationDiagnosePanel({
  scriptId,
  projectId,
  events,
  recordingStatus,
  actionCount,
  diagnoseWithBaseline,
  diagnoseWithUpload,
  onAddExtractor,
}: CorrelationDiagnosePanelProps) {
  const rulesetApi = useCorrelationRulesetApi()
  const [suggestions, setSuggestions] = useState<CorrelationRepairSuggestion[] | null>(null)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [applied, setApplied] = useState<Set<string>>(new Set())
  const [savedRuleset, setSavedRuleset] = useState(false)

  const saveAsRuleset = async () => {
    if (!projectId || !suggestions) return
    const rules = suggestions
      .filter(s => s.extractor)
      .map(s => ({
        name: s.extractor!.name || 'correlation_value',
        rule_id: s.rule_id,
        source: s.extractor!.source,
        extract_type: s.extractor!.extract_type,
        expression: s.extractor!.expression,
        sample_value: s.recorded_value,
        confidence: s.confidence,
      }))
    if (rules.length === 0) return
    setError(null)
    try {
      await rulesetApi.createCorrelationRuleset({ project_id: projectId, name: 'Diagnosed correlations', rules })
      setSavedRuleset(true)
    } catch {
      setError('Could not save the suggestions as a ruleset.')
    }
  }

  const hasBaseline = !!recordingStatus?.exists && !!scriptId
  const replaySteps = replayEventsToSteps(events)

  const present = (response: CorrelationDiagnoseResponse) => {
    setSuggestions(response.suggestions || [])
    // A fresh diagnosis produces a new suggestion set, so re-enable "Save as
    // ruleset" — otherwise it stays stuck disabled for the rest of the session
    // after the first save. Applied-state is per-suggestion and also resets.
    setSavedRuleset(false)
    setApplied(new Set())
  }

  const runBaseline = async () => {
    if (!scriptId) return
    setRunning(true)
    setError(null)
    try {
      present(await diagnoseWithBaseline(scriptId, replaySteps))
    } catch {
      setError('Diagnosis failed. The recorded baseline may be unavailable.')
    } finally {
      setRunning(false)
    }
  }

  const runUpload = async (file: File) => {
    setRunning(true)
    setError(null)
    try {
      const har = JSON.parse(await file.text())
      const recorded = harToSteps(har)
      if (recorded.length === 0) {
        setError('No usable requests found in that HAR.')
        return
      }
      present(await diagnoseWithUpload(recorded, replaySteps))
    } catch {
      setError('Could not read that file as a HAR recording.')
    } finally {
      setRunning(false)
    }
  }

  const applySuggestion = (suggestion: CorrelationRepairSuggestion) => {
    if (!suggestion.extractor) return
    const actionIndex = suggestion.source_step_id
    if (actionIndex < 0 || actionIndex >= actionCount) return
    const repair = typeof suggestion.failing_step_id === 'number' &&
      suggestion.failing_step_id >= 0 &&
      suggestion.failing_step_id < actionCount &&
      suggestion.recorded_value &&
      suggestion.extractor.name
      ? {
        failingActionIndex: suggestion.failing_step_id,
        recordedValue: suggestion.recorded_value,
        replacement: `{{var.${suggestion.extractor.name}}}`,
      }
      : undefined
    const extractor: ScriptExtractorDefinition = {
      name: suggestion.extractor.name || 'correlation_value',
      source: suggestion.extractor.source || 'body',
      type: suggestion.extractor.extract_type || 'regex',
      expression: suggestion.extractor.expression,
    }
    if (suggestion.extractor.convert) {
      extractor.convert = suggestion.extractor.convert
    }
    onAddExtractor(actionIndex, extractor, repair)
    setApplied(previous => new Set(previous).add(suggestion.rule_id + suggestion.source_step_id))
  }

  return (
    <div className="replay-detail-block correlation-diagnose" aria-label="Correlation diagnosis">
      <span className="replay-detail-label">Correlation diagnosis</span>
      <p className="extractor-tester-hint">
        Compare this replay against the recording to find values that rotated (session tokens, CSRF, view-state) and add the extractors that fix them.
      </p>

      {suggestions === null ? (
        <div className="correlation-diagnose-actions">
          {hasBaseline ? (
            <button type="button" className="btn btn-sm btn-primary" onClick={runBaseline} disabled={running || replaySteps.length === 0}>
              {running ? 'Diagnosing…' : 'Diagnose correlations'}
            </button>
          ) : (
            <label className="btn btn-sm btn-secondary correlation-diagnose-upload">
              {running ? 'Diagnosing…' : 'Diagnose from recording (HAR)…'}
              <input
                type="file"
                accept=".har,application/json"
                hidden
                onChange={event => { const file = event.target.files?.[0]; if (file) void runUpload(file) }}
              />
            </label>
          )}
          {!hasBaseline && <span className="extractor-tester-note">No stored baseline for this script — upload the recording it came from.</span>}
          {replaySteps.length === 0 && <span className="extractor-tester-note">Run a replay first.</span>}
        </div>
      ) : suggestions.length === 0 ? (
        <div className="correlation-diagnose-empty">No correlation issues found — the replay matches the recording.</div>
      ) : (
        <div className="correlation-diagnose-list">
          <div className="correlation-diagnose-summary">
            {suggestions.length} suggestion{suggestions.length === 1 ? '' : 's'}
            {projectId && suggestions.some(s => s.extractor) && (
              <button type="button" className="btn btn-sm btn-secondary" onClick={saveAsRuleset} disabled={savedRuleset}>
                {savedRuleset ? 'Saved as ruleset' : 'Save as ruleset'}
              </button>
            )}
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setSuggestions(null); setSavedRuleset(false); setApplied(new Set()) }}>Reset</button>
          </div>
          {suggestions.map((suggestion, index) => {
            const appliedKey = suggestion.rule_id + suggestion.source_step_id
            const canApply = !!suggestion.extractor && suggestion.source_step_id >= 0 && suggestion.source_step_id < actionCount
            return (
              <div key={`${suggestion.rule_id}-${index}`} className={`correlation-diagnose-item severity-${suggestion.severity}`}>
                <div className="correlation-diagnose-item-head">
                  <span className={`action-signal-pill ${suggestion.severity === 'error' ? 'error' : 'warning'}`}>{suggestion.severity}</span>
                  <strong>{suggestion.title}</strong>
                  {typeof suggestion.confidence === 'number' && (
                    <span className="metric-badge">{Math.round(suggestion.confidence * 100)}%</span>
                  )}
                </div>
                <p>{suggestion.detail}</p>
                {(suggestion.recorded_value || suggestion.replay_value) && (
                  <div className="correlation-diagnose-diff">
                    <code className="correlation-diagnose-recorded">{truncate(suggestion.recorded_value || '∅')}</code>
                    <span aria-hidden="true">→</span>
                    <code className="correlation-diagnose-replay">{truncate(suggestion.replay_value || '∅')}</code>
                  </div>
                )}
                {suggestion.extractor && (
                  <div className="extractor-tester-actions">
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => applySuggestion(suggestion)} disabled={!canApply || applied.has(appliedKey)}>
                      {applied.has(appliedKey) ? 'Added' : 'Add as extractor'}
                    </button>
                    {!canApply && <span className="extractor-tester-note">Cannot map this fix to an editable action.</span>}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
      {error && <span className="extractor-tester-error">{error}</span>}
    </div>
  )
}
