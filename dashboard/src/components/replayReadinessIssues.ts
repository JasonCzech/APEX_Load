import type { ReplayPreflight } from '../types/scripts'
import { issueTitle } from './importReviewIssues'
import type { ScriptActionPhase } from './scriptEditorWorkbench'

/**
 * One replay-readiness problem, as Author Problems lists it. The replay
 * preflight (server or local) already knows what blocks or weakens a debug
 * run; listing the same facts here keeps every problem count in the editor —
 * Author Problems, the top bar, the rail and the debugger — telling one story
 * with Checks & readiness.
 */
export interface ReadinessIssue {
  id: string
  severity: 'blocker' | 'warning'
  title: string
  description?: string
  /** The action an execution gate sits on, when the preflight names it. */
  location?: { phase: ScriptActionPhase; actionIndex: number }
  variable?: string
  secret?: string
}

const PHASE_PATH = /^(init|actions|teardown)\[(\d+)\]/
// A review gate as the server and the local preflight word it:
// `init[2] "Name": <what blocks replay>`, or `init[2]: …` for an unnamed action.
// The name match is lazy so a quote inside it (escaped or not) still parses.
const GATE_MESSAGE = /^(?:init|actions|teardown)\[\d+\](\S*)(?: ".*?")?: (.+)$/
const EXECUTION_BLOCKER = /^execution blocker (.+) blocks \S+ until the imported behavior is resolved$/
// Warnings the preflight phrases as sentences about lists this module itemizes.
const ITEMIZED_WARNING = /^(variable\(s\) with no seed|configure project secret\(s\)|secret\(s\) require project configuration)/i

const normalize = (text: string) => text.trim().replace(/\s+/g, ' ').toLowerCase()
const sentence = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

/** A review gate message taken apart: where it sits and what blocks. */
export interface ReviewGateIssue {
  phase: 'init' | 'actions' | 'teardown'
  actionIndex: number
  /** The path below the action, for a nested request (`.params.request`). */
  subPath: string
  /** What blocks replay, as worded after the action path. */
  rest: string
  /** Execution blocker codes, when the gate is one. */
  codes: string[]
}

/**
 * Parses `actions[3] "Name": <what blocks replay>` as the server and the
 * local preflight word a review gate. Codes are split on the commas between
 * them, not the ones inside a code's `(names)` detail.
 */
export function parseReviewGateIssue(message: string): ReviewGateIssue | null {
  const trimmed = message.trim()
  const gate = GATE_MESSAGE.exec(trimmed)
  const path = PHASE_PATH.exec(trimmed)
  if (!gate || !path) return null
  const [, subPath, rest] = gate
  const blocker = EXECUTION_BLOCKER.exec(rest)
  const codes = blocker
    ? blocker[1].split(/,\s*(?![^()]*\))/).map(code => code.replace(/\s*\(.*\)$/, '').trim()).filter(Boolean)
    : []
  return { phase: path[1] as ReviewGateIssue['phase'], actionIndex: Number(path[2]), subPath, rest, codes }
}

/** The reader-facing title of an execution blocker code. */
export function blockerTitle(code: string): string {
  return sentence(issueTitle(code))
}

/**
 * Titles a preflight issue the way the importer titles the same finding, and
 * drops the action path the breadcrumb already shows.
 */
function issueText(message: string): { title: string; description?: string } {
  const gate = parseReviewGateIssue(message)
  if (!gate) return { title: message }
  const { subPath, rest, codes } = gate
  // A nested request's own path is not in the breadcrumb, so keep it in view.
  const description = subPath ? message : sentence(rest)
  if (codes.length === 0) return subPath ? { title: sentence(rest), description } : { title: sentence(rest) }
  return { title: codes.map(blockerTitle).join(' · '), description }
}

/** The preflight facts readiness problems are built from. */
export type ReadinessFacts = Pick<ReplayPreflight, 'issues' | 'blocking_variables' | 'placeholder_variables' | 'missing_variables' | 'missing_secrets' | 'data_source_issues' | 'warnings'>

