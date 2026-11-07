// What the Author outline's selection can be wrapped in, renamed as or
// removed from: transactions. Pure, so the outline's menu, the bulk toolbar
// and tests share one set of rules.
//
// A transaction is a pair of source_transaction markers (lr_start_transaction
// / lr_end_transaction, "Timed span") around steps of one list, or a tag on
// each of a run of top-level steps ("Per step"). Markers may sit in a phase's
// list or in one list inside a block (an if's then or else, a loop, a switch
// case, a parallel branch): the engine runs a block's steps through the same
// path as a phase's. A sequence's requests are timed by the sequence itself.
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import {
  actionTransactionMarker,
  actionTransactionName,
  containerChildSteps,
  debugContainerKind,
  remapIndexAfterMarkerWrap,
  scriptActionsForPhase,
  setActionTransactionName,
  tagActionsTransaction,
  wrapActionsInTransactionMarkers,
  type ScriptActionPhase,
} from './scriptEditorWorkbench'
import { authorFocusFromKey, readSiblingList, replaceSiblingList, siblingListOf } from './scriptEditorJsonModel'
import { outlineNodeRows, renameTransactionMarker, splitMeasureName, type OutlineGroup, type OutlineModel } from './scriptOutlineModel'

/** A step of the script: a phase's top-level step, or one inside a block. */
export interface StepLocation {
  phase: ScriptActionPhase
  actionIndex: number
  stepPath?: string[]
}

/** Where a step before an edit is after it, or null when the edit removed it. */
export type LocationRemap = (location: StepLocation) => StepLocation | null

export type TransactionTiming = 'span' | 'per-step'

/** The steps one wrap brackets: [lo..hi] of one list. */
export interface WrapTarget {
  phase: ScriptActionPhase
  /** The phase step holding the list, or null for the phase's own list. */
  rootIndex: number | null
  /** The block holding the list, inside the root step. */
  parentPath: string[]
  /** A step path of the list's first selected step (to read and write the list). */
  memberPath: string[]
  lo: number
  hi: number
  /** Rows the selection named (a transaction counts as one). */
  selected: number
  /** Steps between the selected ones that the span also times. */
  between: number
  /** Whether "Per step" (tagging) is possible: top-level plain steps only. */
  perStep: boolean
  /** Per step: the indices to tag. */
  stepIndices: number[]
}

export type WrapResolution = { ok: true; target: WrapTarget } | { ok: false; reason: string }

const SAME_LEVEL = 'Select steps at the same level to wrap them together.'
const NOTHING = 'Select one or more steps to wrap.'
const REQUESTS = 'Requests are timed by their sequence. Wrap the sequence instead.'

interface Item {
  list: string
  phase: ScriptActionPhase
  rootIndex: number | null
  parentPath: string[]
  memberPath: string[]
  lo: number
  hi: number
  plain: boolean
}

function phaseOf(value: string): value is ScriptActionPhase {
  return value === 'init' || value === 'action' || value === 'teardown'
}

function itemOf(script: ScriptDefinition, model: OutlineModel, key: string): Item | string {
  const group = model.groupsByKey.get(key)
  if (group) {
    const rows = outlineNodeRows(group)
    if (!rows.length) return NOTHING
    const indices = rows.map(row => row.actionIndex)
    const phase = rows[0].phase as ScriptActionPhase
    return { list: phase, phase, rootIndex: null, parentPath: [], memberPath: [], lo: Math.min(...indices), hi: Math.max(...indices), plain: false }
  }
  const focus = authorFocusFromKey(key)
  if (!focus || !phaseOf(focus.phase)) return NOTHING
  const phaseList = scriptActionsForPhase(script, focus.phase)
  const action = phaseList[focus.actionIndex]
  if (!action) return NOTHING
  if (!focus.stepPath?.length) {
    // A step a sub-transaction measures goes with its two markers.
    const measured = model.stepsByKey.get(key)?.measuredAs
    if (measured) return { list: focus.phase, phase: focus.phase, rootIndex: null, parentPath: [], memberPath: [], lo: measured.start.actionIndex, hi: measured.end.actionIndex, plain: false }
    return { list: focus.phase, phase: focus.phase, rootIndex: null, parentPath: [], memberPath: [], lo: focus.actionIndex, hi: focus.actionIndex, plain: !actionTransactionMarker(action) }
  }
  const sibling = siblingListOf(focus.stepPath)
  if (!sibling) return NOTHING
  if (sibling.requests) return REQUESTS
  if (!readSiblingList(action, focus.stepPath)) return NOTHING
  return {
    list: `${focus.phase}:${focus.actionIndex}|${sibling.parentPath.join('/')}|${sibling.listId}`,
    phase: focus.phase,
    rootIndex: focus.actionIndex,
    parentPath: sibling.parentPath,
    memberPath: focus.stepPath,
    lo: sibling.index,
    hi: sibling.index,
    plain: false,
  }
}

function listOf(script: ScriptDefinition, target: Pick<WrapTarget, 'phase' | 'rootIndex' | 'memberPath'>): ScriptActionDefinition[] {
  const phaseList = scriptActionsForPhase(script, target.phase)
  if (target.rootIndex === null) return phaseList
  return readSiblingList(phaseList[target.rootIndex], target.memberPath) || []
}

/**
 * The transaction a span would cut across: one whose start or end marker is
 * inside [lo..hi] while its partner is outside. Different transactions may
 * nest; the engine refuses a start of one already open and an end of one not
 * open, so a span must hold both markers of each it touches.
 */
