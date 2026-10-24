import { fidelityBlockerCoveredByGate, issueTitle } from './importReviewIssues'
import { dependencyCodeValue, headerIssueText, REPLAY_REFUSAL_SENTENCE, reviewGateCodeProblems, type ReadinessIssue } from './replayReadinessIssues'
import type { ScriptActionPhase } from './scriptEditorWorkbench'

// Server validation folds each import fidelity blocker into its issues with
// this prefix (pkg/internal/importreview health_handlers.go). Those hold the
// script back from promotion and load-test launch, never from a debug run.
const PROMOTION_FOLD_PREFIX = 'import fidelity blocker:'
const VALIDATION_PATH = /(?:script\.)?(actions|init|teardown)\[(\d+)\]/

/** A server validation issue that only promotion and load-test launch enforce. */
export function isPromotionOnlyValidationIssue(issue: string): boolean {
  return issue.trim().toLowerCase().startsWith(PROMOTION_FOLD_PREFIX)
}

// The fidelity blocker a promotion-only issue folds in, without its prefix.
const promotionFoldDetail = (issue: string) => issue.trim().slice(PROMOTION_FOLD_PREFIX.length).trim()
// A repaired report held while review codes remain names them as its blocker
// (pkg/internal/importreview unnamedPromotionBlocker): sorted, joined with
// ", " and ended with a period.
const REVIEW_HOLD = /^Imported script needs import review before promotion: (.+?)\.?$/i
const reviewHoldCodes = (detail: string) => REVIEW_HOLD.exec(detail)?.[1].split(', ').map(code => code.trim()).filter(Boolean) ?? []

/**
 * A promotion-only validation issue in plain words, titled as an import
 * review note. A report held for review codes names, by their titles, only
 * the codes not among `codes` (those the editor lists already).
 */
export function promotionReviewText(issue: string, codes: ReadonlySet<string> = new Set()): { title: string; description: string } {
  const detail = promotionFoldDetail(issue)
  const held = reviewHoldCodes(detail)
  if (held.length > 0) {
    const unlisted = held.filter(code => !codes.has(code))
    const named = unlisted.length > 0 ? unlisted : held
    return {
      title: `Import review: ${named.map(issueTitle).join('; ')}`,
      description: `Promotion and load-test launch hold this imported script for review of ${named.join(', ')}. Debug runs are not refused for it.`,
    }
  }
  return {
    title: `Import review: ${detail || 'review the imported script'}`,
    description: 'Promotion and load-test launch hold this imported script for review. Debug runs are not refused for it.',
  }
}

/**
 * Whether a promotion-only validation issue restates a problem the editor
 * lists already, as the import review leaves the same report entry out: a
 * step gate whose code is among `codes` (the draft's import findings and the
 * readiness problems' codes), unresolved credentials, a protected body, or a
 * report held for review codes that are all among `codes`.
 */
export function promotionFoldCoveredByGate(issue: string, codes: ReadonlySet<string>): boolean {
  if (!isPromotionOnlyValidationIssue(issue)) return false
  const detail = promotionFoldDetail(issue)
  const held = reviewHoldCodes(detail)
  return held.length > 0 ? held.every(code => codes.has(code)) : fidelityBlockerCoveredByGate(detail, codes)
}

/**
 * One problem the server refuses a debug start for. Its id is the Author
 * Problems row that lists it, so selecting that row opens the fix.
 */
export interface ReplayBlocker {
  id: string
  title: string
  description?: string
  /**
   * Who lists it: a readiness problem, a server validation issue, or a check
   * the editor runs itself that reports the same problem (coveredByAuthoring).
   */
  source: 'readiness' | 'validation' | 'authoring'
  /** The action, and the nested step within it, that "Resolve" opens. */
  location?: { phase: ScriptActionPhase; actionIndex: number; stepPath?: string[] }
  /** Execution blocker codes and the step carrying them, for a guided repair. */
  codes?: string[]
  actionPath?: string
  /** The server's own words for a static issue that is not a gate. */
  message?: string
  /** Only a save clears it: the saved review code names a value the draft supplies. */
  clearedBySave?: boolean
}

/**
 * What refuses a debug start as the server judges it: every readiness
 * blocker (review gates, unresolved values, data-source problems, missing
 * secrets once their status is known) and each current server validation
 * issue, except those only promotion enforces. Authoring checks the browser
 * runs on its own, the current operation's error, warnings and placeholder
 * values never refuse a start, so they are not here.
 *
 * @param validationIssues the current server validation issues, in the order
 *   Author Problems lists them (`server-<index>`).
 */
