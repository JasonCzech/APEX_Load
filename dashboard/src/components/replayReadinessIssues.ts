import type { ReplayPreflight, ScriptDefinition } from '../types/scripts'
import { configReferenceText, issueTitle, repairImpactText } from './importReviewIssues'
import { replayBlockingKinds } from './replayPreflightUtils'
import { phaseActionsForScript, stepPathForJsonSubPath } from './scriptEditorJsonModel'
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
  /**
   * The action an execution gate sits on, when the preflight names it, and
   * the outline's step path of the nested step that carries it.
   */
  location?: { phase: ScriptActionPhase; actionIndex: number; stepPath?: string[] }
  /**
   * The execution blocker codes behind the problem and the path of the step
   * that carries them (`init[0]`, `actions[4].params.requests[1]`), as the
   * guided repairs address a finding: on gate rows, and on a value row that
   * stands for the gates naming it.
   */
  codes?: string[]
  actionPath?: string
  variable?: string
  /** A blocking value a protocol configuration reference holds: no step clears it. */
  config?: boolean
  secret?: string
  /**
   * The preflight's own words for a static issue that is not a gate
   * (`actions[2] "Login": missing target`), so the editor can tell when a
   * check it runs itself already lists the same problem.
   */
  message?: string
  /**
   * A problem only a save clears: steps still carry the import review code
   * for a value the draft now supplies, and saving re-evaluates that code.
   */
  clearedBySave?: boolean
}

/** What every execution blocker description ends with. */
export const REPLAY_REFUSAL_SENTENCE = 'Debug and test runs refuse this script until it is resolved.'

const PHASE_PATH = /^(init|actions|teardown)\[(\d+)\]/
// A review gate as the server and the local preflight word it:
// `init[2] "Name": <what blocks replay>`, or `init[2]: …` for an unnamed action.
// The name match is lazy so a quote inside it (escaped or not) still parses.
const GATE_MESSAGE = /^(?:init|actions|teardown)\[\d+\](\S*)(?: ".*?")?: (.+)$/
// A header check names the request, then the header after its position:
// `actions[3] "Create session".header_list[7] "X-Bad": <what is wrong> (<code>)`.
// Both names are Go-quoted, so a name may hold an escaped quote.
const HEADER_MESSAGE = /^(?:init|actions|teardown)\[\d+\].*?\.(header_list|headers|effective_headers)\[\d+\] "((?:[^"\\]|\\.)*)": (.+)$/
// The same check on the script's own default headers.
const DEFAULT_HEADER_MESSAGE = /^config\.http\.(?:default_header_list|default_headers)\[\d+\] "((?:[^"\\]|\\.)*)": (.+)$/
const TRAILING_CODE = /\s*\([a-z0-9_]+\)$/
const EXECUTION_BLOCKER = /^execution blocker (.+) blocks (\S+) until the imported behavior is resolved$/
// The import's dependency codes, which name the value a consumer lacks.
const DEPENDENCY_CODE = /^(?:missing_producer|conditional_producer):(.+)$/

/** The value a dependency code (`missing_producer:sid`) names; undefined for any other code. */
export function dependencyCodeValue(code: string): string | undefined {
  return DEPENDENCY_CODE.exec(code)?.[1] || undefined
}

/** The value facts that say whether a dependency code's value is still missing. */
export type DependencyFacts = Pick<ReplayPreflight, 'blocking_variables' | 'placeholder_variables' | 'missing_variables'>

/**
 * How the dependency codes a preflight's gates carry are listed, each value
 * once however many steps name it:
 * - value: the value blocks replay, so the code is that value's problem.
 * - saved-review: the preflight no longer reports the value missing (the
 *   draft supplies it), but the steps keep the code the import review
 *   stamped until a save re-evaluates it.
 * - code: any other dependency code, a problem of its own.
 * Undefined for a code that is not a dependency code.
 */