export function spanCrossing(items: ScriptActionDefinition[], lo: number, hi: number): string | null {
  const open = new Map<string, { name: string; count: number }>()
  for (let index = lo; index <= hi; index += 1) {
    const marker = items[index] ? actionTransactionMarker(items[index]) : null
    if (!marker) continue
    const key = `${marker.sub ? 'sub' : 'txn'}:${marker.name.toLowerCase()}`
    const entry = open.get(key) || { name: marker.name, count: 0 }
    if (marker.role === 'start') { entry.count += 1; open.set(key, entry); continue }
    if (entry.count === 0) return marker.name
    entry.count -= 1
    open.set(key, entry)
  }
  for (const entry of open.values()) if (entry.count > 0) return entry.name
  return null
}

function displayName(name: string): string {
  return name.includes('||') ? splitMeasureName(name).child : name
}

/**
 * What the outline rows `keys` (step, nested step, marker or transaction rows)
 * can be wrapped in: the span of one list they cover, or why not.
 */
export function resolveWrapTarget(script: ScriptDefinition, model: OutlineModel, keys: Iterable<string>): WrapResolution {
  const items: Item[] = []
  for (const key of keys) {
    const item = itemOf(script, model, key)
    if (typeof item === 'string') return { ok: false, reason: item }
    items.push(item)
  }
  if (!items.length) return { ok: false, reason: NOTHING }
  if (items.some(item => item.list !== items[0].list)) return { ok: false, reason: SAME_LEVEL }
  const first = items[0]
  const lo = Math.min(...items.map(item => item.lo))
  const hi = Math.max(...items.map(item => item.hi))
  const covered = new Set<number>()
  for (const item of items) for (let index = item.lo; index <= item.hi; index += 1) covered.add(index)
  const list = listOf(script, first)
  if (hi >= list.length) return { ok: false, reason: NOTHING }
  const crossing = spanCrossing(list, lo, hi)
  if (crossing) return { ok: false, reason: `The selection cuts across transaction “${displayName(crossing)}”. Include both its start and end, or select steps inside it.` }
  const memberPath = items.find(item => item.memberPath.length)?.memberPath || []
  const perStep = first.rootIndex === null && items.every(item => item.plain)
  return {
    ok: true,
    target: {
      phase: first.phase,
      rootIndex: first.rootIndex,
      parentPath: first.parentPath,
      memberPath,
      lo,
      hi,
      selected: new Set(items.map(item => `${item.lo}-${item.hi}`)).size,
      between: hi - lo + 1 - covered.size,
      perStep,
      stepIndices: perStep ? [...covered].sort((a, b) => a - b) : [],
    },
  }
}

function withPhaseList(script: ScriptDefinition, phase: ScriptActionPhase, actions: ScriptActionDefinition[]): ScriptDefinition {
  if (phase === 'init') return { ...script, init: actions }
  if (phase === 'teardown') return { ...script, teardown: actions }
  return { ...script, actions }
}

function withList(script: ScriptDefinition, target: Pick<WrapTarget, 'phase' | 'rootIndex' | 'memberPath'>, items: ScriptActionDefinition[]): ScriptDefinition {
  if (target.rootIndex === null) return withPhaseList(script, target.phase, items)
  const phaseList = scriptActionsForPhase(script, target.phase)
  const root = replaceSiblingList(phaseList[target.rootIndex], target.memberPath, items)
  if (!root) return script
  return withPhaseList(script, target.phase, phaseList.map((action, index) => index === target.rootIndex ? root : action))
}

/** Index `index` of a target's list, as a step location. */
function locationIn(target: Pick<WrapTarget, 'phase' | 'rootIndex' | 'parentPath' | 'memberPath'>, index: number): StepLocation {
  if (target.rootIndex === null) return { phase: target.phase, actionIndex: index }
  const sibling = siblingListOf(target.memberPath)!
  return { phase: target.phase, actionIndex: target.rootIndex, stepPath: [...target.parentPath, sibling.segmentAt(index)] }
}

/**
 * A remap for an edit of one list: `shift` maps an index of the list to its
 * new index (or null when removed). Steps elsewhere keep their place; steps
 * inside a shifted step move with it.
 */
function listRemap(target: Pick<WrapTarget, 'phase' | 'rootIndex' | 'parentPath' | 'memberPath'>, shift: (index: number) => number | null): LocationRemap {
  const sibling = target.rootIndex === null ? null : siblingListOf(target.memberPath)
  return location => {
    if (location.phase !== target.phase) return location
    if (target.rootIndex === null) {
      const next = shift(location.actionIndex)
      return next === null ? null : next === location.actionIndex ? location : { ...location, actionIndex: next }
    }
    const path = location.stepPath || []
    const depth = target.parentPath.length
    if (location.actionIndex !== target.rootIndex || path.length <= depth) return location
    if (target.parentPath.some((segment, index) => path[index] !== segment)) return location
    const own = siblingListOf(path.slice(0, depth + 1))
    if (!own || !sibling || own.listId !== sibling.listId) return location
    const next = shift(own.index)
    if (next === null) return null
    if (next === own.index) return location
    return { ...location, stepPath: [...target.parentPath, own.segmentAt(next), ...path.slice(depth + 1)] }
  }
}

export interface TransactionEdit {
  script: ScriptDefinition
  remap: LocationRemap
  /** The step to select afterwards (a new transaction's start). */
  focus: StepLocation | null
}

/** Wrap a resolved target in transaction `name`. */
export function wrapInTransaction(script: ScriptDefinition, target: WrapTarget, name: string, timing: TransactionTiming): TransactionEdit {
  const trimmed = name.trim()
  const list = listOf(script, target)
  if (!trimmed || target.hi >= list.length) return { script, remap: location => location, focus: null }
  if (timing === 'per-step' && target.perStep) {
    return {
      script: withList(script, target, tagActionsTransaction(list, target.stepIndices, trimmed)),
      remap: location => location,
      focus: locationIn(target, target.stepIndices[0] ?? target.lo),
    }
  }
  return {
    script: withList(script, target, wrapActionsInTransactionMarkers(list, target.lo, target.hi, trimmed)),
    remap: listRemap(target, index => remapIndexAfterMarkerWrap(index, target.lo, target.hi)),
    focus: locationIn(target, target.lo),
  }
}

