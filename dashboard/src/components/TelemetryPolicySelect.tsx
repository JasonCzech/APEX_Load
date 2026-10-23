import { useEffect, useId, useState } from 'react'
import { analysisPoliciesApi, type AnalysisPolicyRecord } from '../hooks/api/analysisPoliciesApi'
import { TELEMETRY_POLICY_CHOICE, TELEMETRY_POLICY_PROJECT_DEFAULT, telemetryPolicyIssue, telemetryPolicyProblem, telemetryPolicyUnusableReason, type TelemetryPolicyChoiceKind } from './telemetryAcceptanceChoice'

// TEL-B: a run is judged by its project's default telemetry acceptance policy
// unless it (or its scenario) chooses another of the project's policies, and
// takes its live budgets from the system threshold settings unless it (or its
// scenario) chooses one of the project's telemetry budget policies. The
// controller validates each choice at save and run creation and pins its
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

const COPY: Record<TelemetryPolicyChoiceKind, { label: string; testId: string; listNoun: string; scenario: string; run: string; change: string }> = {
  acceptance: {
    label: 'Telemetry acceptance policy',
    testId: 'telemetry-policy-select',
    listNoun: 'telemetry acceptance policies',
    scenario: 'Every run of this scenario, including scheduled runs and execution plans, is judged by this policy unless the run names another. Its revision is pinned when the run starts.',
    run: 'This run is judged by this policy. Its revision is pinned when the run starts.',
    change: 'This run is judged by this policy.',
  },
  budget: {
    label: 'Telemetry budget policy',
    testId: 'telemetry-budget-policy-select',
    listNoun: 'telemetry budget policies',
    scenario: 'Every run of this scenario, including scheduled runs and execution plans, takes its live telemetry budgets from this policy unless the run names another; without one, runs use the system threshold settings. Its revision is pinned when the run starts.',
    run: 'This run takes its live telemetry budgets from this policy; without one, it uses the system threshold settings. Its revision is pinned when the run starts.',
    change: "From the moment the change is stored, this running test's live telemetry is classified by this policy's budgets; without one, by the current threshold settings. Intervals already classified keep their classification, and the interval the change falls inside belongs to no segment: its budgets are not judged.",
  },
}

interface Props {
  /** Which choice this select edits; the acceptance policy by default. */
  kind?: TelemetryPolicyChoiceKind
  projectId?: string
  /** The chosen policy ID; empty uses the fallback (or, for a revised copy, keeps the source run's choice). */
  value: string
  onChange: (value: string) => void
  /** A revised copy's source run's choice: the empty value keeps it. */
  inheritedId?: string
  /** 'scenario' describes every run of a scenario; 'run' describes this run; 'change' a running test's change. */
  subject: 'scenario' | 'run' | 'change'
  /** A change reloads the project's policies (after a save check found the list stale). */
  refreshKey?: number
  /** The scenario's stored choice: one the project lost is a warning, since a save that keeps it succeeds. */
  storedId?: string
}

export default function TelemetryPolicySelect({ kind = 'acceptance', projectId, value, onChange, inheritedId, subject, refreshKey, storedId }: Props) {
  const [list, setList] = useState<PolicyList>({ state: 'loading' })
  const descriptionId = useId()
  const issueId = useId()
  const { apiKind, noun, fallback } = TELEMETRY_POLICY_CHOICE[kind]
  const copy = COPY[kind]
  useEffect(() => {
    if (!projectId) {
      setList({ state: 'no_project' })
      return
    }
    const controller = new AbortController()
    setList({ state: 'loading' })
    analysisPoliciesApi.list(projectId, apiKind, controller.signal)
      .then(policies => {
        if (!controller.signal.aborted) setList({ state: 'loaded', policies })
      })
      .catch(() => {
        if (!controller.signal.aborted) setList({ state: 'failed' })
      })
    return () => controller.abort()
  }, [projectId, refreshKey, apiKind])

  const loaded = list.state === 'loaded'
  const policies = loaded ? list.policies : []
  // A project's default budget policy is pinned only: without a choice a run
  // uses the system threshold settings, so only acceptance names a default.
  const projectDefault = kind === 'acceptance' ? policies.find(appliesByDefault) : undefined
  const defaultLabel = kind === 'budget'
    ? 'System threshold settings'
    : loaded
      ? `Project default${projectDefault ? ` (${projectDefault.name})` : ' (none set)'}`
      : 'Project default'
  const listed = (id: string) => policies.some(policy => policy.id === id)
  // A choice the list does not offer (deleted, or not loaded) stays visible
  // so saving never silently drops it.
  const label = (id: string) => {
    const name = policies.find(policy => policy.id === id)?.name ?? id
    return loaded && !listed(id) ? `${id} (no longer available)` : name
  }
  const optionLabel = (policy: AnalysisPolicyRecord) => {
    const name = policy.name === policy.id ? policy.name : `${policy.name} (${policy.id})`
    return telemetryPolicyUnusableReason(policy, kind) ? `${name} (unusable: ${telemetryPolicyUnusableReason(policy, kind)})` : name
  }
  const chosen = value.trim()
  const unlisted = chosen && chosen !== TELEMETRY_POLICY_PROJECT_DEFAULT && !listed(chosen) ? chosen : ''
  const keptStored = Boolean(chosen) && chosen === storedId?.trim()
  const issue = !loaded || !telemetryPolicyIssue(chosen, policies, inheritedId, kind)
    ? ''
    : keptStored
      ? `The scenario's ${noun} "${chosen}" ${telemetryPolicyProblem(chosen, policies, kind)}: runs created from it are refused until you choose another or ${fallback}.`
      : telemetryPolicyIssue(chosen, policies, inheritedId, kind)

  return (
    <div className="scenario-subpanel" data-testid={copy.testId}>
      <label className="scenario-field">
        <span>{copy.label}</span>
        <select className="settings-input" value={chosen} onChange={event => onChange(event.target.value)}
          aria-describedby={issue ? `${descriptionId} ${issueId}` : descriptionId} aria-invalid={issue && !keptStored ? true : undefined}>
          {inheritedId
            ? <>
                <option value="">{`Keep the source run's choice (${label(inheritedId)})`}</option>
                <option value={TELEMETRY_POLICY_PROJECT_DEFAULT}>{defaultLabel}</option>
              </>
            : <option value="">{defaultLabel}</option>}
          {/* An unusable policy stays selectable only while it is the choice, so the issue can name it. */}
          {policies.map(policy => <option key={policy.id} value={policy.id} disabled={Boolean(telemetryPolicyUnusableReason(policy, kind)) && policy.id !== chosen}>{optionLabel(policy)}</option>)}
          {unlisted && <option value={unlisted}>{label(unlisted)}</option>}
        </select>
      </label>
      <p id={descriptionId} className="scenario-assignment-copy secondary">{subject === 'scenario' ? copy.scenario : subject === 'change' ? copy.change : copy.run}</p>
      {list.state === 'failed' && <p className="settings-inline-notice" role="status">{`The project's ${copy.listNoun} could not be loaded; the current choice is kept.`}</p>}
      {/* Described by the select, not announced: a blocked save announces it in the page error. */}
      {issue && <p id={issueId} className={keptStored ? 'settings-inline-warning' : 'form-error'}>{issue}</p>}
    </div>
  )
}