export function dependencyCodeFolding(facts: DependencyFacts | null | undefined): (code: string) => { kind: 'value' | 'saved-review' | 'code'; name: string } | undefined {
  const blocking = new Set(facts?.blocking_variables || [])
  // A preflight that reports no values cannot say the draft supplies one.
  const reportsValues = facts?.blocking_variables !== undefined || facts?.placeholder_variables !== undefined || facts?.missing_variables !== undefined
  const missing = new Set([...blocking, ...(facts?.placeholder_variables || []), ...(facts?.missing_variables || [])])
  return code => {
    const name = dependencyCodeValue(code)
    if (!name) return undefined
    if (blocking.has(name)) return { kind: 'value', name }
    return { kind: reportsValues && !missing.has(name) ? 'saved-review' : 'code', name }
  }
}

/** The title of the row a value's saved review codes are listed as. */
export function savedReviewTitle(name: string): string {
  return `Save to update the import review for ${name}`
}
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
  /** What each code is about, when the message says: `(headers left out: X-Bad)`. */
  details: Record<string, string>
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
  const details: Record<string, string> = {}
  const codes = blocker
    ? blocker[1].split(/,\s*(?![^()]*\))/).map(part => {
      const code = part.replace(/\s*\(.*\)$/, '').trim()
      const detail = /\((.*)\)$/.exec(part.trim())?.[1]
      if (code && detail) details[code] = detail
      return code
    }).filter(Boolean)
    : []
  return { phase: path[1] as ReviewGateIssue['phase'], actionIndex: Number(path[2]), subPath, rest, codes, details }
}

// `header value contains …` as `value contains …`, without its code.
function headerProblem(rest: string): string {
  return rest.replace(TRAILING_CODE, '').replace(/^header\s+/i, '').trim()
}