/**
 * @param alreadyListed messages the editor already shows from another source
 *   (current server validation issues, validation and replay warnings), so
 *   the same text is not listed twice.
 */
export function replayReadinessIssues(
  preflight: ReadinessFacts | null | undefined,
  alreadyListed: readonly string[] = [],
  secretLabels: ReadonlyMap<string, string> = new Map(),
): ReadinessIssue[] {
  if (!preflight) return []
  const listed = new Set(alreadyListed.map(normalize))
  const issues: ReadinessIssue[] = []
  for (const message of preflight.issues || []) {
    if (!message.trim() || listed.has(normalize(message))) continue
    const match = PHASE_PATH.exec(message.trim())
    issues.push({
      id: `readiness:issue:${normalize(message)}`,
      severity: 'blocker',
      ...issueText(message.trim()),
      ...(match ? { location: { phase: (match[1] === 'actions' ? 'action' : match[1]) as ScriptActionPhase, actionIndex: Number(match[2]) } } : {}),
    })
  }
  // An older controller sends only missing_variables: list those as the
  // non-blocking kind rather than guess which of them refuse replay.
  const structured = preflight.blocking_variables !== undefined || preflight.placeholder_variables !== undefined
  for (const name of preflight.blocking_variables || []) {
    issues.push({ id: `readiness:blocking:${name}`, severity: 'blocker', title: `Unresolved value: ${name}`, description: 'Replay refuses to run until a parameter, an extractor or a session variable provides it.', variable: name })
  }
  for (const name of structured ? preflight.placeholder_variables || [] : preflight.missing_variables || []) {
    issues.push({ id: `readiness:placeholder:${name}`, severity: 'warning', title: `Missing value: ${name}`, description: 'Nothing provides it before use, so replay sends the placeholder text.', variable: name })
  }
  // The title uses the secret's display name (secretBindingDisplayNames); the
  // exact name the script references stays in the description.
  for (const name of preflight.missing_secrets || []) {
    const label = secretLabels.get(name) || name
    issues.push({
      id: `readiness:secret:${name}`,
      severity: 'blocker',
      title: `Configure project secret: ${label}`,
      description: label === name
        ? 'Replay stays blocked until the project secret has a value.'
        : `Replay stays blocked until the project secret ${name} has a value.`,
      secret: name,
    })
  }
  // Data-source warnings refuse replay; an older controller does not single
  // them out, so there they stay in the warnings below. Only the preflight
  // reports them, so no other list can already hold them.
  const dataSourceIssues = new Set<string>()
  for (const message of preflight.data_source_issues || []) {
    if (!message.trim() || dataSourceIssues.has(normalize(message))) continue
    dataSourceIssues.add(normalize(message))
    issues.push({ id: `readiness:data:${normalize(message)}`, severity: 'blocker', title: message.trim(), description: 'Replay refuses to run until the data source is fixed.' })
  }
  for (const message of preflight.warnings || []) {
    if (!message.trim() || ITEMIZED_WARNING.test(message.trim()) || listed.has(normalize(message)) || dataSourceIssues.has(normalize(message))) continue
    issues.push({ id: `readiness:warning:${normalize(message)}`, severity: 'warning', title: message.trim() })
  }
  return issues
}

/**
 * Where the editor opens a script: the first step Author Problems lists (an
 * authoring error, then a replay gate), else the first action (Actions before
 * Init and Teardown); null for a script with no steps.
 */
export function openingEditorFocus(
  authoringErrorRows: readonly { phase: ScriptActionPhase; actionIndex: number }[],
  readiness: readonly ReadinessIssue[],
  rows: readonly { phase: ScriptActionPhase; actionIndex: number }[],
): { phase: ScriptActionPhase; actionIndex: number } | null {
  const pick = (row?: { phase: ScriptActionPhase; actionIndex: number }) => row ? { phase: row.phase, actionIndex: row.actionIndex } : undefined
  return pick(authoringErrorRows[0])
    ?? readiness.find(issue => issue.location)?.location
    ?? pick(rows.find(row => row.phase === 'action'))
    ?? pick(rows[0])
    ?? null
}
