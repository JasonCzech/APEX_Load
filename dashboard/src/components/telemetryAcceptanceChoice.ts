import { analysisPoliciesApi, type AnalysisPolicyRecord } from '../hooks/api/analysisPoliciesApi'

/** Chooses the project's default explicitly (a revised copy drops its source's choice). */
export const TELEMETRY_POLICY_PROJECT_DEFAULT = 'project_default'

/**
 * telemetryPolicyIssue names a chosen policy the project no longer has, once
 * the project's policies are known; empty when the choice is usable or cannot
 * be judged here (the controller decides). An empty choice keeps
 * inheritedId (a revised copy's source run's choice), which is judged instead.
 */
export function telemetryPolicyIssue(chosen: string | undefined, policies: AnalysisPolicyRecord[] | null, inheritedId?: string): string {
  const id = chosen?.trim() ?? ''
  if (policies === null || id === TELEMETRY_POLICY_PROJECT_DEFAULT) return ''
  if (!id) {
    const inherited = inheritedId?.trim() ?? ''
    return inherited && !policies.some(policy => policy.id === inherited)
      ? `The source run's telemetry acceptance policy "${inherited}" no longer exists in this project; choose another or the project default.`
      : ''
  }
  return policies.some(policy => policy.id === id)
    ? ''
    : `The chosen telemetry acceptance policy "${id}" no longer exists in this project; choose another or the project default.`
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
export async function telemetryPolicySaveIssue(projectId: string | undefined, chosen: string | undefined, inheritedId?: string, savedId?: string, signal?: AbortSignal): Promise<string> {
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
    return telemetryPolicyIssue(id, await analysisPoliciesApi.list(projectId, 'telemetry_acceptance_policy', controller.signal), inheritedId)
  } catch {
    return ''
  } finally {
    clearTimeout(timer)
    signal?.removeEventListener('abort', abort)
  }
}
