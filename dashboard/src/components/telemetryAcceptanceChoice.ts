import { analysisPoliciesApi, type AnalysisPolicyKind, type AnalysisPolicyRecord } from '../hooks/api/analysisPoliciesApi'

/** Chooses the project's default explicitly (a revised copy drops its source's choice). */
export const TELEMETRY_POLICY_PROJECT_DEFAULT = 'project_default'

/**
 * The telemetry policy a run chooses (TEL-B): the acceptance policy that
 * judges it, or the budget policy that sets its live budgets. Without a budget
 * choice a run uses the system threshold settings: a project's default budget
 * policy is pinned only and never sets a run's budgets.
 */
export type TelemetryPolicyChoiceKind = 'acceptance' | 'budget'

export const TELEMETRY_POLICY_CHOICE: Record<TelemetryPolicyChoiceKind, { apiKind: AnalysisPolicyKind; noun: string; fallback: string }> = {
  acceptance: { apiKind: 'telemetry_acceptance_policy', noun: 'telemetry acceptance policy', fallback: 'the project default' },
  budget: { apiKind: 'telemetry_policy', noun: 'telemetry budget policy', fallback: 'the system threshold settings' },
}

/** A run's live classification uses this sustained-breach window; a budget policy with another cannot set a run's budgets. */
export const LIVE_SUSTAINED_WINDOW_SECONDS = 15

/**
 * telemetryPolicyUnusableReason says why a listed policy cannot be chosen
 * here, or '' when it can: a budget policy whose sustained window differs
 * from live classification's, which the controller refuses.
 */
export function telemetryPolicyUnusableReason(policy: AnalysisPolicyRecord, kind: TelemetryPolicyChoiceKind = 'acceptance'): string {
  if (kind !== 'budget') return ''
  const definition = policy.definition
  const window = typeof definition === 'object' && definition !== null ? (definition as { sustained_window_seconds?: unknown }).sustained_window_seconds : undefined
  return typeof window === 'number' && window !== LIVE_SUSTAINED_WINDOW_SECONDS
    ? `it sets a ${window} s sustained window, and a run's live classification uses ${LIVE_SUSTAINED_WINDOW_SECONDS} s`
    : ''
}

/**
 * telemetryPolicyProblem names what is wrong with a policy ID against the
 * project's policies ("no longer exists in this project", or the reason it
 * is unusable); '' when it is usable.
 */
export function telemetryPolicyProblem(id: string, policies: AnalysisPolicyRecord[], kind: TelemetryPolicyChoiceKind = 'acceptance'): string {
  const policy = policies.find(candidate => candidate.id === id)
  if (!policy) return 'no longer exists in this project'
  const reason = telemetryPolicyUnusableReason(policy, kind)
  return reason ? `cannot set a run's budgets: ${reason}` : ''
}

/**
 * telemetryPolicyIssue names a chosen policy the project no longer has (or,
 * for a budget policy, cannot use), once the project's policies are known;
 * empty when the choice is usable or cannot be judged here (the controller
 * decides). An empty choice keeps inheritedId (a revised copy's source run's
 * choice), which is judged instead.
 */
export function telemetryPolicyIssue(chosen: string | undefined, policies: AnalysisPolicyRecord[] | null, inheritedId?: string, kind: TelemetryPolicyChoiceKind = 'acceptance'): string {
  const { noun, fallback } = TELEMETRY_POLICY_CHOICE[kind]
  const id = chosen?.trim() ?? ''
  if (policies === null || id === TELEMETRY_POLICY_PROJECT_DEFAULT) return ''
  if (!id) {
    const inherited = inheritedId?.trim() ?? ''
    const problem = inherited ? telemetryPolicyProblem(inherited, policies, kind) : ''
    return problem ? `The source run's ${noun} "${inherited}" ${problem}; choose another or ${fallback}.` : ''
  }
  const problem = telemetryPolicyProblem(id, policies, kind)
  return problem ? `The chosen ${noun} "${id}" ${problem}; choose another or ${fallback}.` : ''
}

/** A save-time policy read that takes longer blocks nothing. */
export const TELEMETRY_POLICY_SAVE_READ_TIMEOUT_MS = 10_000

/**
 * telemetryPolicySaveIssue judges a choice against a fresh read of the
 * project's policies at save time, so a list read earlier (or for another
 * scenario) can never block a save. A choice equal to savedId (the scenario's
 * stored choice) is not judged, as the controller does not judge a save that
 * keeps it. A failed or slow read blocks nothing: the controller validates
 * the choice anyway. signal ends the read early (the screen closed).
 */
export async function telemetryPolicySaveIssue(projectId: string | undefined, chosen: string | undefined, inheritedId?: string, savedId?: string, signal?: AbortSignal, kind: TelemetryPolicyChoiceKind = 'acceptance'): Promise<string> {
  const id = chosen?.trim() ?? ''
  const judged = id || inheritedId?.trim() || ''
  if (!projectId || !judged || id === TELEMETRY_POLICY_PROJECT_DEFAULT) return ''
  if (id && id === (savedId?.trim() ?? '')) return ''
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), TELEMETRY_POLICY_SAVE_READ_TIMEOUT_MS)
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort)
  if (signal?.aborted) controller.abort()
  try {
    return telemetryPolicyIssue(id, await analysisPoliciesApi.list(projectId, TELEMETRY_POLICY_CHOICE[kind].apiKind, controller.signal), inheritedId, kind)
  } catch {
    return ''
  } finally {
    clearTimeout(timer)
    signal?.removeEventListener('abort', abort)
  }
}