/** A transaction group of the outline (a phase's own list), as a target. */
function groupList(group: OutlineGroup) {
  const rows = outlineNodeRows(group)
  const phase = (group.start?.phase || rows[0]?.phase || 'action') as ScriptActionPhase
  const indices = rows.map(row => row.actionIndex)
  return { phase, rows, lo: Math.min(...indices), hi: Math.max(...indices) }
}

/** Whether a group is a marker pair (vs a run of tagged steps). */
export function groupHasMarkers(group: OutlineGroup): boolean {
  return Boolean(group.start)
}

/**
 * Each step list inside `action`'s blocks (an if's then and else, a loop, a
 * switch's cases and default, a parallel's branches), innermost first, mapped
 * by `fn` with the block's path and how a step of the list is named. A
 * sequence's requests are not steps the engine runs on their own.
 */
type ListMapper = (items: ScriptActionDefinition[], parentPath: string[], segmentAt: (index: number) => string) => ScriptActionDefinition[]
function mapBlockLists(action: ScriptActionDefinition, parentPath: string[], fn: ListMapper): ScriptActionDefinition {
  const kind = debugContainerKind(action)
  const control = action.control
  if (!kind || kind === 'http_sequence' || !control) return action
  const each = (items: ScriptActionDefinition[], segmentAt: (index: number) => string) =>
    fn(items.map((child, index) => mapBlockLists(child, [...parentPath, segmentAt(index)], fn)), parentPath, segmentAt)
  const next = { ...control }
  if (kind === 'control_if') {
    if (control.steps) next.steps = each(control.steps, index => `then[${index}]`)
    if (control.else) next.else = each(control.else, index => `else[${index}]`)
  } else if (kind === 'control_loop') {
    if (control.steps) next.steps = each(control.steps, index => `loop[${index}]`)
  } else if (kind === 'control_switch') {
    if (control.cases) next.cases = control.cases.map((item, caseIndex) => item.steps ? { ...item, steps: each(item.steps, index => `case[${caseIndex}][${index}]`) } : item)
    if (control.default) next.default = each(control.default, index => `default[${index}]`)
  } else if (control.branches) {
    next.branches = control.branches.map((branch, branchIndex) => branch.steps ? { ...branch, steps: each(branch.steps, index => `branches[${branchIndex}].steps[${index}]`) } : branch)
  }
  return { ...action, control: next }
}

// The engine matches transaction names without regard to case.
const sameName = (value: unknown, name: string) => typeof value === 'string' && value.trim().toLowerCase() === name.trim().toLowerCase()

const lowerName = (name: string) => name.trim().toLowerCase()

/** The parent a sub-transaction start names in its second argument ("name", "parent"), or ''. */
function subParentOf(action: ScriptActionDefinition): string {
  const args = (action.params as Record<string, unknown> | undefined)?.args
  return Array.isArray(args) && typeof args[1] === 'string' ? args[1].trim() : ''
}

/** Whether a sub-transaction start belongs under one of `parents` (lower-cased): it names one as its parent, or is named "parent||…". */
function startTiedTo(action: ScriptActionDefinition, parents: Set<string>): boolean {
  const marker = actionTransactionMarker(action)
  if (!marker?.sub || marker.role !== 'start') return false
  const name = marker.name.toLowerCase()
  return parents.has(lowerName(subParentOf(action))) || [...parents].some(parent => name.startsWith(`${parent}||`))
}

/**
 * Transaction `parent` and the sub-transactions under it started in `items`,
 * at any depth, by lower-cased name. The engine ties a sub-transaction to its
 * parent by the start's second argument, and a sub-transaction may be another's
 * parent, so the set grows until nothing more joins it.
 */
function transactionFamily(items: ScriptActionDefinition[], parent: string): Set<string> {
  const starts: ScriptActionDefinition[] = []
  const collect = (action: ScriptActionDefinition) => { const marker = actionTransactionMarker(action); if (marker?.sub && marker.role === 'start') starts.push(action) }
  for (const action of items) {
    collect(action)
    mapBlockLists(action, [], list => { list.forEach(collect); return list })
  }
  const family = new Set([lowerName(parent)])
  for (let grew = true; grew;) {
    grew = false
    for (const start of starts) {
      const name = lowerName(actionTransactionMarker(start)!.name)
      if (!family.has(name) && startTiedTo(start, family)) { family.add(name); grew = true }
    }
  }
  return family
}

/**
 * A marker's call with its transaction named `name`: for a start, `parent` is
 * the parent it names as a sub-transaction, null when it is (now) a
 * transaction's own, and undefined to keep what it is. The step's name follows.
 */
function withMarkerCall(action: ScriptActionDefinition, name: string, parent?: string | null): ScriptActionDefinition {
  const marker = actionTransactionMarker(action)
  if (!marker) return action
  const sub = parent === undefined ? Boolean(marker.sub) : parent !== null
  const params: Record<string, unknown> = { ...((action.params as Record<string, unknown> | undefined) || {}) }
  // A change of kind writes the canonical call (an import may use an alias such as lr_end_sub_trans).
  if (sub !== Boolean(marker.sub)) params.function = `lr_${marker.role}_${sub ? 'sub_' : ''}transaction`
  const args = Array.isArray(params.args) ? [...params.args] : [name]
  args[0] = name
  if (marker.role === 'start') {
    if (sub && typeof parent === 'string') args[1] = parent
    else if (!sub && marker.sub) args.splice(1, 1)
  }
  params.args = args
  if (Array.isArray(params.call_args)) {
    const callArgs = [...params.call_args] as unknown[]
    const set = (index: number, value: string) => {
      const current = callArgs[index]
      if (current && typeof current === 'object') callArgs[index] = { ...(current as Record<string, unknown>), value, raw: JSON.stringify(value) }
    }
    set(0, name)
    if (marker.role === 'start') {
      if (sub && typeof parent === 'string') set(1, parent)
      else if (!sub && marker.sub) callArgs.splice(1, 1)
    }
    params.call_args = callArgs
  }
  const label = action.name && marker.name && action.name.includes(marker.name) ? action.name.split(marker.name).join(name) : action.name
  return { ...action, name: label, params } as ScriptActionDefinition
}

