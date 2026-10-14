import type { ScriptDefinition } from '../types/scripts'
import { blockerTitle, parseReviewGateIssue, type ReviewGateIssue } from './replayReadinessIssues'

/** Steps a replay refused for one reason, and the hosts they call. */
export interface ReplayBlockerGroup {
  title: string
  count: number
  hosts: { host: string; count: number }[]
}

export interface ReplayBlockerSummary {
  /** Distinct steps the review gates refused. */
  steps: number
  groups: ReplayBlockerGroup[]
}

function actionHost(script: ScriptDefinition | undefined, gate: ReviewGateIssue): string {
  const action = script?.[gate.phase]?.[gate.actionIndex]
  const target = typeof action?.target === 'string' ? action.target : ''
  if (!target) return ''
  try {
    return new URL(target, script?.config?.base_url || undefined).hostname
  } catch {
    return ''
  }
}

/**
 * Groups the review gates that refused a replay by what blocks them, so a
 * long refusal reads as a few lines: "35 steps need import review, mostly
 * calls to appgw.conviva.com". Messages that are not gates keep their text.
 */
export function summarizeReplayBlockers(issues: readonly string[], script?: ScriptDefinition): ReplayBlockerSummary {
  const groups = new Map<string, { steps: Set<string>; hosts: Map<string, number> }>()
  const steps = new Set<string>()
  for (const issue of issues) {
    const gate = parseReviewGateIssue(issue)
    const step = gate ? `${gate.phase}[${gate.actionIndex}]` : issue
    steps.add(step)
    const titles = gate
      ? gate.codes.length > 0 ? gate.codes.map(blockerTitle) : [gate.rest]
      : [issue]
    const host = gate ? actionHost(script, gate) : ''
    for (const title of new Set(titles)) {
      const group = groups.get(title) ?? { steps: new Set<string>(), hosts: new Map<string, number>() }
      if (!group.steps.has(step)) {
        group.steps.add(step)
        if (host) group.hosts.set(host, (group.hosts.get(host) ?? 0) + 1)
      }
      groups.set(title, group)
    }
  }
  return {
    steps: steps.size,
    groups: [...groups.entries()]
      .map(([title, group]) => ({
        title,
        count: group.steps.size,
        hosts: [...group.hosts.entries()]
          .map(([host, count]) => ({ host, count }))
          .sort((a, b) => b.count - a.count || a.host.localeCompare(b.host)),
      }))
      .sort((a, b) => b.count - a.count || a.title.localeCompare(b.title)),
  }
}

/** The review gates a 409 replay refusal lists, when the server sent them. */
export function replayBlockedIssues(error: unknown): string[] | null {
  if (!error || typeof error !== 'object') return null
  const { code, payload } = error as { code?: unknown; payload?: unknown }
  if (code !== 'script_replay_blocked' || !payload || typeof payload !== 'object') return null
  const issues = (payload as { issues?: unknown }).issues
  return Array.isArray(issues) && issues.every(issue => typeof issue === 'string') ? issues as string[] : null
}