// A Go-quoted name as written, with its escaped quotes and backslashes plain.
function unquoteName(quoted: string): string {
  return quoted.replace(/\\(["\\])/g, '$1')
}

/** The reader-facing title of an execution blocker code. */
export function blockerTitle(code: string): string {
  return sentence(issueTitle(code))
}

/**
 * Where the editor's script gets a protected request body, which no editor
 * form supplies (storedRepairOptions offers no body repair): a saved script,
 * or a draft with no import review behind it, by importing the source again;
 * an unsaved draft opened from the import review, from that review's body
 * repair.
 */
export type ProtectedBodySource = 'reimport' | 'import-review'

/**
 * What a blocker code means for the editor's script, worded once for every
 * editor surface: the import review's wording (repairImpactText), except for
 * a protected body, which is restored where bodySource says.
 */
export function storedScriptImpactText(code: string, detail = '', bodySource: ProtectedBodySource = 'reimport'): string {
  if (code === 'restored_body_required') {
    return bodySource === 'import-review'
      ? 'The original request body could not be restored from the source. A protected request body can be restored only while importing: return to the import review to supply the protected body.'
      : 'The original request body could not be restored from the source. A protected request body can be restored only while importing: import the source again to restore it.'
  }
  return repairImpactText(code, detail)
}

/**
 * A header check as the server or the local preflight words it, in plain
 * words: which header, what is wrong with it and where to fix it, without its
 * JSON path; null for any other message. Author Problems titles server
 * validation issues with it too.
 */
export function headerIssueText(message: string): { title: string; description: string } | null {
  const defaultHeader = DEFAULT_HEADER_MESSAGE.exec(message.trim())
  if (defaultHeader) {
    return {
      title: `Default header "${unquoteName(defaultHeader[1])}": ${headerProblem(defaultHeader[2])}`,
      description: 'Every request sends the script\'s default headers. Correct or remove this one in Runtime Settings › Request Defaults.',
    }
  }
  const header = HEADER_MESSAGE.exec(message.trim())
  if (header) {
    const [, list, name, problem] = header
    return {
      title: `Header "${unquoteName(name)}": ${headerProblem(problem)}`,
      description: list === 'effective_headers'
        ? 'The request sends this header with the script\'s default headers, exactly as written. Correct or remove it in Runtime Settings › Request Defaults or in this request\'s headers.'
        : 'Replay sends headers exactly as written. Correct or remove it in this request\'s headers.',
    }
  }
  return null
}

/**
 * Titles a preflight issue the way the importer titles the same finding, drops
 * the action path the breadcrumb already shows, and says what a blocker means
 * for replay (a protected body restored where bodySource says); a message
 * that is not a gate keeps its text. Author Problems, Checks & readiness and a
 * refused start word an issue with it.
 */
export function readinessIssueText(message: string, bodySource: ProtectedBodySource = 'reimport'): { title: string; description?: string } {
  const header = headerIssueText(message)
  if (header) return header
  const gate = parseReviewGateIssue(message)
  if (!gate) return { title: message }
  const { subPath, rest, codes, details } = gate
  // Only a hold for left-out headers alone is titled after them; the server
  // says "other review and N headers left out" when it covers more.
  const leftOutMatch = codes.length === 1 && codes[0] === 'import_review_required'
    ? /^(\d+) headers? left out: (.+)$/.exec(details.import_review_required || '')
    : null
  if (leftOutMatch) {
    const plural = Number(leftOutMatch[1]) !== 1
    const leftOut = leftOutMatch[2]
    return {
      title: `${plural ? 'Headers' : 'Header'} ${leftOut} left out on import`,
      description: `HTTP cannot send ${plural ? 'these headers' : 'this header'} as the source recorded ${plural ? 'them' : 'it'}, so the import left ${plural ? 'them' : 'it'} out and holds the request for review. Correct ${plural ? 'them' : 'it'} in the source and import it again.`,
    }
  }
  // A nested request's own path is not in the breadcrumb, so keep it in view.
  const description = subPath ? message : sentence(rest)
  if (codes.length === 0) return subPath ? { title: sentence(rest), description } : { title: sentence(rest) }
  // What each blocker means for the editor's script, as the import review
  // says it; a code with nothing specific to say keeps the gate's own words.
  const impacts = [...new Set(codes.map(code => storedScriptImpactText(code, '', bodySource)).filter(Boolean))]
  if (impacts.length === 0) return { title: codes.map(blockerTitle).join(' · '), description }
  const impact = `${impacts.join(' ')} ${REPLAY_REFUSAL_SENTENCE}`
  const label = message.trim().slice(0, -(rest.length + 2))
  return { title: codes.map(blockerTitle).join(' · '), description: subPath ? `${label}: ${impact}` : impact }
}

// The gate message as it would read with only one of its codes, so the code
// is titled and described on its own.
function singleCodeGateMessage(message: string, gate: ReviewGateIssue, code: string): string {
  const label = message.trim().slice(0, -(gate.rest.length + 2))
  const blocks = EXECUTION_BLOCKER.exec(gate.rest)?.[2] || 'replay'
  const detail = gate.details[code]
  return `${label}: execution blocker ${code}${detail ? ` (${detail})` : ''} blocks ${blocks} until the imported behavior is resolved`
}

/**
 * The codes on one step that are each a problem of their own. As the import
 * review groups them, the generic review beside a required body restoration
 * is that restoration's evidence, not a second problem.
 */
export function distinctBlockerCodes<T extends string>(codes: readonly T[]): T[] {
  return codes.filter(code => !(code === 'import_review_required' && codes.includes('restored_body_required' as T)))
}

/**
 * The problems a review gate message stands for, one per execution blocker
 * code (distinctBlockerCodes), as the import review counts one finding per
 * code and step: each with the message as it would read with that code alone
 * (a message with one code keeps its own words). Author Problems, the start
 * gate, Checks & readiness and the step's repair panel all count a gate by
 * these; a message with no codes yields none.
 */
export function reviewGateCodeProblems(message: string, gate: ReviewGateIssue | null = parseReviewGateIssue(message)): { code: string; message: string }[] {
  if (!gate) return []
  const single = gate.codes.length === 1
  return distinctBlockerCodes(gate.codes)
    .map(code => ({ code, message: single ? message.trim() : singleCodeGateMessage(message, gate, code) }))
}

/**
 * Where a gate message sits, as a breadcrumb reads without the script: the
 * phase, then the step's name (`Init › web_set_sockets_option_15`), or its
 * number for an unnamed step; undefined for any other message.
 */
export function reviewGateBreadcrumb(message: string): string | undefined {
  const gate = parseReviewGateIssue(message)
  if (!gate) return undefined
  const name = /^\S*? "((?:[^"\\]|\\.)*)":/.exec(message.trim())?.[1]
  const phase = gate.phase === 'actions' ? 'Actions' : gate.phase === 'init' ? 'Init' : 'Teardown'
  return `${phase} › ${name ? unquoteName(name) : `Action ${gate.actionIndex + 1}`}`
}