export function replayBlockers(readiness: readonly ReadinessIssue[], validationIssues: readonly string[]): ReplayBlocker[] {
  const blockers: ReplayBlocker[] = readiness.filter(issue => issue.severity === 'blocker').map(issue => ({
    id: issue.id,
    title: issue.title,
    ...(issue.description ? { description: issue.description } : {}),
    source: 'readiness',
    ...(issue.location ? { location: issue.location } : {}),
    ...(issue.codes?.length ? { codes: issue.codes, actionPath: issue.actionPath } : {}),
    ...(issue.message ? { message: issue.message } : {}),
    ...(issue.clearedBySave ? { clearedBySave: true } : {}),
  }))
  validationIssues.forEach((issue, index) => {
    if (!issue.trim() || isPromotionOnlyValidationIssue(issue)) return
    const match = VALIDATION_PATH.exec(issue)
    blockers.push({
      id: `server-${index}`,
      ...(headerIssueText(issue) || { title: issue }),
      source: 'validation',
      ...(match ? { location: { phase: (match[1] === 'actions' ? 'action' : match[1]) as ScriptActionPhase, actionIndex: Number(match[2]) } } : {}),
    })
  })
  return blockers
}

/**
 * A blocker that a check the editor runs itself also reports (a refused
 * start's `actions[2] "Login": missing target` beside "Action target is
 * required"), listed as that check: it takes the check's row id and title, so
 * Author Problems lists the problem once and "Resolve" opens that row. Rows
 * are one problem each, so blockers that land on one row count once.
 *
 * @param covering the editor's own row for a blocker, if one reports it.
 */
export function coveredByAuthoring(blockers: readonly ReplayBlocker[], covering: (blocker: ReplayBlocker) => { id: string; title: string } | undefined): ReplayBlocker[] {
  const ids = new Set<string>()
  return blockers.flatMap(blocker => {
    const local = covering(blocker)
    const listed: ReplayBlocker = local ? { ...blocker, id: local.id, title: local.title, source: 'authoring' } : blocker
    if (ids.has(listed.id)) return []
    ids.add(listed.id)
    return [listed]
  })
}

/**
 * How many problems the saved copy's review gates are, counted as the
 * blocked state counts them (reviewGateCodeProblems): one per execution
 * blocker code, one for a gate that names none, and one per value however
 * many gates name it, as a gated value is listed once script-wide.
 */
export function storedGateProblemCount(storedGateIssues: readonly string[]): number {
  const values = new Set<string>()
  return storedGateIssues.reduce((count, message) => {
    const problems = reviewGateCodeProblems(message)
    if (problems.length === 0) return count + 1
    return count + problems.filter(({ code }) => {
      const value = dependencyCodeValue(code)
      if (value === undefined) return true
      if (values.has(value)) return false
      values.add(value)
      return true
    }).length
  }, 0)
}

export type ReplayStartWaitCause = 'busy' | 'incomplete' | 'json' | 'secrets' | 'save'

/**
 * Whether a debug run can start now. Every start entry point (the Debug
 * button, Step, Run to selected action and the shortcuts) follows it.
 * - ready: nothing the server refuses for is known.
 * - waiting: the editor cannot start yet (a run or save in flight, no name
 *   or steps, unparsable JSON, project secret status not known yet, or a
 *   saved copy to save over while saving is unavailable).
 * - blocked: the draft has problems the server refuses a start for.
 * - save-first: the draft is clean, but the saved script the server judges
 *   first still has review gates; or the draft's only problems are review
 *   codes for values its unsaved changes supply, which a save re-evaluates
 *   (blockers).
 */
export type ReplayStartGate =
  | { kind: 'ready' }
  | { kind: 'waiting'; cause: ReplayStartWaitCause; reason: string }
  | { kind: 'blocked'; blockers: ReplayBlocker[] }
  | { kind: 'save-first'; storedGateIssues: string[]; blockers?: ReplayBlocker[] }

export interface ReplayStartGateInput {
  replaying: boolean
  saving: boolean
  hasName: boolean
  actionCount: number
  jsonInvalid: boolean
  /** Whether the project secrets the draft needs have a known status. */
  secretStatus: 'known' | 'checking' | 'unavailable'
  blockers: readonly ReplayBlocker[]
  /** The saved copy's review gates; empty for a script that is not saved. */
  storedGateIssues: readonly string[]
  /** Why a start waits for secret status, in the words the button shows. */
  secretStatusReason?: string
  /**
   * Why the editor cannot save now (its baseline is loading or held for
   * reconciliation); a draft that must be saved first waits instead.
   */
  saveUnavailableReason?: string
  /**
   * A saved script has unsaved changes, so a save can clear a problem only a
   * save clears (ReplayBlocker.clearedBySave).
   */
  unsavedChanges?: boolean
}