/** A tied sub-transaction start still open where an end may close it, with what was decided for it. */
interface OpenMember { name: string; promoted: boolean }

interface TransactionRewrite {
  /** Its own start or end; null removes it. */
  own: (action: ScriptActionDefinition) => ScriptActionDefinition | null
  /** A tied sub-transaction start: the rewritten start, and whether it became a transaction of its own. */
  start: (action: ScriptActionDefinition) => { action: ScriptActionDefinition; promoted: boolean }
  /** The end that closes a rewritten start. */
  end: (action: ScriptActionDefinition, opened: OpenMember) => ScriptActionDefinition
  /** A step's tags (an http_sequence's requests carry them too). */
  retag: (action: ScriptActionDefinition) => ScriptActionDefinition
}

interface TransactionWalk {
  /** Indices removed from each list (its own markers, and a block they left empty), by list. */
  removed: Map<string, Set<number>>
  /** Why the edit cannot be made, or ''. */
  problem: string
}

/** A list's identity for remapping: the phase list, or one list of a block of the step at `rootIndex`. */
function listKeyOf(rootIndex: number | null, parentPath: string[], firstSegment: string | null): string {
  if (rootIndex === null) return 'phase'
  return `${rootIndex}|${parentPath.join('/')}|${firstSegment ? siblingListOf([firstSegment])?.listId ?? '' : ''}`
}

type BlockList = {
  items: ScriptActionDefinition[]
  segmentAt: (index: number) => string
  /** A switch case or parallel branch may not be left empty (the validator refuses it). */
  required: boolean
  put: (control: NonNullable<ScriptActionDefinition['control']>, items: ScriptActionDefinition[]) => void
}

/** A block's lists, each with how its steps are named and how to put it back. */
function blockLists(action: ScriptActionDefinition): BlockList[] {
  const kind = debugContainerKind(action)
  const control = action.control
  if (!kind || kind === 'http_sequence' || !control) return []
  const lists: BlockList[] = []
  if (kind === 'control_if') {
    if (control.steps) lists.push({ items: control.steps, segmentAt: index => `then[${index}]`, required: false, put: (next, items) => { next.steps = items } })
    if (control.else) lists.push({ items: control.else, segmentAt: index => `else[${index}]`, required: false, put: (next, items) => { next.else = items } })
  } else if (kind === 'control_loop') {
    if (control.steps) lists.push({ items: control.steps, segmentAt: index => `loop[${index}]`, required: false, put: (next, items) => { next.steps = items } })
  } else if (kind === 'control_switch') {
    ;(control.cases || []).forEach((item, caseIndex) => {
      if (item.steps) lists.push({ items: item.steps, segmentAt: index => `case[${caseIndex}][${index}]`, required: true, put: (next, items) => { next.cases = (next.cases || []).map((current, at) => at === caseIndex ? { ...current, steps: items } : current) } })
    })
    if (control.default) lists.push({ items: control.default, segmentAt: index => `default[${index}]`, required: false, put: (next, items) => { next.default = items } })
  } else {
    ;(control.branches || []).forEach((branch, branchIndex) => {
      if (branch.steps) lists.push({ items: branch.steps, segmentAt: index => `branches[${branchIndex}].steps[${index}]`, required: true, put: (next, items) => { next.branches = (next.branches || []).map((current, at) => at === branchIndex ? { ...current, steps: items } : current) } })
    })
  }
  return lists
}

/** A pause call (lr_stop/resume_transaction) naming `from`, renamed to `to`. */
function renamePause(action: ScriptActionDefinition, from: string, to: string): ScriptActionDefinition {
  const params: Record<string, unknown> = { ...((action.params as Record<string, unknown> | undefined) || {}) }
  const was = Array.isArray(params.args) ? params.args[0] : undefined
  if (!sameName(was, from)) return action
  params.args = [to, ...(params.args as unknown[]).slice(1)]
  if (Array.isArray(params.call_args) && params.call_args[0] && typeof params.call_args[0] === 'object') {
    params.call_args = [{ ...(params.call_args[0] as Record<string, unknown>), value: to, raw: JSON.stringify(to) }, ...params.call_args.slice(1)]
  }
  const label = action.name && typeof was === 'string' && action.name.includes(was) ? action.name.split(was).join(to) : action.name
  return { ...action, name: label, params } as ScriptActionDefinition
}

/** lr_stop_transaction / lr_resume_transaction for transaction `name`: a pause, not a marker. */
function pausesTransaction(action: ScriptActionDefinition, name: string): boolean {
  if ((action.type || '').toLowerCase() !== 'source_transaction') return false
  const params = action.params as Record<string, unknown> | undefined
  const fn = String(params?.function || '').toLowerCase()
  const args = Array.isArray(params?.args) ? params.args : []
  return (fn.includes('stop') || fn.includes('resume')) && sameName(args[0], name)
}

const hasThinkTime = (action: ScriptActionDefinition) => Boolean(action.think_time_after)

interface ListWalk {
  items: ScriptActionDefinition[]
  /** Think time a removed marker carried that no step in the list could take (it went first). */
  leftover: ScriptActionDefinition['think_time'] | null
  /** Inherited sub-transactions this list closed. */
  closed: Set<OpenMember>
}