/**
 * The line a refused debug start shows, from the blockers the editor lists
 * now (not the refusal's text, which describes the draft as it was sent):
 * `Debug did not start: 2 problems block replay — <title> (<where>); …`, with
 * at most three named and the rest counted.
 */
export function replayRefusalSummary<T extends { title: string }>(blockers: readonly T[], breadcrumb: (blocker: T) => string | undefined = () => undefined): string {
  if (blockers.length === 0) return 'Debug did not start.'
  const named = blockers.slice(0, 3).map(blocker => {
    const where = breadcrumb(blocker)
    return where ? `${blocker.title} (${where})` : blocker.title
  })
  const more = blockers.length > 3 ? `; +${blockers.length - 3} more` : ''
  return `Debug did not start: ${blockers.length} ${blockers.length === 1 ? 'problem blocks' : 'problems block'} replay — ${named.join('; ')}${more}.`
}

/** The preflight facts readiness problems are built from. */
export type ReadinessFacts = Pick<ReplayPreflight, 'issues' | 'blocking_variables' | 'placeholder_variables' | 'missing_variables' | 'late_produced_variables' | 'param_blocking_variables' | 'config_blocking_variables' | 'missing_secrets' | 'data_source_issues' | 'warnings'>

/**
 * @param alreadyListed messages the editor already shows from another source
 *   (current server validation issues, validation and replay warnings), so
 *   the same text is not listed twice.
 * @param script the document the preflight describes, so a gate on a nested
 *   step (`actions[3].params.requests[1]`) locates that step, not only its
 *   action, and so a blocking value is worded by why it blocks when an older
 *   controller's preflight does not say (replayBlockingKinds).
 * @param bodySource where the script gets a protected request body, for the
 *   words of a gate that needs one (storedScriptImpactText).
 */
