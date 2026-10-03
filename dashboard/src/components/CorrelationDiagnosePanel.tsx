import CorrelationRelationshipCoverage from './CorrelationRelationshipCoverage'
import { admitRelationshipEvidence } from './correlationEvidenceAdmission'
import { useEffect, useMemo, useRef, useState } from 'react'
import type {
  CorrelationDiagnoseResponse,
  CorrelationRepairSuggestion,
  CorrelationStep,
  ScriptExtractorDefinition,
  ScriptReplayActionEvent,
} from '../types/scripts'
import type { ScriptRecordingStatus } from '../hooks/api/useScriptApi'
import { ApiError } from '../hooks/apiClient'
import { useCorrelationRulesetApi } from '../hooks/api/useCorrelationRulesetApi'
import { duplicateCorrelationStepIds, HarToStepsLimitError, harToSteps, nestedCorrelationStepIds, replayEventsToSteps } from './correlationDiagnose'
import {
  repairLocationsFromSuggestion,
  type CorrelationApplyResult,
  type CorrelationExtractorRepair,
} from './scriptCorrelationRepair'

export type RecordingStatusState = 'idle' | 'loading' | 'ready' | 'error'

// Keep uploaded evidence within the same envelope as the 2 MiB correlation
// diagnosis route. The entry/content limits also bound object walking after
// JSON parsing, including compact HARs with very large arrays.
const CORRELATION_HAR_MAX_FILE_BYTES = 2 * 1024 * 1024
const CORRELATION_HAR_MAX_ENTRIES = 4096
const CORRELATION_HAR_MAX_DECODED_CHARS = 2 * 1024 * 1024

interface CorrelationDiagnosePanelProps {
  scriptId: string | null
  replaySessionId?: string
  replayTerminal?: boolean
  replayMatchesCurrentScript?: boolean
  projectId?: string
  events: ScriptReplayActionEvent[] | undefined
  recordingStatus: ScriptRecordingStatus | null
  recordingStatusState?: RecordingStatusState
  onRetryRecordingStatus?: () => void
  diagnosisKey?: string
  /**
   * Serialized editable-actions key for the current draft (null while the JSON
   * buffer is dirty/invalid). When provided, the panel snapshots it at
   * diagnosis time and distinguishes its own applied fixes (which keep the
   * presented diagnosis usable) from external draft edits (which disable the
   * remaining automatic applies).
   */
  currentEditableActionsKey?: string | null
  actionCount: number
  diagnoseWithBaseline: (scriptId: string, replay: CorrelationStep[], replaySessionId?: string) => Promise<CorrelationDiagnoseResponse>
  diagnoseWithUpload: (recorded: CorrelationStep[], replay: CorrelationStep[]) => Promise<CorrelationDiagnoseResponse>
  onAddExtractor: (actionIndex: number, extractor: ScriptExtractorDefinition, repair?: CorrelationExtractorRepair) => CorrelationApplyResult
}

function baselineUnavailableMessage(reason?: string): string {
  switch (reason) {
    case 'raw_baseline_unavailable':
      return 'This recording has only a scrubbed baseline. Configure baseline encryption and capture it again to enable diagnosis.'
    case 'baseline_empty':
      return 'The stored recording does not contain any diagnosable requests.'
    case 'baseline_not_found':
      return 'No stored baseline is available for this script.'
    case 'legacy_encryption_recapture_required':
      return 'This baseline was captured with a legacy encryption format that can no longer be read. Capture a new recording to re-enable correlation diagnosis.'
    default:
      return reason || 'The stored baseline is disclosure-safe but cannot be used for correlation diagnosis.'
  }
}

function suggestionIdentity(suggestion: CorrelationRepairSuggestion): string {
  return [
    suggestion.rule_id,
    suggestion.source_step_id,
    suggestion.failing_step_id ?? '',
    suggestion.extractor?.name ?? '',
    suggestion.extractor?.source ?? '',
    suggestion.extractor?.extract_type ?? '',
    suggestion.extractor?.expression ?? '',
  ].join('\u0000')
}

function boundedDiagnosisError(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError)) return fallback
  const message = error.message.replace(/\s+/g, ' ').trim().slice(0, 240)
  return message ? `Diagnosis failed: ${message}` : fallback
}

