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

/**
 * A start's parent argument renamed from `from` to `to`: the parent itself, or
 * a sub-transaction under it ("from||a" becomes "to||a").
 */
function renameParentArgument(action: ScriptActionDefinition, from: string, to: string): ScriptActionDefinition {
  const rewrite = (value: unknown): string | null => {
    if (typeof value !== 'string') return null
    const trimmed = value.trim()
    if (sameName(trimmed, from)) return to
    return trimmed.toLowerCase().startsWith(`${lowerName(from)}||`) ? `${to}||${trimmed.slice(from.trim().length + 2)}` : null
  }
  const params: Record<string, unknown> = { ...((action.params as Record<string, unknown> | undefined) || {}) }
  let changed = false
  const parentArg = Array.isArray(params.args) ? rewrite(params.args[1]) : null
  if (parentArg !== null && Array.isArray(params.args)) { params.args = [params.args[0], parentArg, ...params.args.slice(2)]; changed = true }
  if (Array.isArray(params.call_args)) {
    const second = params.call_args[1] as Record<string, unknown> | undefined
    const value = second && typeof second === 'object' ? rewrite(second.value) : null
    if (second && value !== null) {
      params.call_args = [params.call_args[0], { ...second, value, raw: JSON.stringify(value) }, ...params.call_args.slice(2)]
      changed = true
    }
  }
  return changed ? { ...action, params } as ScriptActionDefinition : action
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
 * Rename a transaction group to `to` (for a sub-transaction, its full
 * "Parent||child" name). The sub-transactions under it, inside blocks and
 * under one another too, follow: a "parent||…" name takes the new name, and a
 * start naming it (or a sub-transaction under it) as its parent names the new
 * one. A tagged run's steps are re-tagged.
 */
export function renameTransaction(script: ScriptDefinition, group: OutlineGroup, to: string): ScriptDefinition {
  const next = to.trim()
  const from = group.name
  if (!next || next === from) return script
  const { phase, rows, lo, hi } = groupList(group)
  const list = scriptActionsForPhase(script, phase)
  if (!groupHasMarkers(group)) {
    const indices = new Set(rows.filter(row => actionTransactionName(row.action) === from).map(row => row.actionIndex))
    return withPhaseList(script, phase, list.map((action, index) => indices.has(index) ? setActionTransactionName(action, next) : action))
  }
  const sub = group.kind === 'subtransaction'
  const family = transactionFamily(list.slice(lo, hi + 1), from)
  const prefix = `${from.toLowerCase()}||`
  const rename = (action: ScriptActionDefinition) => {
    const marker = actionTransactionMarker(action)
    if (!marker) return action
    if (Boolean(marker.sub) === sub && marker.name === from) return renameTransactionMarker(action, from, next)
    if (!marker.sub) return action
    const named = marker.name.toLowerCase().startsWith(prefix) ? renameTransactionMarker(action, marker.name, `${next}||${marker.name.slice(prefix.length)}`) : action
    return marker.role === 'start' && startTiedTo(action, family) ? renameParentArgument(named, from, next) : named
  }
  return withPhaseList(script, phase, list.map((action, index) => {
    if (index < lo || index > hi) return action
    return rename(mapBlockLists(action, [], items => items.map(rename)))
  }))
}

export interface TransactionRemoval extends TransactionEdit {
  /** Sub-transactions removed with their parent. */
  subTransactions: number
  /** Why nothing was removed (a block would be left with no step), or ''. */
  problem: string
}

/**
 * Remove a transaction and keep its steps: a marker pair's markers go, and
 * those of the sub-transactions under it (inside blocks, and under one another,
 * too: the engine refuses a sub-transaction whose parent is not open); an end
 * goes with the start it closes in its list, so another transaction's
 * sub-transaction of the same name stays. A tagged run's steps lose the tag.
 */
export function removeTransaction(script: ScriptDefinition, group: OutlineGroup): TransactionRemoval {
  const unchanged: TransactionRemoval = { script, remap: location => location, focus: null, subTransactions: 0, problem: '' }
  const { phase, rows, lo, hi } = groupList(group)
  const list = scriptActionsForPhase(script, phase)
  if (!groupHasMarkers(group)) {
    const indices = new Set(rows.filter(row => actionTransactionName(row.action) === group.name).map(row => row.actionIndex))
    return {
      ...unchanged,
      script: withPhaseList(script, phase, list.map((action, index) => indices.has(index) ? setActionTransactionName(action, '') : action)),
      focus: indices.size ? { phase, actionIndex: Math.min(...indices) } : null,
    }
  }
  const sub = group.kind === 'subtransaction'
  const family = transactionFamily(list.slice(lo, hi + 1), group.name)
  let subTransactions = 0
  // The markers that go from items[from..to]: the transaction's own, and each
  // tied sub-transaction start with the end that closes it in the same list.
  const goingIn = (items: ScriptActionDefinition[], from: number, to: number) => {
    const removed = new Set<number>()
    const open: string[] = []
    for (let index = from; index <= to; index += 1) {
      const marker = items[index] ? actionTransactionMarker(items[index]) : null
      if (!marker) continue
      if (Boolean(marker.sub) === sub && marker.name === group.name) { removed.add(index); continue }
      if (!marker.sub) continue
      const name = marker.name.toLowerCase()
      if (marker.role === 'start') {
        if (startTiedTo(items[index], family)) { removed.add(index); open.push(name); subTransactions += 1 }
        continue
      }
      const at = open.lastIndexOf(name)
      if (at >= 0) { open.splice(at, 1); removed.add(index) }
    }
    return removed
  }
  // The indices removed from each list, keyed by the list ("phase|root|block path|list").
  const removedBy = new Map<string, Set<number>>()
  let emptied = false
  const listKey = (rootIndex: number | null, parentPath: string[], firstSegment: string | null) =>
    `${rootIndex ?? ''}|${parentPath.join('/')}|${firstSegment ? siblingListOf([firstSegment])?.listId ?? '' : ''}`
  const top = goingIn(list, lo, hi)
  const kept = list.map((action, index) => {
    if (index < lo || index > hi || top.has(index)) return action
    return mapBlockLists(action, [], (items, parentPath, segmentAt) => {
      const removed = goingIn(items, 0, items.length - 1)
      if (!removed.size) return items
      if (removed.size === items.length) emptied = true
      removedBy.set(listKey(index, parentPath, segmentAt(0)), removed)
      return items.filter((_, at) => !removed.has(at))
    })
  })
  if (emptied) return { ...unchanged, problem: 'Removing it would leave a block with no steps. Remove the markers inside that block first.' }
  const shiftIn = (removed: Set<number> | undefined, index: number) => {
    if (!removed?.size) return index
    if (removed.has(index)) return null
    return index - [...removed].filter(at => at < index).length
  }
  const remap: LocationRemap = location => {
    if (location.phase !== phase) return location
    const actionIndex = shiftIn(top, location.actionIndex)
    if (actionIndex === null) return null
    const path = location.stepPath || []
    const nextPath: string[] = []
    for (let depth = 0; depth < path.length; depth += 1) {
      const sibling = siblingListOf(path.slice(0, depth + 1))
      if (!sibling) return location
      const moved = shiftIn(removedBy.get(listKey(location.actionIndex, sibling.parentPath, path[depth])), sibling.index)
      if (moved === null) return null
      nextPath.push(sibling.segmentAt(moved))
    }
    if (actionIndex === location.actionIndex && nextPath.every((segment, depth) => segment === path[depth])) return location
    return { ...location, actionIndex, stepPath: path.length ? nextPath : location.stepPath }
  }
  const firstKept = (() => { for (let index = lo; index <= hi; index += 1) if (!top.has(index)) return shiftIn(top, index); return null })()
  return {
    script: withPhaseList(script, phase, kept.filter((_, index) => !top.has(index))),
    remap,
    focus: firstKept === null ? null : { phase, actionIndex: firstKept },
    subTransactions,
    problem: '',
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
export function groupSpan(group: OutlineGroup): Pick<WrapTarget, 'phase' | 'rootIndex' | 'memberPath' | 'lo' | 'hi'> {
  const { phase, lo, hi } = groupList(group)
  return { phase, rootIndex: null, memberPath: [], lo, hi }
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