export function replayStartGate(input: ReplayStartGateInput): ReplayStartGate {
  if (input.replaying) return { kind: 'waiting', cause: 'busy', reason: 'A debug run is in progress.' }
  if (input.saving) return { kind: 'waiting', cause: 'busy', reason: 'Wait for the save to finish before debugging.' }
  if (!input.hasName || input.actionCount === 0) return { kind: 'waiting', cause: 'incomplete', reason: 'Name the script and add a step before debugging.' }
  if (input.jsonInvalid) return { kind: 'waiting', cause: 'json', reason: 'Fix the Script JSON before debugging.' }
  if (input.secretStatus !== 'known') {
    return {
      kind: 'waiting',
      cause: 'secrets',
      reason: input.secretStatusReason || (input.secretStatus === 'unavailable'
        ? 'Project secret status is temporarily unavailable. Retry the credential check before running.'
        : 'Checking project secret configuration before running…'),
    }
  }
  // Review codes for values the unsaved changes supply are re-evaluated by
  // the save "Save and debug" makes first; anything else must be resolved.
  const savedFirst = Boolean(input.unsavedChanges) && input.blockers.every(blocker => blocker.clearedBySave)
  if (input.blockers.length > 0 && !savedFirst) return { kind: 'blocked', blockers: [...input.blockers] }
  if (input.storedGateIssues.length > 0 || input.blockers.length > 0) {
    // "Save and debug" saves first; a save that cannot run would do nothing.
    if (input.saveUnavailableReason) return { kind: 'waiting', cause: 'save', reason: input.saveUnavailableReason }
    return { kind: 'save-first', storedGateIssues: [...input.storedGateIssues], ...(input.blockers.length > 0 ? { blockers: [...input.blockers] } : {}) }
  }
  return { kind: 'ready' }
}

const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`
// The saved script's problems a save-first start saves over: its gates, or
// the review codes its changes supply values for, which it carries too.
const saveFirstProblemCount = (gate: Extract<ReplayStartGate, { kind: 'save-first' }>) => Math.max(storedGateProblemCount(gate.storedGateIssues), gate.blockers?.length || 0)

/** Why a start control is unavailable while the gate is not ready; undefined when it is. */
export function replayStartBlockedReason(gate: ReplayStartGate): string | undefined {
  switch (gate.kind) {
    case 'ready': return undefined
    case 'waiting': return gate.reason
    case 'blocked': return `Resolve ${plural(gate.blockers.length, 'blocker')} before debugging.`
    case 'save-first': return `Save your changes before debugging: the saved script still has ${plural(saveFirstProblemCount(gate), 'blocker')}.`
  }
}

export interface ReplayGateAction {
  /** What a click does: start, open the next blocker, or save and then start. */
  intent: 'start' | 'resolve' | 'save-and-start'
  label: string
  compactLabel: string
  title: string
  ariaLabel?: string
  tone: 'success' | 'danger'
  icon: 'run' | 'blocker'
  disabled: boolean
}

/**
 * The Debug button for a gate: today's start while ready (and disabled while
 * waiting, with the title the caller gives, which names a secret check), and
 * "Resolve N blockers" that opens them while blocked, and "Save and debug"
 * while only the saved copy is gated.
 */
export function replayGateAction(gate: ReplayStartGate, { hasReport, readyTitle }: { hasReport: boolean; readyTitle: string }): ReplayGateAction {
  const start = { intent: 'start' as const, label: hasReport ? 'Debug again' : 'Debug one iteration', compactLabel: 'Debug', tone: 'success' as const, icon: 'run' as const }
  switch (gate.kind) {
    case 'ready': return { ...start, title: readyTitle, disabled: false }
    // A start that waits for a save it cannot make says why on the button.
    case 'waiting': return { ...start, title: gate.cause === 'save' ? gate.reason : readyTitle, disabled: true }
    case 'blocked': {
      const count = gate.blockers.length
      const [first] = gate.blockers
      return {
        intent: 'resolve',
        label: `Resolve ${plural(count, 'blocker')}`,
        compactLabel: 'Resolve',
        title: `${first.title}${count > 1 ? ` and ${count - 1} more` : ''}. ${REPLAY_REFUSAL_SENTENCE}`,
        // The accessible name starts with the visible label (WCAG 2.5.3).
        ariaLabel: `Resolve ${plural(count, 'blocker')} that refuse replay`,
        tone: 'danger',
        icon: 'blocker',
        disabled: false,
      }
    }
    case 'save-first': {
      const count = saveFirstProblemCount(gate)
      return {
        intent: 'save-and-start',
        label: 'Save and debug',
        compactLabel: 'Save & debug',
        title: `Your changes are not saved; the saved script still has ${plural(count, 'blocker')}. Save them, then debug.`,
        tone: 'success',
        icon: 'run',
        disabled: false,
      }
    }
  }
}
