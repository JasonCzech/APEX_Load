import { lazy, Suspense, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { useProject } from '../hooks/useProject'
import { ScenarioValidationContext, StoredScript, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { dialogBackdropProps } from './dialogBackdropProps'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'
import { describeValidationContextChanges, scenarioValidationContextChanges } from './scriptAuthoringSession'
import DialogLayer from './ux/DialogLayer'
import LoadingState from './ux/LoadingState'
import Notice from './ux/Notice'

const loadCreateTestModal = () => import('./CreateTestModal')
const CreateTestModal = lazy(loadCreateTestModal)

interface Props {
  open: boolean
  test: TestSummary | null
  onClose: () => void
  onSelectTest: (testId: string) => void
}

// The editor opens only once the project's scripts are known: a failed or
// slow load must not read as a project without scripts. A retry keeps the
// failure on screen until its result arrives.
type ScriptsLoad =
  | { status: 'loading' }
  | { status: 'loaded'; scripts: StoredScript[] }
  | { status: 'failed'; failure: string; retrying: boolean }

export default function TestExecutionScenarioEditor({ open, test, onClose, onSelectTest }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scriptsLoad, setScriptsLoad] = useState<ScriptsLoad>({ status: 'loading' })
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [validatedContext, setValidatedContext] = useState<ScenarioValidationContext | null>(null)
  const [observedContext, setObservedContext] = useState<ScenarioValidationContext | null>(null)
  const probeInFlightRef = useRef(false)
  const lastProbeAtRef = useRef(0)
  const probeFailuresRef = useRef(0)
  const initialScenario = useMemo(() => (test ? buildScenarioDraftFromTest(test) : undefined), [test])
  const owningProjectId = test?.project_id || activeProject?.id

  const validationPayload = useMemo(() => {
    if (!initialScenario) return null
    return {
      project_id: owningProjectId,
      name: initialScenario.name,
      duration: initialScenario.duration,
      termination_mode: initialScenario.terminationMode,
      scenario_mode: initialScenario.scenarioMode,
      telemetry_acceptance_policy_id: initialScenario.telemetryAcceptancePolicyId,
      telemetry_budget_policy_id: initialScenario.telemetryBudgetPolicyId,
      runtime_settings: initialScenario.runtimeSettings,
      groups: initialScenario.groups.map(group => ({
        name: group.name,
        script_id: group.scriptId,
        vusers: group.vusers,
        ramp_up: group.rampUp,
        ramp_down: group.rampDown,
        initialization: group.initialization,
        runtime_settings: group.runtimeSettings,
      })),
    }
  }, [initialScenario, owningProjectId])

  const readValidationContext = useCallback(async (): Promise<ScenarioValidationContext | null> => {
    if (!validationPayload) return null
    if (typeof api.getScenarioValidationContext === 'function') {
      return (await api.getScenarioValidationContext(validationPayload)) ?? null
    }
    if (typeof api.validateScenarioLoadModel === 'function') {
      const report = await api.validateScenarioLoadModel(validationPayload)
      return report?.validation_context ?? null
    }
    return null
  }, [api, validationPayload])

  useEffect(() => {
    if (!open || scriptsLoad.status !== 'loaded' || !validationPayload) {
      setValidatedContext(null)
      setObservedContext(null)
      probeFailuresRef.current = 0
      return undefined
    }
    let cancelled = false
    void readValidationContext().then(context => {
      if (!cancelled && context) {
        setValidatedContext(context)
        setObservedContext(context)
        probeFailuresRef.current = 0
      }
    }).catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [open, readValidationContext, scriptsLoad.status, validationPayload])

  const probeValidationContext = useCallback(async () => {
    if (!open || scriptsLoad.status !== 'loaded' || !validatedContext) return
    const spacing = Math.min(5_000 * 2 ** probeFailuresRef.current, 60_000)
    if (probeInFlightRef.current || Date.now() - lastProbeAtRef.current < spacing) return
    probeInFlightRef.current = true
    lastProbeAtRef.current = Date.now()
    try {
      const next = await readValidationContext()
      if (!next) {
        probeFailuresRef.current += 1
        return
      }
      probeFailuresRef.current = 0
      setObservedContext(next)
    } catch {
      // Read failures on context recheck never mark held validation stale.
      probeFailuresRef.current += 1
    } finally {
      probeInFlightRef.current = false
    }
  }, [open, readValidationContext, scriptsLoad.status, validatedContext])

  useEffect(() => {
    if (!open || scriptsLoad.status !== 'loaded' || !validatedContext) return undefined
    const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
    const onFocus = () => { if (visible()) void probeValidationContext() }
    const onInterval = () => { if (typeof document === 'undefined' || document.hasFocus()) onFocus() }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onFocus)
    const timer = window.setInterval(onInterval, 60_000)
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onFocus)
      window.clearInterval(timer)
    }
  }, [open, probeValidationContext, scriptsLoad.status, validatedContext])

  const staleCauses = useMemo(
    () => scenarioValidationContextChanges(validatedContext, observedContext),
    [observedContext, validatedContext],
  )
  const staleValidationMessage = staleCauses.length > 0
    ? `Validation results no longer apply: ${describeValidationContextChanges(staleCauses)} changed since the check. Validate load model again.`
    : ''

  const recheckScenarioValidation = async () => {
    try {
      const next = await readValidationContext()
      if (next) {
        setValidatedContext(next)
        setObservedContext(next)
        probeFailuresRef.current = 0
      }
    } catch {
      // Keep the notice until a fresh context read succeeds.
    }
  }

  useEffect(() => {
    if (!open) return

    let cancelled = false
    setScriptsLoad(current => (current.status === 'loading' || (current.status === 'failed' && current.retrying) ? current : { status: 'loading' }))
    // Fetch the editor's code while its scripts load. A failed fetch surfaces
    // when the editor renders.
    loadCreateTestModal().catch(() => undefined)

    const loadScripts = async () => {
      try {
        const response = await api.listScripts(owningProjectId)
        if (!cancelled) {
          setScriptsLoad({ status: 'loaded', scripts: response.scripts || [] })
        }
      } catch (cause) {
        if (!cancelled) {
          setScriptsLoad({ status: 'failed', failure: getErrorMessage(cause, 'Scripts could not be loaded.'), retrying: false })
        }
      }
    }

    loadScripts()
    return () => {
      cancelled = true
    }
  }, [api, open, owningProjectId, loadAttempt])

  if (!open || !test || !initialScenario) {
    return null
  }

  if (scriptsLoad.status === 'loading') {
    return <LoadingState what="scenario editor" />
  }

  if (scriptsLoad.status === 'failed') {
    return (
      <ScriptsUnavailableDialog
        failure={scriptsLoad.failure}
        retrying={scriptsLoad.retrying}
        onRetry={() => {
          setScriptsLoad({ ...scriptsLoad, retrying: true })
          setLoadAttempt(attempt => attempt + 1)
        }}
        onClose={onClose}
      />
    )
  }

  return (
    <>
      {staleValidationMessage && (
        <div className="scenario-source-pending scenario-validation-stale-notice" role="status">
          <span>{staleValidationMessage}</span>
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => void recheckScenarioValidation()}>Recheck validation</button>
        </div>
      )}
      <Suspense fallback={<LoadingState what="scenario editor" />}>
        <CreateTestModal
          mode="revise-queued-run"
          projectId={owningProjectId}
          scripts={scriptsLoad.scripts}
          initialScenario={initialScenario}
          profileManaged={test.traffic_profile?.runtime_target === 'tps' && Boolean(test.traffic_profile.mappings?.length)}
          onClose={onClose}
          onCreated={(createdTest) => {
            if (createdTest?.id) {
              onSelectTest(createdTest.id)
            }
            onClose()
          }}
        />
      </Suspense>
    </>
  )
}

function ScriptsUnavailableDialog({ failure, retrying, onRetry, onClose }: { failure: string; retrying: boolean; onRetry: () => void; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const retryRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  useModalDialog({ dialogRef, initialFocusRef: retryRef, onClose })
  return (
    <DialogLayer>
      <div className="modal-overlay" {...dialogBackdropProps(onClose)}>
        <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
          <div className="modal-header">
            <h3 id={titleId}>Create revised queued run</h3>
            <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>Close</button>
          </div>
          <Notice
            tone="error"
            title="Scripts could not be loaded"
            details={failure}
            action={<button ref={retryRef} className="btn btn-secondary" type="button" disabled={retrying} onClick={onRetry}>{retrying ? 'Retrying…' : 'Retry'}</button>}
          >
            The scenario editor needs this project&apos;s scripts. Check your connection or access, then retry.
          </Notice>
        </div>
      </div>
    </DialogLayer>
  )
}