/**
 * The steps of a list (and of the blocks inside them, at any depth) with a
 * transaction's markers rewritten. A tied sub-transaction's start and the end
 * that closes it are rewritten together, even when the end is in a block
 * inside the start's list (an import writes `if (…) lr_end_…(LR_PASS); else
 * lr_end_…(LR_FAIL);`); another transaction's sub-transaction of the same name
 * is left alone. An if or loop that loses every step to the edit goes too. The
 * think time a removed marker carried moves to the step before it.
 */
function walkTransaction(
  items: ScriptActionDefinition[],
  where: { rootIndex: number | null; parentPath: string[]; segmentAt: ((index: number) => string) | null; offset: number },
  own: { name: string; sub: boolean },
  family: Set<string>,
  rewrite: TransactionRewrite,
  inherited: OpenMember[],
  walk: TransactionWalk,
): ListWalk {
  const open = [...inherited]
  const closed = new Set<OpenMember>()
  const out: ScriptActionDefinition[] = []
  const removed = new Set<number>()
  let leftover: ListWalk['leftover'] = null
  // A removed step's think time goes onto the step before it, as think time after it.
  const keepThinkTime = (think: ScriptActionDefinition['think_time']) => {
    const previous = out[out.length - 1]
    if (!previous) { if (leftover) walk.problem = THINK_PROBLEM; leftover = think; return }
    if (hasThinkTime(previous)) { walk.problem = THINK_PROBLEM; return }
    out[out.length - 1] = { ...previous, think_time: think, think_time_after: true }
  }
  const drop = (at: number, item: ScriptActionDefinition) => {
    removed.add(at)
    if (hasThinkTime(item)) keepThinkTime(item.think_time)
  }
  items.forEach((item, at) => {
    const marker = actionTransactionMarker(item)
    if (!marker) {
      if (pausesTransaction(item, own.name)) {
        const next = rewrite.own(item)
        if (next) out.push(next)
        else drop(at, item)
        return
      }
      const lists = blockLists(item)
      if (!lists.length) { out.push(rewrite.retag(item)); return }
      const rootIndex = where.rootIndex ?? where.offset + at
      const path = where.rootIndex === null ? [] : [...where.parentPath, where.segmentAt!(at)]
      const control = { ...item.control! }
      const leftovers: Array<ListWalk['leftover']> = []
      const closedInAll: Set<OpenMember>[] = []
      for (const list of lists) {
        const next = walkTransaction(list.items, { rootIndex, parentPath: path, segmentAt: list.segmentAt, offset: 0 }, own, family, rewrite, open, walk)
        if (list.required && list.items.length && !next.items.length) walk.problem = 'Removing it would leave a switch case or parallel branch with no steps. Remove the markers inside that block first.'
        leftovers.push(next.leftover)
        closedInAll.push(next.closed)
        list.put(control, next.items)
      }
      // A sub-transaction every list of the block closes is closed after it.
      const kind = debugContainerKind(item)
      const everyPath = kind === 'control_loop' || (kind === 'control_if' && Array.isArray(item.control?.steps) && Array.isArray(item.control?.else))
      for (const member of closedInAll[0] || []) {
        if (everyPath && closedInAll.every(set => set.has(member))) {
          const index = open.indexOf(member)
          if (index >= 0) open.splice(index, 1)
          if (inherited.includes(member)) closed.add(member)
        }
      }
      const block = { ...item, control } as ScriptActionDefinition
      const stepsLeft = blockLists(block).some(list => list.items.length > 0)
      const anyLeftover = leftovers.some(Boolean)
      if ((kind === 'control_if' || kind === 'control_loop') && !stepsLeft && blockLists(item).some(list => list.items.length > 0)) {
        drop(at, item)
        // A wait the block made on every path stays; one only some paths made cannot.
        if (anyLeftover) {
          if (everyPath && leftovers.every(think => JSON.stringify(think) === JSON.stringify(leftovers[0]))) keepThinkTime(leftovers[0] ?? undefined)
          else walk.problem = THINK_PROBLEM
        }
        return
      }
      if (anyLeftover) walk.problem = THINK_PROBLEM
      out.push(rewrite.retag(block))
      return
    }
    if (Boolean(marker.sub) === own.sub && marker.name === own.name) {
      const next = rewrite.own(item)
      if (next) out.push(next)
      else drop(at, item)
      return
    }
    const name = marker.name.toLowerCase()
    if (marker.sub && marker.role === 'start' && startTiedTo(item, family)) {
      const started = rewrite.start(item)
      open.push({ name, promoted: started.promoted })
      out.push(started.action)
      return
    }
    const closing = marker.sub && marker.role === 'end' ? open.map(entry => entry.name).lastIndexOf(name) : -1
    if (closing >= 0) {
      const [opened] = open.splice(closing, 1)
      if (inherited.includes(opened)) closed.add(opened)
      out.push(rewrite.end(item, opened))
      return
    }
    out.push(item)
  })
  if (removed.size) walk.removed.set(listKeyOf(where.rootIndex, where.parentPath, where.segmentAt ? where.segmentAt(0) : null), new Set([...removed].map(index => index + where.offset)))
  return { items: out, leftover, closed }
}

const THINK_PROBLEM = 'Its start or end marker carries think time that cannot move to the step before it (that step has think time of its own, there is none, or only some branches wait). Move or clear that think time first, then remove the transaction.'

/**
 * A marker group's span in its phase list. A transaction an import ends in an
 * if/else has no end the outline sees, so its group runs on: it ends at that
 * if/else, or else before the next start of the same transaction (the engine
 * cannot start it twice).
 */