export function replayReadinessIssues(
  preflight: ReadinessFacts | null | undefined,
  alreadyListed: readonly string[] = [],
  secretLabels: ReadonlyMap<string, string> = new Map(),
  script?: ScriptDefinition,
  bodySource: ProtectedBodySource = 'reimport',
): ReadinessIssue[] {
  if (!preflight) return []
  const listed = new Set(alreadyListed.map(normalize))
  const issues: ReadinessIssue[] = []
  // A dependency code that names a value listed as blocking below is the
  // same problem as that value: it is listed once, as the importer groups
  // it, at the first gated step that uses it. A code for a value the draft
  // now supplies is one row for that value, at its first gated step: only a
  // save re-evaluates the code (dependencyCodeFolding).
  const folding = dependencyCodeFolding(preflight)
  const gatedValues = new Map<string, Pick<ReadinessIssue, 'location' | 'codes' | 'actionPath'>>()
  const savedReviews = new Map<string, { issue: ReadinessIssue; codes: string[]; steps: Set<string> }>()
  for (const message of preflight.issues || []) {
    if (!message.trim() || listed.has(normalize(message))) continue
    const match = PHASE_PATH.exec(message.trim())
    const gate = parseReviewGateIssue(message)
    const at = match ? { phase: (match[1] === 'actions' ? 'action' : match[1]) as ScriptActionPhase, actionIndex: Number(match[2]) } : undefined
    const stepPath = at && gate?.subPath && script ? stepPathForJsonSubPath(phaseActionsForScript(script, at.phase)[at.actionIndex], gate.subPath) : undefined
    const location: ReadinessIssue['location'] = at && stepPath ? { ...at, stepPath } : at
    if (!gate?.codes.length) {
      issues.push({ id: `readiness:issue:${normalize(message)}`, severity: 'blocker', ...readinessIssueText(message.trim(), bodySource), ...(location ? { location } : {}), message: message.trim() })
      continue
    }
    // Each code is its own problem (reviewGateCodeProblems); a message with
    // several keeps them apart by code.
    const actionPath = `${gate.phase}[${gate.actionIndex}]${gate.subPath}`
    const single = gate.codes.length === 1
    for (const { code, message: codeMessage } of reviewGateCodeProblems(message, gate)) {
      const value = folding(code)
      if (value?.kind === 'value') {
        if (location && !gatedValues.has(value.name)) gatedValues.set(value.name, { location, codes: [code], actionPath })
        continue
      }
      if (value?.kind === 'saved-review') {
        const saved = savedReviews.get(value.name)
        if (saved) {
          if (!saved.codes.includes(code)) saved.codes.push(code)
          saved.steps.add(actionPath)
          continue
        }
        const codes = [code]
        const issue: ReadinessIssue = { id: `readiness:saved-review:${value.name}`, severity: 'blocker', title: savedReviewTitle(value.name), ...(location ? { location } : {}), codes, actionPath, variable: value.name, clearedBySave: true }
        savedReviews.set(value.name, { issue, codes, steps: new Set([actionPath]) })
        issues.push(issue)
        continue
      }
      issues.push({
        id: single ? `readiness:issue:${normalize(message)}` : `readiness:issue:${normalize(message)}#${code}`,
        severity: 'blocker',
        ...readinessIssueText(codeMessage, bodySource),
        ...(location ? { location } : {}),
        codes: [code],
        actionPath,
      })
    }
  }
  for (const [name, { issue, steps }] of savedReviews) {
    const one = steps.size === 1
    issue.description = `The draft now supplies ${name}, but ${one ? 'a step still carries the import review code' : `${steps.size} steps still carry the import review codes`} that ${one ? 'names' : 'name'} it missing. Saving re-evaluates ${one ? 'the step\'s review code' : 'their review codes'}. ${REPLAY_REFUSAL_SENTENCE}`
  }
  // An older controller sends only missing_variables: list those as the
  // non-blocking kind rather than guess which of them refuse replay.
  const structured = preflight.blocking_variables !== undefined || preflight.placeholder_variables !== undefined
  // A value some step sets, only not before every use, is a producer-order
  // problem, titled and worded as the importer titles conditional_producer.
  const lateProduced = new Set(preflight.late_produced_variables || [])
  const producerOrder = (name: string) => `Producer ordering or conditional execution for ${name} requires review. A step sets it, but not before every use`
  // No step clears a configuration reference or a {{param.*}} use, so their
  // fix is worded first, as the importer words those uses, even where an
  // unrelated {{var.*}} use of the name is late.
  const kinds = replayBlockingKinds(preflight, script)
  const blockingDescription = (name: string) => kinds.config.has(name)
    ? configReferenceText(name)
    : kinds.param.has(name)
      ? `No parameter supplies ${name}, and replay refuses to run with an unresolved parameter. Declare a script parameter named ${name} (it can read its values from a data source).`
      : 'Replay refuses to run until a parameter, an extractor or a session variable provides it.'
  for (const name of preflight.blocking_variables || []) {
    // A configuration reference is script-wide and replaced in Runtime
    // Settings: a step gate on the same name is evidence of it, as the
    // importer groups it, so its row opens no step and no step repair.
    const gated = kinds.config.has(name) ? {} : gatedValues.get(name) || {}
    issues.push(lateProduced.has(name) && !kinds.config.has(name) && !kinds.param.has(name)
      ? { id: `readiness:blocking:${name}`, severity: 'blocker', title: `Check producer order: ${name}`, description: `${producerOrder(name)}, and replay refuses to run without it.`, variable: name, ...gated }
      : { id: `readiness:blocking:${name}`, severity: 'blocker', title: kinds.config.has(name) ? `Replace configuration reference: ${name}` : `Unresolved value: ${name}`, description: blockingDescription(name), variable: name, ...(kinds.config.has(name) ? { config: true } : {}), ...gated })
  }
  for (const name of structured ? preflight.placeholder_variables || [] : preflight.missing_variables || []) {
    issues.push(lateProduced.has(name)
      ? { id: `readiness:placeholder:${name}`, severity: 'warning', title: `Check producer order: ${name}`, description: `${producerOrder(name)}, so replay can send the placeholder text.`, variable: name }
      : { id: `readiness:placeholder:${name}`, severity: 'warning', title: `Missing value: ${name}`, description: 'Nothing provides it before use, so replay sends the placeholder text.', variable: name })
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
    ?? pick(readiness.find(issue => issue.location)?.location)
    ?? pick(rows.find(row => row.phase === 'action'))
    ?? pick(rows[0])
    ?? null
}
