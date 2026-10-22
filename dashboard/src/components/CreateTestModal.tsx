import MonitorRuleEditor from './MonitorRuleEditor'
import RunMonitorBindingSelector from './RunMonitorBindingSelector'
import type { MonitorBinding } from '../types/runMonitoring'
import DialogLayer from './ux/DialogLayer'
import CaptureSelectionHelp from './CaptureSelectionHelp'
import { legacyELKCaptureReady, captureSelectionNeedsReview, dynatraceCapabilities } from './captureConnectionModel'
import { useCallback, useContext, useEffect, useId, useMemo, useReducer, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { DraftCapacityEstimate, DynatraceConnection, ELKConnection, ScenarioDraft, ScenarioDraftGroup, StoredScript, SystemInventoryGenerator, TestSummary } from '../types'
import './CreateTestModal.css'
import { isValidDurationInput } from '../utils/timeUtils'
import CreateTestCapacityPreview from './CreateTestCapacityPreview'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import CreateTestGroupsEditor from './CreateTestGroupsEditor'
import TransactionResponseTimeSLASettings from './TransactionResponseTimeSLASettings'
import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import { getErrorMessage } from '../utils/errorUtils'
import { buildDefaultScenarioGroup, buildInitialScenarioDraft, scenarioDraftReducer, ScenarioDraftField, ScenarioDraftState } from './scenarioDraftUtils'
import { scenarioCreatePayload, scenarioEstimateRequest, scenarioLoadPreview } from './scenarioAssembly'
import DurationField from './DurationField'
import { NavigationGuardRegisterContext, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { hasGoalModeConfiguration, validateGoalConfig, validateTransactionResponseTimeSLAOverrides } from './goalConfigValidation'
import GroupPlacementEditor from './GroupPlacementEditor'
import { effectiveGroupVUsers, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'
import ConfiguredGeneratorChips from './ConfiguredGeneratorChips'
import { formatEstimate } from './generatorNames'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { confirmAction } from './ux/confirmAction'
import { BROWSER_ITERATION_CATEGORY, describeJointEvidence, jointPropertyCategory, promotedPropertyCategories } from './jointEvidenceModel'
import { AuthoringCreateNeedsNewIntent, authoringPayloadDigest, forgetAuthoringCreateIntent, nextAuthoringCreateIntent, sendUnderAuthoringCreateIntent, startingAuthoringCreateIntent, type AuthoringCreateIntent } from './authoringCreateIntent'

interface Props {
  scripts: StoredScript[]
  projectId?: string
  folderId?: string | null
  onClose: () => void
  onCreated: (test: TestSummary) => void
  initialScenario?: ScenarioDraft
  mode?: 'create-queued-run' | 'revise-queued-run'
  /** The run replays a TPS traffic profile: it is the throughput target, so none is offered. */
  profileManaged?: boolean
}

// The groups' protocols, for the goal ceilings they can produce; unknown
// (no group has a known script yet) offers them all.
function knownGroupProtocols(groups: ReadonlyArray<{ scriptId: string }>, scripts: StoredScript[]): string[] | undefined {
  const protocols = [...new Set(groups.map(group => {
    const script = scripts.find(candidate => candidate.id === group.scriptId)
    return script ? script.protocol || 'http' : ''
  }).filter(Boolean))]
  return protocols.length > 0 ? protocols : undefined
}

export default function CreateTestModal({ scripts, projectId, folderId, onClose, onCreated, initialScenario, mode = 'create-queued-run', profileManaged = false }: Props) {
  const api = useApi()
  const { runGuarded } = useNavigationGuardRunner()
  const registerGuard = useContext(NavigationGuardRegisterContext)
  const releaseOwnGuardRef = useRef<(() => void) | null>(null)
  const [scenarioDraft, dispatchScenarioDraft] = useReducer(
    scenarioDraftReducer,
    buildInitialScenarioDraft(initialScenario, scripts),
  )
  const submitDraftRef = useRef<() => Promise<boolean>>(async () => false)
  const submitOperationRef = useRef<Promise<boolean> | null>(null)
  const modalDirtyRef = useRef(false)
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [error, setError] = useState('')
  // The run this draft creates is one intent (AUTH-1): its key survives a
  // lost response and retries; only Save as new starts another.
  const createIntentRef = useRef<{ intent: AuthoringCreateIntent | null; fresh: boolean }>({ intent: null, fresh: false })
  // The error that refused this draft's key for good; Save as new is offered
  // with that error only.
  const [createNeedsNewIntent, setCreateNeedsNewIntent] = useState('')
  // A create that replayed the run an earlier attempt of this draft created:
  // no new run was queued, so it is reported rather than opened as new.
  const [replayedRun, setReplayedRun] = useState<TestSummary | null>(null)
  const [monitorEditorError, setMonitorEditorError] = useState('')
  const [loading, setLoading] = useState(false)
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const [captureRetry, setCaptureRetry] = useState(0)
  const [canManageConnections, setCanManageConnections] = useState(false)
  const [elkLoading, setELKLoading] = useState(false)
  const [elkError, setELKError] = useState(false)
  const [dynatraceLoading, setDynatraceLoading] = useState(false)
  const [dynatraceError, setDynatraceError] = useState(false)
  const [elkConnections, setELKConnections] = useState<ELKConnection[]>([])
  const [elkConnectionId, setELKConnectionId] = useState(() => initialScenario?.revisionContext?.elkConnectionId ?? '')
  const [elasticMonitorConflict, setElasticMonitorConflict] = useState(false)
  const [dynatraceConnections, setDynatraceConnections] = useState<DynatraceConnection[]>([])
  const [dynatraceConnectionId, setDynatraceConnectionId] = useState(() => initialScenario?.revisionContext?.dynatraceConnectionId ?? '')
  const [monitorBindings, setMonitorBindings] = useState<MonitorBinding[] | undefined>(() => initialScenario?.revisionContext?.monitorBindings?.map(binding => ({ ...binding })))
  const authoringFingerprint = JSON.stringify([scenarioDraft, monitorBindings, elkConnectionId, dynatraceConnectionId])
  const latestAuthoringFingerprintRef = useRef(authoringFingerprint)
  latestAuthoringFingerprintRef.current = authoringFingerprint
  const baselineAuthoringFingerprintRef = useRef(authoringFingerprint)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const scenarioNameRef = useRef<HTMLInputElement | null>(null)
  const titleId = useId()
  const mountedRef = useRef(false)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const { name, duration, terminationMode, startDelay, scenarioMode, scriptShakeout, resourceBaseline, assignment, groups, goalConfig, runtimeSettings, revisionContext } = scenarioDraft
  const isReviseMode = mode === 'revise-queued-run'
  // A kept property category that this modal's group edits stop promoting:
  // the controller refuses a copy whose groups no longer promote it.
  // Unchanged groups keep the source run's script snapshots, which the
  // library cannot show, so only the edits are judged here, both sides
  // against the library's current scripts; the controller decides.
  const revisionCategory = revisionContext?.jointEvidence?.dimensions.map(jointPropertyCategory).find((name): name is string => name !== null && name !== BROWSER_ITERATION_CATEGORY) ?? null
  const promotionSource = (draft: { groups: ReadonlyArray<{ scriptId: string; runtimeSettings?: ScenarioDraft['runtimeSettings'] }>; runtimeSettings?: ScenarioDraft['runtimeSettings'] }) => ({ groups: draft.groups.map(group => ({ script_id: group.scriptId, runtimeSettings: group.runtimeSettings })), runtime_settings: draft.runtimeSettings })
  const revisionCategoryNote = revisionCategory && initialScenario
    && promotedPropertyCategories(promotionSource(initialScenario), scripts).includes(revisionCategory)
    && !promotedPropertyCategories(promotionSource({ groups, runtimeSettings }), scripts).includes(revisionCategory)
    ? `These changes stop promoting "${revisionCategory}": restore its promotion, or the copy may be refused.`
    : ''
  const modalTitle = isReviseMode ? 'Create revised queued run' : 'Create queued run'
  const submitLabel = isReviseMode ? 'Create revised copy' : 'Create queued run'
  const loadingLabel = isReviseMode ? 'Creating revised copy…' : 'Creating queued run…'
  const errorMessage = isReviseMode ? 'Failed to create revised queued run' : 'Failed to create queued run'

  const updateScenarioField = <K extends ScenarioDraftField>(field: K, value: ScenarioDraftState[K]) => {
    dispatchScenarioDraft({ type: 'setField', field, value })
  }

  const handleGuardedClose = useCallback(() => {
    runGuarded(onClose)
  }, [onClose, runGuarded])

  useModalDialog({
    dialogRef,
    initialFocusRef: scenarioNameRef,
    onClose: loading ? undefined : handleGuardedClose,
  })

  const updateGroup = (idx: number, patch: Partial<ScenarioDraftGroup>) => {
    dispatchScenarioDraft({ type: 'updateGroup', index: idx, patch })
  }

  const addGroup = () => {
    dispatchScenarioDraft({ type: 'addGroup', group: buildDefaultScenarioGroup(scripts) })
  }

  const updateGoalConfig = <K extends keyof ScenarioDraftState['goalConfig']>(field: K, value: ScenarioDraftState['goalConfig'][K]) => {
    dispatchScenarioDraft({ type: 'setGoalConfigField', field, value })
  }

  const replaceRuntimeSettings = (value: ScenarioDraftState['runtimeSettings']) => {
    dispatchScenarioDraft({ type: 'replaceRuntimeSettings', value })
  }

  const removeGroup = (idx: number) => {
    dispatchScenarioDraft({ type: 'removeGroup', index: idx })
  }

  const totalVUsers = groups.reduce((sum, group) => sum + effectiveGroupVUsers(group), 0)
  const assignmentRecommendation = draftEstimate?.assignment_recommendation
  const availableTargets = assignmentRecommendation?.available_targets || []
  const availableLabels = assignmentRecommendation?.available_labels || {}
  const footprint = draftEstimate?.footprint
  const hasGoalModeConfig = hasGoalModeConfiguration(goalConfig)
  const effectiveProjectId = revisionContext?.projectId ?? projectId
  const captureProjectRef = useRef(effectiveProjectId)
  useEffect(() => {
    let active = true
    setCanManageConnections(false)
    if (effectiveProjectId && typeof api.getAuthEffectiveAccess === 'function') {
      void api.getAuthEffectiveAccess(effectiveProjectId).then(result => {
        if (active) setCanManageConnections(isEffectiveOperationAllowed(result.resources || [], 'manage_projects', false) || isEffectiveOperationAllowed(result.resources || [], 'manage_system', false))
      }).catch(() => { /* Keep administration links hidden if access cannot be verified. */ })
    }
    return () => { active = false }
  }, [api, effectiveProjectId])
  useEffect(() => {
    if (captureProjectRef.current === effectiveProjectId) return
    captureProjectRef.current = effectiveProjectId
    setELKConnectionId('')
    setDynatraceConnectionId('')
    setELKConnections([])
    setDynatraceConnections([])
  }, [effectiveProjectId])
  const captureNeedsReview = captureSelectionNeedsReview(elkConnectionId, elkConnections, elkLoading, elkError) || captureSelectionNeedsReview(dynatraceConnectionId, dynatraceConnections, dynatraceLoading, dynatraceError)

  useEffect(() => {
    const listELKConnections = api.listELKConnections
    if (!effectiveProjectId || typeof listELKConnections !== 'function') {
      setELKConnections([])
      setELKLoading(false)
      setELKError(Boolean(effectiveProjectId))
      return
    }
    let active = true
    setELKLoading(true); setELKError(false)
    listELKConnections(effectiveProjectId).then(result => {
      if (!active) return
      const ready = result.connections.filter(legacyELKCaptureReady)
      setELKConnections(ready)
      // Retain an unavailable selection until the user explicitly reviews it.
    }).catch(() => {
      if (!active) return
      setELKConnections([])
      setELKError(true)
    }).finally(() => { if (active) setELKLoading(false) })
    return () => { active = false }
  }, [api, effectiveProjectId, captureRetry])

  useEffect(() => {
    const list = api.listDynatraceConnections
    if (!effectiveProjectId || typeof list !== 'function') { setDynatraceConnections([]); setDynatraceLoading(false); setDynatraceError(Boolean(effectiveProjectId)); return }
    let active = true
    setDynatraceLoading(true); setDynatraceError(false)
    list(effectiveProjectId).then(result => { if (!active) return; const ready = result.connections.filter(connection => (connection.legacy_capture_status ?? connection.status) === 'ready'); setDynatraceConnections(ready);  }).catch(() => { if (active) { setDynatraceConnections([]); setDynatraceError(true) } }).finally(() => { if (active) setDynatraceLoading(false) })
    return () => { active = false }
  }, [api, effectiveProjectId, captureRetry])

  // The chart draws the draft's load preview: the projection every scenario
  // editor shares (AUTH-3), so offsets, stages and baselines match.
  const loadPlan = scenarioLoadPreview(scenarioDraft)
  const buildPayload = () => scenarioCreatePayload(scenarioDraft, { projectId, folderId, monitorBindings, elkConnectionId, dynatraceConnectionId })

  const buildEstimatePayload = useCallback(
    () => scenarioEstimateRequest(scenarioDraft, { projectId, folderId, monitorBindings }),
    [folderId, monitorBindings, projectId, scenarioDraft],
  )

  useEffect(() => {
    const canEstimate = (terminationMode === 'iterations' || Boolean(duration.trim())) && groups.every((group) => group.scriptId)
    if (!canEstimate) {
      setDraftEstimate(null)
      setEstimateError('')
      setEstimateLoading(false)
      return
    }

    let cancelled = false
    setEstimateLoading(true)
    setEstimateError('')
    const timer = window.setTimeout(async () => {
      try {
        const estimate = await api.estimateTest(buildEstimatePayload())
        if (!cancelled) {
          setDraftEstimate(estimate)
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setDraftEstimate(null)
          setEstimateError(getErrorMessage(error, 'Unable to estimate load generator requirements'))
        }
      } finally {
        if (!cancelled) {
          setEstimateLoading(false)
        }
      }
    }, 300)

    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [api, buildEstimatePayload, groups, duration, terminationMode])

  useEffect(() => {
    let cancelled = false
    api.getSystemInventory()
      .then(response => {
        if (!cancelled) setGeneratorInventory(response.generators || [])
      })
      .catch(() => {
        if (!cancelled) setGeneratorInventory([])
      })
    return () => {
      cancelled = true
    }
  }, [api])

  const performSubmitDraft = async (): Promise<boolean> => {
    if (!mountedRef.current) return false
    setCreateNeedsNewIntent('')
    if (monitorEditorError) { setError(monitorEditorError); return false }
    if (elasticMonitorConflict) { setError('Turn legacy Elastic log capture off or remove the conflicting run-bound source before launching.'); return false }
    if (captureNeedsReview) { setError('Review the unavailable capture connection, retry, or explicitly turn capture off before launching.'); return false }
    setError('')
    // A replay notice stays until another run is queued: a later refusal of a
    // changed draft does not change that this draft's run exists.
    setLoading(true)
    try {
      if (terminationMode === 'iterations' ? duration !== '' : !isValidDurationInput(duration)) {
        throw new Error(terminationMode === 'iterations' ? 'Iteration completion must not specify a duration.' : 'Enter a positive steady state duration.')
      }
      const rootAssignmentError = validateGeneratorAssignment(assignment, 'scenario', 'Scenario assignment')
      if (rootAssignmentError) throw new Error(rootAssignmentError)
      for (let index = 0; index < groups.length; index += 1) {
        const group = groups[index]
        const script = scripts.find(item => item.id === group.scriptId)
        const groupError = validateGroupExecutionEnvelope({
          role: group.role,
          protocol: script?.protocol,
          vusers: effectiveGroupVUsers(group),
          label: group.name || `Group ${index + 1}`,
        })
        if (groupError) throw new Error(groupError)
        const assignmentError = validateGeneratorAssignment(group.assignment, 'group', group.name || `Group ${index + 1}`)
        if (assignmentError) throw new Error(assignmentError)
      }
      const aggregateError = validateAggregateVUserDemand(groups)
      if (aggregateError) throw new Error(aggregateError)
      const transactionSLAError = validateGoalConfig(goalConfig) || validateTransactionResponseTimeSLAOverrides(groups)
      if (transactionSLAError) throw new Error(transactionSLAError)
      const payload = buildPayload()

      if (scenarioMode === 'goal_oriented') {
        if (!hasGoalModeConfig) {
          throw new Error('Goal-oriented scenarios require at least one goal or guardrail setting')
        }
      }

      const submittedFingerprint = authoringFingerprint
      const intentProject = effectiveProjectId || ''
      // A revised copy's key belongs to its source run: two queued runs revised
      // with the same settings are different decisions.
      const digest = authoringPayloadDigest(revisionContext?.sourceTestId ? { payload, source_test_id: revisionContext.sourceTestId } : payload)
      const intentOwner = createIntentRef.current
      const intent = nextAuthoringCreateIntent(startingAuthoringCreateIntent(intentOwner.intent, 'test_create', intentProject, digest, Date.now(), intentOwner.fresh), 'test_create', intentProject, digest)
      const created = await sendUnderAuthoringCreateIntent(intent, { update: next => { intentOwner.intent = next; intentOwner.fresh = false } }, 'run',
        key => api.createTestKeyed(payload, key),
        // A replay leaves the key with this draft: creating again replays the
        // same run (a changed draft is refused) and only Save as new queues
        // another.
        async outcome => ({ done: mountedRef.current && outcome.kind === 'created', value: outcome }))
      if (!mountedRef.current) return false
      const test = created.resource
      if (created.kind === 'replayed') {
        // The outcome is known, so a reload need not recall the key; it
        // stays with this open draft only.
        forgetAuthoringCreateIntent(intentOwner.intent)
        // The draft's run already exists: retire the guard for this
        // submission and say so, without presenting it as a new run.
        baselineAuthoringFingerprintRef.current = submittedFingerprint
        modalDirtyRef.current = latestAuthoringFingerprintRef.current !== submittedFingerprint
        setReplayedRun(test)
        return false
      }
      // Successful creation durably accepts this submitted draft. Retire only
      // its guard before the parent attempts guarded navigation synchronously.
      setReplayedRun(null)
      baselineAuthoringFingerprintRef.current = submittedFingerprint
      if (latestAuthoringFingerprintRef.current !== submittedFingerprint) {
        // Fields remain editable while saving. The successful receipt belongs
        // to the submitted snapshot, so later edits retain their own guard.
        modalDirtyRef.current = true
        setError(`Queued run ${test.ref_key || test.id} was created. Edits made while saving remain unsaved; review them before creating another run.`)
        return false
      }
      modalDirtyRef.current = false
      releaseOwnGuardRef.current?.()
      releaseOwnGuardRef.current = null
      onCreated(test)
      return true
    } catch (error: unknown) {
      if (mountedRef.current) {
        const message = error instanceof AuthoringCreateNeedsNewIntent && error.reason === 'changed' && replayedRun
          ? `This draft already created run ${replayedRun.ref_key || replayedRun.id}, and it has changed since. To queue another run with these changes, choose Save as new.`
          : getErrorMessage(error, errorMessage)
        if (error instanceof AuthoringCreateNeedsNewIntent) setCreateNeedsNewIntent(message)
        // The replayed run is gone: its notice and Open run no longer apply.
        if (error instanceof AuthoringCreateNeedsNewIntent && error.reason === 'deleted') setReplayedRun(null)
        setError(message)
      }
      return false
    } finally {
      if (mountedRef.current) setLoading(false)
    }
  }
  const submitDraft = (): Promise<boolean> => {
    // The form button and the navigation guard share this instance-owned
    // operation. Join the captured submission, including its preflight and
    // late-edit result; a history guard must never launch a second copy.
    if (submitOperationRef.current) return submitOperationRef.current
    if (!mountedRef.current) return Promise.resolve(false)
    const operation = Promise.resolve().then(performSubmitDraft)
    submitOperationRef.current = operation
    const retire = () => { if (submitOperationRef.current === operation) submitOperationRef.current = null }
    // Handle both settlements explicitly; an ignored rejecting finally chain
    // would otherwise create a second, unhandled promise.
    void operation.then(retire, retire)
    return operation
  }
  submitDraftRef.current = submitDraft
  // An explicit new intent: the next create sends a new key.
  const saveAsNew = () => {
    forgetAuthoringCreateIntent(createIntentRef.current.intent)
    createIntentRef.current = { intent: null, fresh: true }
    setCreateNeedsNewIntent('')
    setReplayedRun(null)
    void submitDraft()
  }
  const modalDirty = authoringFingerprint !== baselineAuthoringFingerprintRef.current
  modalDirtyRef.current = modalDirty
  const modalGuard = useMemo<NavigationGuard>(() => ({
    id: `create-test-modal-${mode}-${titleId}`,
    label: modalTitle,
    isDirty: () => modalDirtyRef.current,
    save: () => submitDraftRef.current(),
    discard: () => undefined,
  }), [modalTitle, mode, titleId])
  useEffect(() => {
    const release = registerGuard(modalGuard, modalDirty)
    releaseOwnGuardRef.current = release
    return () => {
      release()
      if (releaseOwnGuardRef.current === release) releaseOwnGuardRef.current = null
    }
  }, [modalDirty, modalGuard, registerGuard])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    await submitDraft()
  }

  return (
    <DialogLayer><div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget && !loading) handleGuardedClose()
      }}
    >
      <div ref={dialogRef} className="modal modal-wide" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <h3 id={titleId}>{modalTitle}</h3>
          <span className="total-vusers">{totalVUsers} total VUsers</span>
        </div>
        <form onSubmit={handleSubmit} className="modal-form">
          {isReviseMode && (
            <p className="page-subtitle">Saving here creates a new queued run. The current queued run remains unchanged.</p>
          )}
          <div className="form-row">
            <div className="form-column flex-2">
              <div className="scenario-mode-switcher">
                <button
                  type="button"
                  className={`mode-chip ${scenarioMode === 'vuser_driven' ? 'active' : ''}`}
                  onClick={() => updateScenarioField('scenarioMode', 'vuser_driven')}
                >
                  VUser-Driven
                </button>
                <button
                  type="button"
                  className={`mode-chip ${scenarioMode === 'goal_oriented' ? 'active' : ''}`}
                  onClick={() => updateScenarioField('scenarioMode', 'goal_oriented')}
                >
                  Goal-Oriented
                </button>
              </div>
              <label>
                <span>Scenario Name</span>
                <input
                  ref={scenarioNameRef}
                  type="text"
                  value={name}
                  onChange={(e) => updateScenarioField('name', e.target.value)}
                  placeholder="Optional — auto-generated if empty"
                />
              </label>
              {isReviseMode && <p><strong>Completion:</strong> {terminationMode === 'iterations' ? 'All per-user iterations' : 'Duration'}. The source completion mode is retained.</p>}
              {terminationMode === 'iterations' && <p>Each fixed VUser completes its configured finite iterations. The controller validates the final script and runtime iteration counts; no steady state duration is submitted.</p>}
              <DurationField
                label="Steady State Duration"
                value={duration}
                required={terminationMode !== 'iterations'}
                disabled={terminationMode === 'iterations'}
                allowEmpty={terminationMode === 'iterations'}
                showPresets
                onChange={(value) => updateScenarioField('duration', value)}
              />
              <DurationField
                label="Global Start Delay"
                value={startDelay}
                allowEmpty
                showPresets
                onChange={(value) => updateScenarioField('startDelay', value)}
              />
              <label className="setting-checkbox-row">
                <span>Pre-Run Script Shakeout</span>
                <div className="setting-checkbox-control">
                  <input
                    type="checkbox"
                    checked={scriptShakeout}
                    onChange={(e) => updateScenarioField('scriptShakeout', e.target.checked)}
                  />
                  <p>Run one instance of each script before full scale startup. Any failure halts the scenario and raises an alert.</p>
                </div>
              </label>
              <label className="setting-checkbox-row">
                <span>Resource baseline</span>
                <div className="setting-checkbox-control">
                  <input
                    type="checkbox"
                    checked={resourceBaseline}
                    onChange={(e) => updateScenarioField('resourceBaseline', e.target.checked)}
                  />
                  <p>Add a fixed 5 minute baseline before load starts and another 5 minute baseline after load stops so resource utilization can be observed without user traffic.</p>
                </div>
              </label>
              <label>
                <span>Elastic log capture</span>
                <select value={elkConnectionId} onChange={event => setELKConnectionId(event.target.value)}>
                  <option value="">Off for this run</option>
                  {elkConnectionId && !elkConnections.some(c => c.id === elkConnectionId) && <option value={elkConnectionId}>Selected connection unavailable — review required</option>}
                  {elkConnections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}
                </select>
                <small>Optional. The controller polls the selected ready connection; load generators are unaffected.</small>
              </label>
              <label>
                <span>Dynatrace SaaS capture</span>
                <select value={dynatraceConnectionId} onChange={event => setDynatraceConnectionId(event.target.value)}>
                  <option value="">Off for this run</option>
                  {dynatraceConnectionId && !dynatraceConnections.some(c => c.id === dynatraceConnectionId) && <option value={dynatraceConnectionId}>Selected connection unavailable — review required</option>}
                  {dynatraceConnections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}
                </select>
                <small>Optional. Metrics, problems, and Grail logs are polled by the controller.</small>
              </label>
              <CaptureSelectionHelp canManage={canManageConnections} provider="elk" count={elkConnections.length} error={elkError} loading={elkLoading} retry={() => setCaptureRetry(value => value + 1)} selected={elkConnections.find(c => c.id === elkConnectionId)?.name} />
              <CaptureSelectionHelp canManage={canManageConnections} provider="dynatrace" capabilities={dynatraceCapabilities(dynatraceConnections.find(c => c.id === dynatraceConnectionId))} count={dynatraceConnections.length} error={dynatraceError} loading={dynatraceLoading} retry={() => setCaptureRetry(value => value + 1)} selected={dynatraceConnections.find(c => c.id === dynatraceConnectionId)?.name} />
            </div>

            <CreateTestCapacityPreview
              plan={loadPlan}
              groups={groups}
              draftEstimate={draftEstimate}
              estimateLoading={estimateLoading}
              estimateError={estimateError}
            />
          </div>

            <section className="assignment-settings">
              <div className="assignment-settings-header">
                <div>
                  <h4>Controller / generator assignment</h4>
                  <p>Overall size is derived from the scenario footprint, current generator capacity profile, and the configured distributed targets.</p>
                </div>
              </div>

              {footprint && (
                <div className="assignment-footprint-grid">
                  <div className="assignment-footprint-card">
                    <span>Footprint</span>
                    <strong>{footprint.total_vusers} VUsers</strong>
                    <small>{footprint.duration} steady state</small>
                  </div>
                  <div className="assignment-footprint-card">
                    <span>Complexity</span>
                    <strong>{footprint.complexity_score.toFixed(2)}</strong>
                    <small>script design score</small>
                  </div>
                  <div className="assignment-footprint-card">
                    <span>Estimated load</span>
                    <strong>{formatEstimate(footprint.estimated_cpu_cores)} CPU</strong>
                    <small>{Math.round(footprint.estimated_memory_mb)} MB / {Math.round(footprint.estimated_network_mbps)} Mbps</small>
                  </div>
                </div>
              )}

              {assignmentRecommendation?.summary && (
                <p className="assignment-copy">{assignmentRecommendation.summary}</p>
              )}

              <GroupPlacementEditor
                assignment={assignment}
                onChange={next => updateScenarioField('assignment', next || { mode: 'auto' })}
                availableTargets={availableTargets}
                generatorInventory={generatorInventory}
                availableLabels={availableLabels}
                allowInherit={false}
                scope="scenario"
              />

              {(!assignment.mode || assignment.mode === 'auto') && <ConfiguredGeneratorChips targets={availableTargets} inventory={generatorInventory} rowClassName="assignment-chip-row" chipClassName="assignment-chip" labelClassName="assignment-chip-label" />}

              {assignmentRecommendation?.warnings && assignmentRecommendation.warnings.length > 0 && (
                <div className="assignment-warnings">
                  {assignmentRecommendation.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </section>

          {scenarioMode === 'goal_oriented' && (
            <CreateTestGoalSettings
              protocols={knownGroupProtocols(groups, scripts)}
              profileManaged={profileManaged || (scenarioDraft.trafficProfile?.runtime_target === 'tps' && scenarioDraft.trafficProfile.mappings.length > 0)}
              goalConfig={goalConfig}
              onGoalConfigChange={updateGoalConfig}
            />
          )}

          <CreateTestGroupsEditor
            scenarioMode={scenarioMode}
            groups={groups}
            scripts={scripts}
            onAddGroup={addGroup}
            onUpdateGroup={updateGroup}
            onRemoveGroup={removeGroup}
            availableTargets={availableTargets}
            generatorInventory={generatorInventory}
            availableLabels={availableLabels}
          />

			<TransactionResponseTimeSLASettings
				defaultMs={goalConfig.transaction_response_time_sla_ms}
				groups={groups.map((group, index) => ({
					name: group.name || `Group ${index + 1}`,
					scriptName: scripts.find(script => script.id === group.scriptId)?.name,
					overrideMs: group.transactionResponseTimeSlaMs,
				}))}
				onDefaultChange={value => updateGoalConfig('transaction_response_time_sla_ms', value)}
				onGroupOverrideChange={(index, value) => updateGroup(index, { transactionResponseTimeSlaMs: value })}
			/>

          {revisionContext?.jointEvidence && (
            <p className="settings-inline-notice" data-testid="revision-joint-evidence">
              {`Joint evidence: ${describeJointEvidence(revisionContext.jointEvidence)}. The revised copy keeps the source run's projection.`}
              {revisionCategoryNote ? ` ${revisionCategoryNote}` : ''}
            </p>
          )}
          <RunMonitorBindingSelector projectId={revisionContext?.projectId ?? projectId} scenarioId={revisionContext?.scenarioId} legacyELKConnectionId={elkConnectionId} onConflictChange={setElasticMonitorConflict} value={monitorBindings} onChange={setMonitorBindings} />
          <MonitorRuleEditor projectId={revisionContext?.projectId ?? projectId} value={goalConfig.monitor_rules || []} onChange={rules => updateGoalConfig('monitor_rules', rules)} onValidationChange={setMonitorEditorError} />
          <CreateTestRuntimeOverrides
            draftScope={`${revisionContext?.projectId ?? projectId ?? ''}:${revisionContext?.scenarioId ?? 'new'}`}
            runtimeSettings={runtimeSettings}
            groupProtocols={groups.map(group => scripts.find(script => script.id === group.scriptId)?.protocol)}
            onReplaceRuntimeSettings={replaceRuntimeSettings}
          />

          {replayedRun && (
            <p className="settings-inline-notice" role="status">
              {`This draft already created run ${replayedRun.ref_key || replayedRun.id} (${replayedRun.status || 'status unknown'}); no new run was queued. To queue another, choose Save as new.`}
              <button type="button" className="btn btn-sm btn-secondary" onClick={async () => {
                // Edits made since the replayed submission are not saved by
                // opening the existing run: only discarding them leaves.
                if (latestAuthoringFingerprintRef.current !== baselineAuthoringFingerprintRef.current
                  && !(await confirmAction(`Discard the edits made since and open run ${replayedRun.ref_key || replayedRun.id}?`, { title: 'Discard edits', confirmLabel: 'Discard edits', destructive: true }))) return
                if (!mountedRef.current) return
                modalDirtyRef.current = false
                releaseOwnGuardRef.current?.()
                releaseOwnGuardRef.current = null
                onCreated(replayedRun)
              }}>Open run</button>
              <button type="button" className="btn btn-sm btn-secondary" disabled={loading} onClick={saveAsNew}>Save as new</button>
            </p>
          )}
          {error && <p className="form-error" role="alert">
            {error}
            {/* The replay notice above carries its own Save as new. */}
            {createNeedsNewIntent && error === createNeedsNewIntent && !replayedRun && (
              <button type="button" className="btn btn-sm btn-secondary" disabled={loading} onClick={saveAsNew}>Save as new</button>
            )}
          </p>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={handleGuardedClose} disabled={loading}>
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={loading || captureNeedsReview || elasticMonitorConflict || Boolean(monitorEditorError) || groups.every((g) => !g.scriptId)}
            >
              {loading ? loadingLabel : `${submitLabel} (${groups.length} group${groups.length > 1 ? 's' : ''})`}
            </button>
          </div>
        </form>
      </div>
    </div></DialogLayer>
  )
}