function markerSpan(list: ScriptActionDefinition[], group: OutlineGroup, lo: number, hi: number): number {
  if (!group.start || group.end) return hi
  const sub = group.kind === 'subtransaction'
  const restarts = (action: ScriptActionDefinition): boolean => {
    const marker = actionTransactionMarker(action)
    if (marker) return marker.role === 'start' && Boolean(marker.sub) === sub && sameName(marker.name, group.name)
    return blockLists(action).some(block => block.items.some(restarts))
  }
  // An if/else whose every branch ends it (`if (…) lr_end_…(LR_PASS); else lr_end_…(LR_FAIL);`) is where it ends.
  const endsOnEveryPath = (action: ScriptActionDefinition) => debugContainerKind(action) === 'control_if'
    && Array.isArray(action.control?.steps) && Array.isArray(action.control?.else)
    && [action.control!.steps!, action.control!.else!].every(branch => branch.some(item => {
      const marker = actionTransactionMarker(item)
      return marker?.role === 'end' && Boolean(marker.sub) === sub && sameName(marker.name, group.name)
    }))
  for (let index = lo + 1; index <= hi; index += 1) {
    if (!list[index]) continue
    // An if/else that ends it on every path is its end, even when a branch then retries it.
    if (endsOnEveryPath(list[index])) return index
    if (restarts(list[index])) return index - 1
  }
  return hi
}

/** A name or parent under `from` ("from||rest") moved under `to` ("to||rest", or "rest" when `to` is empty); others as they are. */
function moveUnder(value: string, from: string, to: string): string {
  const prefix = `${from.toLowerCase()}||`
  if (!value.toLowerCase().startsWith(prefix)) return value
  const rest = value.slice(prefix.length)
  return to ? `${to}||${rest}` : rest
}

/** A step's tag (and its sequence requests' tags) renamed from `from` to `to` ('' clears it). */
function retagFrom(from: string, to: string) {
  const retag = (action: ScriptActionDefinition) => sameName(actionTransactionName(action), from) ? setActionTransactionName(action, to) : action
  return (action: ScriptActionDefinition) => {
    const next = retag(action)
    const requests = (next.params as Record<string, unknown> | undefined)?.requests
    if (debugContainerKind(next) !== 'http_sequence' || !Array.isArray(requests)) return next
    const retagged = requests.map(request => request && typeof request === 'object' ? retag(request as ScriptActionDefinition) : request)
    return retagged.some((request, index) => request !== requests[index]) ? { ...next, params: { ...(next.params || {}), requests: retagged } } as ScriptActionDefinition : next
  }
}

/** A remap for a walk's removals: a location in a removed step goes; later ones in its list move up. */
function walkRemap(phase: ScriptActionPhase, walk: TransactionWalk): LocationRemap {
  const shiftIn = (removed: Set<number> | undefined, index: number) => {
    if (!removed?.size) return index
    if (removed.has(index)) return null
    return index - [...removed].filter(at => at < index).length
  }
  return location => {
    if (location.phase !== phase) return location
    const actionIndex = shiftIn(walk.removed.get('phase'), location.actionIndex)
    if (actionIndex === null) return null
    const path = location.stepPath || []
    const nextPath: string[] = []
    for (let depth = 0; depth < path.length; depth += 1) {
      const sibling = siblingListOf(path.slice(0, depth + 1))
      if (!sibling) return location
      const moved = shiftIn(walk.removed.get(listKeyOf(location.actionIndex, sibling.parentPath, path[depth])), sibling.index)
      if (moved === null) return null
      nextPath.push(sibling.segmentAt(moved))
    }
    if (actionIndex === location.actionIndex && nextPath.every((segment, depth) => segment === path[depth])) return location
    return { ...location, actionIndex, stepPath: path.length ? nextPath : location.stepPath }
  }
}

/**
 * Rename a transaction group to `to` (for a sub-transaction, its full
 * "Parent||child" name). The sub-transactions under it, inside blocks and
 * under one another too, follow: a "parent||…" name takes the new name, and a
 * start naming it (or a sub-transaction under it) as its parent names the new
 * one. Steps tagged with it, and a sequence's requests, are re-tagged.
 */
export function renameTransaction(script: ScriptDefinition, group: OutlineGroup, to: string): ScriptDefinition {
  const next = to.trim()
  const from = group.name
  if (!next || next === from) return script
  const { phase, rows, lo, hi } = groupList(group)
  const list = scriptActionsForPhase(script, phase)
  if (!groupHasMarkers(group)) {
    const indices = new Set(rows.filter(row => actionTransactionName(row.action) === from).map(row => row.actionIndex))
    const retag = retagFrom(from, next)
    return withPhaseList(script, phase, list.map((action, index) => indices.has(index) ? retag(action) : action))
  }
  const last = markerSpan(list, group, lo, hi)
  const span = list.slice(lo, last + 1)
  const walk: TransactionWalk = { removed: new Map(), problem: '' }
  const renamed = walkTransaction(span, { rootIndex: null, parentPath: [], segmentAt: null, offset: lo }, { name: from, sub: group.kind === 'subtransaction' }, transactionFamily(span, from), {
    own: action => actionTransactionMarker(action) ? renameTransactionMarker(action, from, next) : renamePause(action, from, next),
    start: action => {
      const parent = subParentOf(action)
      return { action: withMarkerCall(action, moveUnder(actionTransactionMarker(action)!.name, from, next), sameName(parent, from) ? next : moveUnder(parent, from, next)), promoted: false }
    },
    end: action => withMarkerCall(action, moveUnder(actionTransactionMarker(action)!.name, from, next)),
    retag: retagFrom(from, next),
  }, [], walk).items
  return withPhaseList(script, phase, [...list.slice(0, lo), ...renamed, ...list.slice(last + 1)])
}

