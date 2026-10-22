import { useEffect, useId, useState } from 'react'
import { analysisPoliciesApi, type AnalysisPolicyRecord } from '../hooks/api/analysisPoliciesApi'
import { TELEMETRY_POLICY_PROJECT_DEFAULT, telemetryPolicyIssue } from './telemetryAcceptanceChoice'

// TEL-B: a run is judged by its project's default telemetry acceptance policy
// unless it (or its scenario) chooses another of the project's policies. The
// controller validates the choice at save and run creation and pins its
// revision at Start; this select only offers the project's live policies.

type PolicyList =
  | { state: 'no_project' }
  | { state: 'loading' }
  | { state: 'loaded'; policies: AnalysisPolicyRecord[] }
  | { state: 'failed' }

function appliesByDefault(policy: AnalysisPolicyRecord) {
  const definition = policy.definition
  return typeof definition === 'object' && definition !== null && (definition as { apply_by_default?: unknown }).apply_by_default === true
}

interface Props {
  projectId?: string
  /** The chosen policy ID; empty uses the default (or, for a revised copy, keeps the source run's choice). */
  value: string
  onChange: (value: string) => void
  /** A revised copy's source run's choice: the empty value keeps it. */
  inheritedId?: string
  /** 'scenario' describes every run of a scenario; 'run' describes this run. */
  subject: 'scenario' | 'run'
  /** A change reloads the project's policies (after a save check found the list stale). */
  refreshKey?: number
  /** The scenario's stored choice: one the project lost is a warning, since a save that keeps it succeeds. */
  storedId?: string
}

export default function TelemetryPolicySelect({ projectId, value, onChange, inheritedId, subject, refreshKey, storedId }: Props) {
  const [list, setList] = useState<PolicyList>({ state: 'loading' })
  const descriptionId = useId()
  const issueId = useId()
  useEffect(() => {
    if (!projectId) {
      setList({ state: 'no_project' })
      return
    }
    const controller = new AbortController()
    setList({ state: 'loading' })
    analysisPoliciesApi.list(projectId, 'telemetry_acceptance_policy', controller.signal)
      .then(policies => {
        if (!controller.signal.aborted) setList({ state: 'loaded', policies })
      })
      .catch(() => {
        if (!controller.signal.aborted) setList({ state: 'failed' })
      })
    return () => controller.abort()
  }, [projectId, refreshKey])

  const loaded = list.state === 'loaded'
  const policies = loaded ? list.policies : []
  const projectDefault = policies.find(appliesByDefault)
  const defaultLabel = loaded
    ? `Project default${projectDefault ? ` (${projectDefault.name})` : ' (none set)'}`
    : 'Project default'
  const listed = (id: string) => policies.some(policy => policy.id === id)
  // A choice the list does not offer (deleted, or not loaded) stays visible
  // so saving never silently drops it.
  const label = (id: string) => {
    const name = policies.find(policy => policy.id === id)?.name ?? id
    return loaded && !listed(id) ? `${id} (no longer available)` : name
  }
  const chosen = value.trim()
  const unlisted = chosen && chosen !== TELEMETRY_POLICY_PROJECT_DEFAULT && !listed(chosen) ? chosen : ''
  const keptStored = Boolean(chosen) && chosen === storedId?.trim()
  const issue = !loaded || !telemetryPolicyIssue(chosen, policies, inheritedId)
    ? ''
    : keptStored
      ? `The scenario's telemetry acceptance policy "${chosen}" no longer exists in this project: runs created from it are refused until you choose another or the project default.`
      : telemetryPolicyIssue(chosen, policies, inheritedId)
  const description = subject === 'scenario'
    ? 'Every run of this scenario, including scheduled runs and execution plans, is judged by this policy unless the run names another. Its revision is pinned when the run starts.'
    : 'This run is judged by this policy. Its revision is pinned when the run starts.'

  return (
    <div className="scenario-subpanel" data-testid="telemetry-policy-select">
      <label className="scenario-field">
        <span>Telemetry acceptance policy</span>
        <select className="settings-input" value={chosen} onChange={event => onChange(event.target.value)}
          aria-describedby={issue ? `${descriptionId} ${issueId}` : descriptionId} aria-invalid={issue && !keptStored ? true : undefined}>
          {inheritedId
            ? <>
                <option value="">{`Keep the source run's choice (${label(inheritedId)})`}</option>
                <option value={TELEMETRY_POLICY_PROJECT_DEFAULT}>{defaultLabel}</option>
              </>
            : <option value="">{defaultLabel}</option>}
          {policies.map(policy => <option key={policy.id} value={policy.id}>{policy.name === policy.id ? policy.name : `${policy.name} (${policy.id})`}</option>)}
          {unlisted && <option value={unlisted}>{label(unlisted)}</option>}
        </select>
      </label>
      <p id={descriptionId} className="scenario-assignment-copy secondary">{description}</p>
      {list.state === 'failed' && <p className="settings-inline-notice" role="status">The project's telemetry acceptance policies could not be loaded; the current choice is kept.</p>}
      {/* Described by the select, not announced: a blocked save announces it in the page error. */}
      {issue && <p id={issueId} className={keptStored ? 'settings-inline-warning' : 'form-error'}>{issue}</p>}
    </div>
  )
}