// Record-vs-replay correlation diagnosis: diff the current replay against the
// recorded baseline (stored server-side, or an uploaded HAR) and surface ranked
// extractor fixes for values that rotated on replay.
export function CorrelationDiagnosePanel({
  scriptId,
  replaySessionId,
  replayTerminal = false,
  replayMatchesCurrentScript = false,
  projectId,
  events,
  recordingStatus,
  recordingStatusState = 'ready',
  onRetryRecordingStatus,
  diagnosisKey,
  currentEditableActionsKey,
  actionCount,
  diagnoseWithBaseline,
  diagnoseWithUpload,
  onAddExtractor,
}: CorrelationDiagnosePanelProps) {
  const rulesetApi = useCorrelationRulesetApi()
  const [suggestions, setSuggestions] = useState<CorrelationRepairSuggestion[] | null>(null)
  const [presentedDiagnosisKey, setPresentedDiagnosisKey] = useState<string | null>(null)
  const [diagnosisCoverage, setDiagnosisCoverage] = useState<Pick<CorrelationDiagnoseResponse, 'compared_recorded_steps' | 'total_recorded_steps' | 'truncated' | 'relationship_coverage'> | null>(null)
  const [automaticRepairSupported, setAutomaticRepairSupported] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [applicationResults, setApplicationResults] = useState<Map<string, CorrelationApplyResult>>(new Map())
  const [savedRuleset, setSavedRuleset] = useState(false)
  const [savingRuleset, setSavingRuleset] = useState(false)
  // True once the editable draft changed in a way this panel did not cause
  // (manual edit, other tooling) after a diagnosis was presented. The
  // presented suggestions stay visible, but automatic applies are disabled
  // because their raw offsets were proven against the diagnosis-time draft.
  const [externallyEdited, setExternallyEdited] = useState(false)
  const diagnosisRequestRef = useRef(0)
  const rulesetSaveRequestRef = useRef(0)
  const rulesetSaveInFlightKeysRef = useRef<Set<string>>(new Set())
  const appliedSuggestionKeysRef = useRef<Set<string>>(new Set())
  // The editable-actions key this diagnosis currently trusts: seeded at
  // diagnosis time and advanced whenever the key change was produced by one of
  // this panel's own applies.
  const trustedEditableActionsKeyRef = useRef<string | null | undefined>(undefined)
  const panelMutationPendingRef = useRef(false)
  // Failing actions already rewritten by an apply. Any later suggestion whose
  // offsets target the same action falls back to extractor-only, because those
  // offsets were computed against the pre-repair request content.
  const repairedActionIndexesRef = useRef<Set<number>>(new Set())
	const harUploadInputRef = useRef<HTMLInputElement>(null)

  // These scans are O(replay size); memoize them so replay-progress renders and
  // debug-tab keystrokes do not rescan the whole event list.
  const replaySteps = useMemo(() => replayEventsToSteps(events), [events])
  const ambiguousStepIds = useMemo(() => duplicateCorrelationStepIds(replaySteps), [replaySteps])
  const nestedStepIds = useMemo(() => nestedCorrelationStepIds(replaySteps), [replaySteps])
  // The key identifies the diagnosis inputs (replay identity). The live draft
  // and replayMatchesCurrentScript are deliberately excluded: applying one
  // suggestion edits the draft, and that must not wipe the presented diagnosis
  // or the per-suggestion applied/failed results.
  const effectiveDiagnosisKey = useMemo(() => JSON.stringify({
    // A caller-provided diagnosis key identifies the evidence revision, but
    // it must not replace the authorization/resource scope. Otherwise a
    // project switch that happens to reuse the same key can expose or save
    // suggestions from the previous project.
    diagnosisKey: diagnosisKey ?? null,
    projectId: projectId ?? null,
    scriptId,
    replaySessionId: replaySessionId ?? null,
    actionCount,
    eventCount: events?.length ?? 0,
    replayTerminal,
  }), [diagnosisKey, projectId, scriptId, replaySessionId, actionCount, events, replayTerminal])
  const activeDiagnosisKeyRef = useRef(effectiveDiagnosisKey)
  activeDiagnosisKeyRef.current = effectiveDiagnosisKey
  const currentSuggestions = presentedDiagnosisKey === effectiveDiagnosisKey ? suggestions : null
  const currentDiagnosisCoverage = presentedDiagnosisKey === effectiveDiagnosisKey ? diagnosisCoverage : null

  useEffect(() => {
    diagnosisRequestRef.current += 1
    setSuggestions(null)
    setPresentedDiagnosisKey(null)
    setDiagnosisCoverage(null)
    setAutomaticRepairSupported(false)
    setRunning(false)
    setError(null)
    setApplicationResults(new Map())
    setSavedRuleset(false)
    setSavingRuleset(false)
    rulesetSaveRequestRef.current += 1
    setExternallyEdited(false)
    trustedEditableActionsKeyRef.current = undefined
    panelMutationPendingRef.current = false
    repairedActionIndexesRef.current = new Set()
    appliedSuggestionKeysRef.current = new Set()
  }, [effectiveDiagnosisKey])

  useEffect(() => {
    // Distinguish this panel's own applies from external draft edits. An apply
    // advances the trusted key; any other change (including a dirty JSON
    // buffer, which reports null) marks the presented diagnosis as drifted.
    if (presentedDiagnosisKey === null || currentEditableActionsKey === undefined) return
    if (currentEditableActionsKey === trustedEditableActionsKeyRef.current) return
    if (panelMutationPendingRef.current && currentEditableActionsKey !== null) {
      trustedEditableActionsKeyRef.current = currentEditableActionsKey
      panelMutationPendingRef.current = false
      return
    }
    setExternallyEdited(true)
  }, [currentEditableActionsKey, presentedDiagnosisKey])

  const saveAsRuleset = async () => {
    if (!projectId || !currentSuggestions) return
    const saveDiagnosisKey = effectiveDiagnosisKey
    // Resetting the presentation invalidates a stale response, but it cannot
    // cancel a create request that already reached the server. Keep a
    // per-diagnosis synchronous fence until that request settles so resetting
    // and immediately diagnosing the same evidence cannot create duplicates.
    if (rulesetSaveInFlightKeysRef.current.has(saveDiagnosisKey)) return
    const saveRequestID = ++rulesetSaveRequestRef.current
    const isCurrentSave = () => (
      activeDiagnosisKeyRef.current === saveDiagnosisKey
      && rulesetSaveRequestRef.current === saveRequestID
    )
    const uniqueRules = new Map<string, {
      name: string
      rule_id: string
      source: string
      extract_type: string
      expression: string
      confidence: number
    }>()
    currentSuggestions
      .filter(s => s.extractor)
      .forEach(s => {
        const rule = {
          name: s.extractor!.name || 'correlation_value',
          rule_id: s.rule_id,
          source: s.extractor!.source,
          extract_type: s.extractor!.extract_type,
          expression: s.extractor!.expression,
          confidence: s.confidence,
        }
        const identity = [rule.rule_id, rule.name, rule.source, rule.extract_type, rule.expression].join('\u0000')
        const previous = uniqueRules.get(identity)
        if (!previous || rule.confidence > previous.confidence) uniqueRules.set(identity, rule)
      })
    const rules = [...uniqueRules.values()]
    if (rules.length === 0) return
    rulesetSaveInFlightKeysRef.current.add(saveDiagnosisKey)
    setSavingRuleset(true)
    setError(null)
    try {
      await rulesetApi.createCorrelationRuleset({ project_id: projectId, name: 'Diagnosed correlations', rules })
      if (isCurrentSave()) setSavedRuleset(true)
    } catch {
      if (isCurrentSave()) setError('Could not save the suggestions as a ruleset.')
    } finally {
      rulesetSaveInFlightKeysRef.current.delete(saveDiagnosisKey)
      if (isCurrentSave()) {
        setSavingRuleset(false)
      }
    }
  }

  const hasBaseline = recordingStatusState === 'ready' && !!recordingStatus?.exists && recordingStatus.correlation_usable !== false && !!scriptId
  const trustedServerReplay = !!scriptId && !!replaySessionId && replayTerminal && replayMatchesCurrentScript
  const diagnosisReady = replayTerminal && replayMatchesCurrentScript && replaySteps.length > 0 && ambiguousStepIds.length === 0 && nestedStepIds.length === 0
  const baselineTruncated = recordingStatus?.correlation_truncated === true
  const baselineTotal = recordingStatus?.request_count
  const baselineRetained = recordingStatus?.correlation_request_count ?? recordingStatus?.request_count

  const present = (response: CorrelationDiagnoseResponse, supportsAutomaticRepair: boolean) => {
    setSuggestions(response.suggestions || [])
    setPresentedDiagnosisKey(effectiveDiagnosisKey)
    setAutomaticRepairSupported(supportsAutomaticRepair)
    setDiagnosisCoverage({
      // Missing coverage metadata is not evidence of a comparison. Treat it
      // as zero so an older or malformed response cannot produce a clean claim.
      compared_recorded_steps: response.compared_recorded_steps ?? 0,
      total_recorded_steps: response.total_recorded_steps ?? 0,
      truncated: response.truncated,
      relationship_coverage: response.relationship_coverage,
    })
    // A fresh diagnosis produces a new suggestion set, so re-enable "Save as
    // ruleset" — otherwise it stays stuck disabled for the rest of the session
    // after the first save. Applied-state is per-suggestion and also resets.
    setSavedRuleset(false)
    setApplicationResults(new Map())
    // Snapshot the draft state this diagnosis was proven against.
    setExternallyEdited(false)
    trustedEditableActionsKeyRef.current = currentEditableActionsKey
    panelMutationPendingRef.current = false
    repairedActionIndexesRef.current = new Set()
    appliedSuggestionKeysRef.current = new Set()
  }

  const runBaseline = async () => {
    if (!scriptId || !trustedServerReplay || !diagnosisReady) return
    const requestId = ++diagnosisRequestRef.current
    const requestDiagnosisKey = effectiveDiagnosisKey
    setRunning(true)
    setError(null)
    try {
      const response = await diagnoseWithBaseline(scriptId, replaySteps, replaySessionId)
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) present(response, true)
    } catch (cause: unknown) {
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) {
        setError(boundedDiagnosisError(cause, 'Diagnosis failed. The recorded baseline may be unavailable.'))
      }
    } finally {
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) setRunning(false)
    }
  }

  const runUpload = async (file: File) => {
    if (!diagnosisReady) return
    const requestId = ++diagnosisRequestRef.current
    const requestDiagnosisKey = effectiveDiagnosisKey
    setRunning(true)
    setError(null)
    try {
      if (file.size > CORRELATION_HAR_MAX_FILE_BYTES) {
        setError('That HAR is too large for correlation diagnosis. Upload a file no larger than 2 MiB.')
        return
      }
      let recorded: CorrelationStep[]
      try {
        const har = JSON.parse(await file.text())
        recorded = harToSteps(har, {
          maxEntries: CORRELATION_HAR_MAX_ENTRIES,
          maxDecodedChars: CORRELATION_HAR_MAX_DECODED_CHARS,
        })
      } catch (cause: unknown) {
        if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) {
          setError(cause instanceof HarToStepsLimitError
            ? 'That HAR contains too many entries or too much decoded content for correlation diagnosis.'
            : 'Could not read that file as a HAR recording.')
        }
        return
      }
      if (recorded.length === 0) {
        if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) setError('No usable requests found in that HAR.')
        return
      }
      // Uploaded recordings are intentionally diagnosed only against the
      // disclosure-safe client replay. Pairing attacker-controlled recorded
      // data with a server-held raw replay session would create a value oracle.
      const response = await diagnoseWithUpload(recorded, replaySteps)
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) present(response, false)
    } catch (cause: unknown) {
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) {
        setError(boundedDiagnosisError(cause, 'Diagnosis failed for that HAR recording.'))
      }
    } finally {
      if (diagnosisRequestRef.current === requestId && activeDiagnosisKeyRef.current === requestDiagnosisKey) setRunning(false)
    }
  }

  const applySuggestion = (suggestion: CorrelationRepairSuggestion) => {
    // The presented diagnosis was proven against the diagnosis-time draft
    // (replay terminal and matching at that moment); it stays applicable while
    // the only draft changes since then are this panel's own applies.
    if (presentedDiagnosisKey !== effectiveDiagnosisKey || !suggestion.extractor || externallyEdited) return
    const suggestionKey = suggestionIdentity(suggestion)
    if (appliedSuggestionKeysRef.current.has(suggestionKey)) return
    const actionIndex = suggestion.source_step_id
    if (actionIndex < 0 || actionIndex >= actionCount) return
    const requestLocations = repairLocationsFromSuggestion(suggestion, replaySteps)
    const repair = automaticRepairSupported && suggestion.review_action !== 'manual_value_replacement_required' &&
      typeof suggestion.failing_step_id === 'number' &&
      suggestion.failing_step_id >= 0 &&
      suggestion.failing_step_id < actionCount &&
      // A previous apply already rewrote this failing action; these offsets
      // were computed against its pre-repair content, so fall back to
      // extractor-only rather than risking a misplaced replacement.
      !repairedActionIndexesRef.current.has(suggestion.failing_step_id) &&
      requestLocations.length > 0 &&
      suggestion.extractor.name
      ? {
        failingActionIndex: suggestion.failing_step_id,
        requestLocations,
        replacement: `{{var.${suggestion.extractor.name}}}`,
      }
      : undefined
    const extractor: ScriptExtractorDefinition = {
      name: suggestion.extractor.name || 'correlation_value',
      source: suggestion.extractor.source || 'body',
      type: suggestion.extractor.extract_type || 'regex',
      expression: suggestion.extractor.expression,
      // A diagnosis repair exists specifically because replaying a stale value
      // failed. Never let a later extraction miss silently retain that value.
      on_miss: suggestion.extractor.on_miss || 'fail',
    }
    if (suggestion.extractor.convert) {
      extractor.convert = suggestion.extractor.convert
    }
    // React state does not update until after the event completes. Fence the
    // synchronous callback as well so two clicks in the same turn cannot add
    // the same extractor twice.
    appliedSuggestionKeysRef.current.add(suggestionKey)
    const result = onAddExtractor(actionIndex, extractor, repair)
    if (result) {
      if (result.extractorAdded || result.requestRepaired) {
        // The draft will change; the next editable-actions key is this panel's
        // own doing, not an external edit.
        panelMutationPendingRef.current = true
      }
      if (result.requestRepaired && repair) {
        repairedActionIndexesRef.current.add(repair.failingActionIndex)
      }
      setApplicationResults(previous => new Map(previous).set(suggestionKey, result))
    } else {
      appliedSuggestionKeysRef.current.delete(suggestionKey)
    }
  }

  const resetDiagnosis = () => {
    diagnosisRequestRef.current += 1
    rulesetSaveRequestRef.current += 1
    setSuggestions(null)
    setPresentedDiagnosisKey(null)
    setDiagnosisCoverage(null)
    setAutomaticRepairSupported(false)
    setRunning(false)
    setError(null)
    setSavedRuleset(false)
    setSavingRuleset(false)
    setApplicationResults(new Map())
    setExternallyEdited(false)
    trustedEditableActionsKeyRef.current = undefined
    panelMutationPendingRef.current = false
    repairedActionIndexesRef.current = new Set()
    appliedSuggestionKeysRef.current = new Set()
  }

  const coverageTruncated = currentDiagnosisCoverage?.truncated === true
  const coverageCompared = currentDiagnosisCoverage?.compared_recorded_steps
  const coverageTotal = currentDiagnosisCoverage?.total_recorded_steps
  const coverageMessage = coverageTruncated
    ? `Correlation diagnosis compared ${coverageCompared ?? 'the available'} of ${coverageTotal ?? 'all'} recorded request observations; results cover only that compared scope.`
    : null
  const baselineCoverageMessage = baselineTruncated
    ? `The stored baseline retains ${baselineRetained ?? 'only part'} of ${baselineTotal ?? 'all'} request observations; diagnosis will cover only that retained scope.`
    : null
  const diagnosisInconclusive = currentDiagnosisCoverage?.compared_recorded_steps === 0
  const relationshipEvidence = useMemo(() => admitRelationshipEvidence(currentDiagnosisCoverage?.relationship_coverage), [currentDiagnosisCoverage?.relationship_coverage])
  const relationshipInconclusive = relationshipEvidence.present && (relationshipEvidence.malformed || relationshipEvidence.coverage?.complete !== true)

  return (
    <div className="replay-detail-block correlation-diagnose" aria-label="Correlation diagnosis">
      <span className="replay-detail-label">Correlation diagnosis</span>
      <p className="extractor-tester-hint">
        Compare this replay against the recording to find values that rotated (session tokens, CSRF, view-state) and add the extractors that fix them.
      </p>

      <CorrelationRelationshipCoverage coverage={currentDiagnosisCoverage?.relationship_coverage} />
      {currentSuggestions === null ? (
        <div className="correlation-diagnose-actions">
          {recordingStatusState === 'loading' ? (
            <button type="button" className="btn btn-sm btn-secondary" disabled>Checking stored baseline…</button>
          ) : hasBaseline ? (
            <button type="button" className="btn btn-sm btn-primary" onClick={runBaseline} disabled={running || !trustedServerReplay || !diagnosisReady}>
              {running ? 'Diagnosing…' : 'Diagnose correlations'}
            </button>
          ) : (
			<>
			<button
			  type="button"
			  className="btn btn-sm btn-secondary correlation-diagnose-upload"
			  disabled={running || !diagnosisReady}
			  onClick={() => harUploadInputRef.current?.click()}
			>
              {running ? 'Diagnosing…' : 'Diagnose from recording (HAR)…'}
			</button>
              <input
				ref={harUploadInputRef}
                className="correlation-diagnose-file-input"
                type="file"
				tabIndex={-1}
                accept=".har,application/json"
                disabled={running || !diagnosisReady}
                onChange={event => { const file = event.target.files?.[0]; if (file) void runUpload(file) }}
              />
			</>
          )}
          {recordingStatusState === 'ready' && !recordingStatus?.exists && <span className="extractor-tester-note">No recorded baseline is available. If this script originated from a recording, you can attach it for comparison. Contract-authored scripts do not require one.</span>}
          {recordingStatusState === 'ready' && !!recordingStatus?.exists && recordingStatus.correlation_usable === false && (
            <span className="extractor-tester-note">{baselineUnavailableMessage(recordingStatus.correlation_unavailable_reason)}</span>
          )}
          {recordingStatusState === 'error' && (
            <span className="extractor-tester-note">
              Could not check whether this script has a stored baseline.
              {onRetryRecordingStatus && <button type="button" className="btn btn-sm btn-ghost" onClick={onRetryRecordingStatus}>Retry</button>}
            </span>
          )}
          {baselineCoverageMessage && <span className="extractor-tester-note">{baselineCoverageMessage}</span>}
          {!(scriptId && replaySessionId) && (
            <span className="extractor-tester-note">This replay has no server-side raw evidence. HAR fallback can suggest extractors, but request replacements require manual verification.</span>
          )}
          {!replayTerminal && replaySteps.length > 0 && (
            <span className="extractor-tester-note">Wait for the replay session to finish before diagnosing correlations.</span>
          )}
          {replayTerminal && !replayMatchesCurrentScript && (
            <span className="extractor-tester-error">The editable actions changed after this replay started. Run a new replay before diagnosing correlations.</span>
          )}
          {hasBaseline && replayTerminal && replayMatchesCurrentScript && !(scriptId && replaySessionId) && (
            <span className="extractor-tester-note">Run a new saved-script replay to attach trusted raw evidence to the stored baseline.</span>
          )}
          {ambiguousStepIds.length > 0 && (
            <span className="extractor-tester-error">This replay contains repeated observations for action {ambiguousStepIds.map(id => id + 1).join(', ')}. Correlation diagnosis is disabled because those sequence or loop requests cannot yet be mapped to one editable request safely.</span>
          )}
          {nestedStepIds.length > 0 && (
            <span className="extractor-tester-error">This replay contains nested request evidence for action {nestedStepIds.map(id => id + 1).join(', ')}. Correlation diagnosis is disabled because nested sequence or loop leaves are not bound to top-level recording actions.</span>
          )}
          {replaySteps.length === 0 && <span className="extractor-tester-note">Run a replay first.</span>}
        </div>
      ) : currentSuggestions.length === 0 ? (
        <div className="correlation-diagnose-empty">
          {diagnosisInconclusive
            ? 'Correlation diagnosis was inconclusive because no recorded request steps were compared.'
            : relationshipInconclusive
              ? 'Correlation relationship evidence is incomplete. Review missing observations and unresolved values before drawing a replay conclusion.'
              : 'No correlation issues detected in the compared recording/replay scope.'}
          {coverageMessage && <span className="extractor-tester-note">{coverageMessage}</span>}
          {!automaticRepairSupported && <span className="extractor-tester-note">The replay evidence was disclosure-safe only, so verify this result manually with a saved replay session.</span>}
          <button type="button" className="btn btn-sm btn-ghost" onClick={resetDiagnosis}>Try another diagnosis</button>
        </div>
      ) : (
        <div className="correlation-diagnose-list">
          <div className="correlation-diagnose-summary">
            {currentSuggestions.length} suggestion{currentSuggestions.length === 1 ? '' : 's'}
            {projectId && currentSuggestions.some(s => s.extractor) && (
              <button type="button" className="btn btn-sm btn-secondary" onClick={saveAsRuleset} disabled={savedRuleset || savingRuleset}>
                {savedRuleset ? 'Saved as ruleset' : savingRuleset ? 'Saving ruleset…' : 'Save as ruleset'}
              </button>
            )}
            <button type="button" className="btn btn-sm btn-ghost" onClick={resetDiagnosis}>Reset</button>
          </div>
          {coverageMessage && <span className="extractor-tester-note">{coverageMessage}</span>}
          {!automaticRepairSupported && <span className="extractor-tester-note">Automatic request replacement is unavailable because this diagnosis did not use server-side raw replay evidence.</span>}
          {externallyEdited && (
            <span className="extractor-tester-error">The editable actions were changed outside this diagnosis. The remaining fixes can no longer be applied safely — run a new replay and diagnose again.</span>
          )}
          {!externallyEdited && !replayMatchesCurrentScript && (
            <span className="extractor-tester-note">Applied fixes changed the script, so this replay no longer matches it. The remaining suggestions stay applicable; run a new replay before diagnosing again.</span>
          )}
          {currentSuggestions.map((suggestion, index) => {
            const appliedKey = suggestionIdentity(suggestion)
            const applicationResult = applicationResults.get(appliedKey)
            const canApply = !!suggestion.extractor && suggestion.source_step_id >= 0 && suggestion.source_step_id < actionCount
            const requiresManualReplacement = suggestion.review_action === 'manual_value_replacement_required'
            const hasLocalRepair = automaticRepairSupported && repairLocationsFromSuggestion(suggestion, replaySteps).length > 0
            const expectedAutomaticRepair = !requiresManualReplacement && typeof suggestion.failing_step_id === 'number' && hasLocalRepair
            const fullyApplied = !!applicationResult?.extractorAvailable && (!expectedAutomaticRepair || applicationResult.requestRepaired)
            const extractorOnly = !!applicationResult?.extractorAvailable && expectedAutomaticRepair && !applicationResult.requestRepaired
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
                {suggestion.extractor && (
                  <div className="extractor-tester-actions">
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => applySuggestion(suggestion)} disabled={!canApply || externallyEdited || !!applicationResult?.extractorAvailable}>
                      {fullyApplied ? (expectedAutomaticRepair ? 'Fix applied' : 'Extractor added') : extractorOnly ? 'Extractor added only' : 'Add as extractor'}
                    </button>
                    {!canApply && <span className="extractor-tester-note">Cannot map this fix to an editable action.</span>}
                    {canApply && requiresManualReplacement && (
                      <span className="extractor-tester-note">The recorded value is hidden; add the extractor, then replace the stale request value manually.</span>
                    )}
                    {canApply && !requiresManualReplacement && suggestion.failing_step_id !== undefined && !hasLocalRepair && (
                      <span className="extractor-tester-note">The extractor can be added, but this replay does not have raw evidence for a safe automatic request replacement.</span>
                    )}
                    {extractorOnly && (
                      <span className="extractor-tester-error">The extractor was added, but the mapped editable request span could not be updated. Replace that stale value manually.</span>
                    )}
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