export interface TransactionRemoval extends TransactionEdit {
  /** Sub-transactions that moved up a level, and how many of them are now transactions of their own. */
  movedUp: number
  promoted: number
  /** Why nothing was removed, or ''. */
  problem: string
  /** Request groups it owned, which now time their own requests (under their own names). */
  sequences: string[]
}

/**
 * Remove a transaction and keep everything inside it: its start and end
 * markers go (an if or loop left with nothing but them goes too) and its steps
 * lose its tag. The sub-transactions under it move up a level and keep
 * measuring their steps: under a transaction's removal each becomes a
 * transaction of its own ("Login||submit" becomes "submit"); under a
 * sub-transaction's, a sub-transaction of its parent. A tagged run's steps
 * lose the tag.
 */
export function removeTransaction(script: ScriptDefinition, group: OutlineGroup): TransactionRemoval {
  const { phase, rows, lo, hi } = groupList(group)
  const list = scriptActionsForPhase(script, phase)
  const unchanged = { script, remap: (location: StepLocation) => location, focus: null, movedUp: 0, promoted: 0, problem: '', sequences: [] as string[] }
  if (!groupHasMarkers(group)) {
    const indices = new Set(rows.filter(row => actionTransactionName(row.action) === group.name).map(row => row.actionIndex))
    const retag = retagFrom(group.name, '')
    return {
      ...unchanged,
      script: withPhaseList(script, phase, list.map((action, index) => indices.has(index) ? retag(action) : action)),
      focus: indices.size ? { phase, actionIndex: Math.min(...indices) } : null,
    }
  }
  const removed = group.name
  const sub = group.kind === 'subtransaction'
  // Where its sub-transactions go: up to its own parent, or out to stand alone.
  const parent = sub && group.start ? subParentOf(group.start.action) || removed.slice(0, Math.max(0, removed.lastIndexOf('||'))) : ''
  const last = markerSpan(list, group, lo, hi)
  const span = list.slice(lo, last + 1)
  let movedUp = 0
  let promoted = 0
  // A request group (http_sequence) the transaction owned times its requests again once it is gone.
  const sequences: string[] = []
  const noteSequences = (action: ScriptActionDefinition) => {
    // A transparent concurrent group records no transaction of its own.
    const transparent = action.metadata?.transparent_request_group === true && (action.params as Record<string, unknown> | undefined)?.execution_mode === 'concurrent'
    if (debugContainerKind(action) === 'http_sequence' && !transparent && sameName(actionTransactionName(action), removed)) sequences.push(action.name || 'Request group')
    blockLists(action).forEach(list => list.items.forEach(noteSequences))
  }
  span.forEach(noteSequences)
  const walk: TransactionWalk = { removed: new Map(), problem: '' }
  const kept = walkTransaction(span, { rootIndex: null, parentPath: [], segmentAt: null, offset: lo }, { name: removed, sub }, transactionFamily(span, removed), {
    own: () => null,
    start: action => {
      const was = subParentOf(action)
      const next = sameName(was, removed) ? parent : moveUnder(was, removed, parent)
      movedUp += 1
      if (!next) promoted += 1
      return { action: withMarkerCall(action, moveUnder(actionTransactionMarker(action)!.name, removed, parent), next || null), promoted: !next }
    },
    end: (action, opened) => withMarkerCall(action, moveUnder(actionTransactionMarker(action)!.name, removed, parent), opened.promoted ? null : undefined),
    retag: retagFrom(removed, ''),
  }, [], walk)
  if (walk.problem) return { ...unchanged, problem: walk.problem }
  // Think time its start marker carried goes to the step that runs before the transaction.
  const before = list.slice(0, lo)
  if (kept.leftover) {
    const previous = before[before.length - 1]
    if (!previous || hasThinkTime(previous)) return { ...unchanged, problem: THINK_PROBLEM }
    before[before.length - 1] = { ...previous, think_time: kept.leftover, think_time_after: true }
  }
  const remap = walkRemap(phase, walk)
  const top = walk.removed.get('phase') || new Set<number>()
  const firstKept = (() => { for (let index = lo; index <= last; index += 1) if (!top.has(index)) return remap({ phase, actionIndex: index })?.actionIndex ?? null; return null })()
  return {
    script: withPhaseList(script, phase, [...before, ...kept.items, ...list.slice(last + 1)]),
    remap,
    focus: firstKept === null ? null : { phase, actionIndex: firstKept },
    movedUp,
    promoted,
    problem: '',
    sequences,
  }
}

/**
 * Why `name` cannot name a transaction around [lo..hi] of a list: the engine
 * refuses to start a transaction that is already open, so a transaction may
 * not share a name with one around it or inside it. (Ones before or after it
 * may: their times are reported together.) `exceptName` is a transaction's
 * own name, when renaming it.
 */
export function nestedNameConflict(script: ScriptDefinition, target: Pick<WrapTarget, 'phase' | 'rootIndex' | 'memberPath' | 'lo' | 'hi'>, name: string, exceptName = ''): string {
  const wanted = name.trim().toLowerCase()
  const except = exceptName.trim().toLowerCase()
  if (!wanted || wanted === except) return ''
  const message = 'A transaction with this name is already open around or inside these steps; the engine cannot start it twice. Choose another name.'
  const nameOf = (action: ScriptActionDefinition) => (actionTransactionMarker(action)?.name || '').toLowerCase()
  // Open around the span: before it in its list, and before each block that holds the list.
  const openBefore = (items: ScriptActionDefinition[], index: number) => {
    const open = new Map<string, number>()
    for (const action of items.slice(0, index)) {
      const marker = actionTransactionMarker(action)
      if (!marker) continue
      const key = marker.name.toLowerCase()
      open.set(key, (open.get(key) || 0) + (marker.role === 'start' ? 1 : -1))
    }
    return (open.get(wanted) || 0) > 0
  }
  const phaseList = scriptActionsForPhase(script, target.phase)
  if (target.rootIndex === null) {
    if (openBefore(phaseList, target.lo)) return message
  } else {
    if (openBefore(phaseList, target.rootIndex)) return message
    const path = target.memberPath
    for (let depth = 0; depth < path.length - 1; depth += 1) {
      const sibling = siblingListOf(path.slice(0, depth + 1))
      if (sibling && openBefore(readSiblingList(phaseList[target.rootIndex], path.slice(0, depth + 1)) || [], sibling.index)) return message
    }
    if (openBefore(listOf(script, target), target.lo)) return message
  }
  // Inside the span, at any depth.
  let inside = false
  const visit = (action: ScriptActionDefinition) => {
    if (nameOf(action) === wanted && nameOf(action) !== except) inside = true
    mapBlockLists(action, [], items => { items.forEach(item => { if (nameOf(item) === wanted) inside = true }); return items })
  }
  listOf(script, target).slice(target.lo, target.hi + 1).forEach(visit)
  return inside ? message : ''
}

/** A transaction group of the outline as a span of its phase's list (for nestedNameConflict when renaming). */
export function groupSpan(script: ScriptDefinition, group: OutlineGroup): Pick<WrapTarget, 'phase' | 'rootIndex' | 'memberPath' | 'lo' | 'hi'> {
  const { phase, lo, hi } = groupList(group)
  return { phase, rootIndex: null, memberPath: [], lo, hi: markerSpan(scriptActionsForPhase(script, phase), group, lo, hi) }
}

export interface StepRemoval extends TransactionEdit {
  /** Top-level steps removed, markers included. */
  removed: number
}

/**
 * Delete the top-level outline rows `keys`: steps, markers, and transactions
 * with every step inside them; a step a sub-transaction measures goes with its
 * markers. Null when a key names a nested step (those go one at a time).
 */
export function removeTopLevelRows(script: ScriptDefinition, model: OutlineModel, keys: Iterable<string>): StepRemoval | null {
  const byPhase = new Map<ScriptActionPhase, Set<number>>()
  const add = (phase: ScriptActionPhase, index: number) => {
    const set = byPhase.get(phase) || new Set<number>()
    set.add(index)
    byPhase.set(phase, set)
  }
  for (const key of keys) {
    const group = model.groupsByKey.get(key)
    if (group) { for (const row of outlineNodeRows(group)) add(row.phase as ScriptActionPhase, row.actionIndex); continue }
    const focus = authorFocusFromKey(key)
    if (!focus || !phaseOf(focus.phase) || focus.stepPath?.length) return null
    const step = model.stepsByKey.get(key)
    if (step) { for (const row of outlineNodeRows(step)) add(row.phase as ScriptActionPhase, row.actionIndex); continue }
    add(focus.phase, focus.actionIndex)
  }
  let next = script
  let removed = 0
  for (const [phase, indices] of byPhase) {
    const list = scriptActionsForPhase(next, phase)
    removed += [...indices].filter(index => index < list.length).length
    next = withPhaseList(next, phase, list.filter((_, index) => !indices.has(index)))
  }
  const remap: LocationRemap = location => {
    const indices = byPhase.get(location.phase)
    if (!indices) return location
    if (indices.has(location.actionIndex)) return null
    const below = [...indices].filter(index => index < location.actionIndex).length
    return below ? { ...location, actionIndex: location.actionIndex - below } : location
  }
  return { script: next, remap, focus: null, removed }
}

/** Every transaction name the script uses (markers and tags, at any depth), lower-cased. */
export function scriptTransactionNames(script: ScriptDefinition): Set<string> {
  const names = new Set<string>()
  const visit = (actions: ScriptActionDefinition[]) => {
    for (const action of actions) {
      if (!action) continue
      const name = actionTransactionMarker(action)?.name || actionTransactionName(action)
      if (name) names.add(name.toLowerCase())
      const kind = debugContainerKind(action)
      if (kind) visit(containerChildSteps(action, kind).map(step => step.action))
    }
  }
  for (const phase of ['init', 'action', 'teardown'] as ScriptActionPhase[]) visit(scriptActionsForPhase(script, phase))
  return names
}

/** A default name no transaction of the script uses ("Transaction 1", ...). */
export function defaultTransactionName(script: ScriptDefinition): string {
  const used = scriptTransactionNames(script)
  let n = 1
  while (used.has(`transaction ${n}`)) n += 1
  return `Transaction ${n}`
}

/** Why a transaction name cannot be used, or ''. */
export function transactionNameProblem(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return 'Enter a name.'
  if (trimmed.includes('||')) return 'A name cannot contain “||”.'
  if (new TextEncoder().encode(trimmed).length > 256) return 'Use 256 characters or fewer.'
  return ''
}

/** A remap applied to a location key (top-level "phase:i", nested "phase:i#path" or "phase:i|path", request "phase:i:n"). */
export function remapLocationKey(key: string, remap: LocationRemap): string | null {
  const match = key.match(/^(init|action|teardown):(\d+)(?:([:#|])(.+))?$/)
  if (!match) return key
  const separator = match[3]
  const focus = authorFocusFromKey(key)
  if (!focus) return key
  const moved = remap({ phase: focus.phase, actionIndex: focus.actionIndex, stepPath: focus.stepPath })
  if (!moved) return null
  const base = `${moved.phase}:${moved.actionIndex}`
  if (!separator) return base
  if (separator === ':') {
    const last = moved.stepPath?.[moved.stepPath.length - 1]
    const request = last ? /^requests\[(\d+)\]$/.exec(last) : null
    return request && moved.stepPath!.length === 1 ? `${base}:${request[1]}` : `${base}#${(moved.stepPath || []).join('/')}`
  }
  return `${base}${separator}${(moved.stepPath || []).join('/')}`
}
