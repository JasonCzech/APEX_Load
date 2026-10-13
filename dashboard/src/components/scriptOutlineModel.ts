// The Author outline as a tree: phases, transactions, sub-transactions and
// steps, with the names a reader recognises. A sub-transaction around one step
// folds into that step's row (VuGen draws it as three rows: a start marker, the
// request under a generated name, and an end marker). Pure, so the outline and
// the debug rail draw the same tree and tests can check it without a DOM.
import type { ScriptActionDefinition } from '../types/scripts'
import { humanActionType } from './authorExperience'
import {
  actionTransactionMarker,
  actionTransactionName,
  buildDebugRailRenderRows,
  debugContainerKind,
  debugSequenceKey,
  type DebugRailRenderRow,
  type ScriptActionPhase,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'

/** A script with more steps than this opens with its transactions collapsed. */
export const OUTLINE_COLLAPSE_STEP_THRESHOLD = 200

const PHASES: ScriptActionPhase[] = ['init', 'action', 'teardown']
const PHASE_LABELS: Record<ScriptActionPhase, string> = { init: 'Init', action: 'Actions', teardown: 'Teardown' }

/** A sub-transaction that measures exactly one step, folded into it. */
export interface OutlineMeasure {
  /** The marker's full name, e.g. "Login||ACTION$execute_getUser". */
  name: string
  start: ScriptPhaseActionRow
  end: ScriptPhaseActionRow
}

export interface OutlineStep {
  kind: 'step'
  key: string
  row: ScriptPhaseActionRow
  label: string
  /** A shorter name to show when the label does not fit (an Aura step's method). */
  short?: string
  title: string
  badge: string
  measuredAs?: OutlineMeasure
  occurrence?: number
}

export interface OutlineGroup {
  kind: 'transaction' | 'subtransaction'
  key: string
  /** The transaction's full name (a sub-transaction's includes its parent). */
  name: string
  label: string
  start?: ScriptPhaseActionRow
  end?: ScriptPhaseActionRow
  children: OutlineNode[]
  /** Steps inside at any depth, not counting markers. */
  stepCount: number
  occurrence?: number
}

/** A marker drawn as its own row: "Show markers" is on, or it has no partner. */
export interface OutlineMarker {
  kind: 'marker'
  key: string
  row: ScriptPhaseActionRow
  role: 'start' | 'end'
  sub: boolean
  name: string
  label: string
}

export type OutlineNode = OutlineStep | OutlineGroup | OutlineMarker

export interface OutlinePhase {
  phase: ScriptActionPhase
  key: string
  label: string
  children: OutlineNode[]
  stepCount: number
}

export interface OutlineModel {
  phases: OutlinePhase[]
  /** Steps in the script, not counting markers. */
  stepCount: number
  /** For every action ("phase:index"), the key of the row that shows it. */
  rowKeyByAction: Map<string, string>
  /** For every row key, the keys of the groups (and phase) above it, outermost first. */
  ancestorsByKey: Map<string, string[]>
  /** Each folded step's sub-transaction, by the step's action key. */
  measureByAction: Map<string, OutlineMeasure>
  /** Every step, by its row key. */
  stepsByKey: Map<string, OutlineStep>
  /** Every transaction and sub-transaction, by its key. */
  groupsByKey: Map<string, OutlineGroup>
}

// --- Names -------------------------------------------------------------------

/** The sub-transaction's own name and its parent's, from "Parent||child". */
export function splitMeasureName(name: string): { parent: string; child: string } {
  const at = name.lastIndexOf('||')
  return at < 0 ? { parent: '', child: name } : { parent: name.slice(0, at), child: name.slice(at + 2) }
}

/** A sub-transaction's own name ("child" of "Parent||child"), or the whole name when that is empty. */
function ownName(name: string): string {
  return splitMeasureName(name).child || name
}

const SEPARATORS = '_-.|:/ '

/** The class part of an Aura descriptor head: "ui.force.x.RecordGvpController" → "RecordGvpController". */
function auraController(head: string): string {
  let name = head.replace(/\/+$/, '').replace(/^aura_/, '')
  name = name.slice(name.lastIndexOf('.') + 1)
  name = name.slice(name.lastIndexOf('/') + 1)
  // Imports name a framework action "aura_<Controller>_<Controller>": say it once.
  const half = (name.length - 1) / 2
  if (Number.isInteger(half) && name[half] === '_' && name.slice(0, half) === name.slice(half + 1)) name = name.slice(0, half)
  return name
}

const apexCallCache = new WeakMap<ScriptActionDefinition, Array<{ classname: string; method: string }>>()

/** The Apex calls an Aura request makes (ApexActionController execute), from its message. */
export function auraApexCalls(action: ScriptActionDefinition): Array<{ classname: string; method: string }> {
  const cached = apexCallCache.get(action)
  if (cached) return cached
  let calls: Array<{ classname: string; method: string }> = []
  const body = typeof action.body === 'string' ? action.body : ''
  if (body.includes('classname')) {
    let text = body
    try { text = decodeURIComponent(body.replace(/\+/g, ' ')) } catch { /* a body that is not URL-encoded is read as is */ }
    const classnames = [...text.matchAll(/"classname"\s*:\s*"([^"]+)"/g)].map(match => match[1])
    const methods = [...text.matchAll(/"method"\s*:\s*"([^"]+)"/g)].map(match => match[1])
    if (classnames.length === methods.length) calls = classnames.map((classname, index) => ({ classname, method: methods[index] }))
  }
  apexCallCache.set(action, calls)
  return calls
}

/** An Aura step name's class and method, when it names an Aura action. */
export function auraNameParts(name: string, action?: ScriptActionDefinition): { controller: string; method: string } | null {
  const at = name.indexOf('ACTION$')
  if (at < 0) return null
  let method = name.slice(at + 'ACTION$'.length)
  let controller = at > 0 ? auraController(name.slice(0, at)) : ''
  if (!controller && method.startsWith('execute_') && method.length > 'execute_'.length) {
    method = method.slice('execute_'.length)
    controller = (action && auraApexCalls(action).find(call => call.method === method)?.classname) || ''
  }
  return method ? { controller, method } : null
}

/**
 * A step name as an Apex or Aura developer reads it. Salesforce names an Apex
 * method Class.method (as in `@salesforce/apex/ContactController.getContactList`),
 * so "…FlexipageControllerV2/ACTION$getPage" reads "FlexipageControllerV2.getPage"
 * and an ApexActionController call "ACTION$execute_getUser" reads
 * "UserController.getUser" when the request names its class. Other names are
 * returned unchanged.
 */
export function readableStepName(name: string, action?: ScriptActionDefinition): string {
  const parts = auraNameParts(name, action)
  if (!parts) return name
  return parts.controller ? `${parts.controller}.${parts.method}` : parts.method
}

/** The name without a leading prefix it shares with its transaction ("T02_Login_aura_2" in T02_Login → "aura_2"). */
export function dropNamePrefix(name: string, prefix: string): string {
  if (!prefix || name.length <= prefix.length + 1 || !name.startsWith(prefix)) return name
  const rest = name.slice(prefix.length)
  if (!SEPARATORS.includes(rest[0])) return name
  const trimmed = rest.replace(/^[_\-.|:/ ]+/, '')
  return trimmed || name
}

/** The longest prefix, ending at a separator, that every name shares; '' when one name would be left empty. */
export function commonNamePrefix(names: string[]): string {
  if (names.length < 2) return ''
  let prefix = names[0]
  for (const name of names.slice(1)) {
    let length = 0
    while (length < prefix.length && length < name.length && prefix[length] === name[length]) length += 1
    prefix = prefix.slice(0, length)
  }
  // Cut back to the last separator so no word is split.
  let cut = prefix.length
  while (cut > 0 && !SEPARATORS.includes(prefix[cut - 1])) cut -= 1
  prefix = prefix.slice(0, cut)
  if (!prefix || names.some(name => name.length <= prefix.length)) return ''
  return prefix.replace(/[_\-.|:/ ]+$/, '')
}

/** The places a name may be cut: after a separator, at a camelCase hump, or between letters and digits. */
function nameBreaks(name: string): number[] {
  const breaks: number[] = []
  for (let index = 1; index < name.length; index += 1) {
    const before = name[index - 1]
    const here = name[index]
    if (SEPARATORS.includes(before) && !SEPARATORS.includes(here)) breaks.push(index)
    else if (/[a-z]/.test(before) && /[A-Z]/.test(here)) breaks.push(index)
    else if (/[A-Za-z]/.test(before) && /\d/.test(here)) breaks.push(index)
  }
  return breaks
}

/**
 * The name as it fits in `width`: whole; else `shorter` whole (an Aura step's
 * method without its class); else cut at the end on a word boundary with an
 * ellipsis, never inside an identifier. `measure` returns a string's width in
 * the same unit as `width`.
 */
export function fitOutlineName(name: string, width: number, measure: (text: string) => number, shorter?: string): string {
  if (!(width > 0) || measure(name) <= width) return name
  if (shorter && measure(shorter) <= width) return shorter
  const base = shorter || name
  const breaks = nameBreaks(base)
  for (let index = breaks.length - 1; index >= 0; index -= 1) {
    const head = base.slice(0, breaks[index]).replace(/[_\-.|:/ ]+$/, '')
    if (head && measure(`${head}…`) <= width) return `${head}…`
  }
  return '…'
}

function stepBadge(action: ScriptActionDefinition): string {
  const kind = debugContainerKind(action)
  // Imported LoadRunner calls are all "source_*" types; the prefix says nothing here.
  return humanActionType(kind || action.method || action.type).replace(/^Source /, '')
}

// --- Building ------------------------------------------------------------------

interface OpenGroup {
  group: OutlineGroup
  /** A marker pair (closed by its end marker), or a run of tagged steps. */
  byMarkers: boolean
}

function buildPhase(rows: ScriptPhaseActionRow[], phase: ScriptActionPhase, showMarkers: boolean, occurrences: Map<string, number>): OutlinePhase {
  const root: OutlinePhase = { phase, key: `phase:${phase}`, label: PHASE_LABELS[phase], children: [], stepCount: 0 }
  const stack: OpenGroup[] = []
  const container = () => (stack.length ? stack[stack.length - 1].group.children : root.children)
  const nextKey = (kind: string, name: string) => {
    const id = `${phase}:${name}`
    const count = (occurrences.get(`${kind}:${id}`) || 0) + 1
    occurrences.set(`${kind}:${id}`, count)
    return `${kind}:${id}#${count}`
  }
  const closeTaggedRuns = () => {
    while (stack.length && !stack[stack.length - 1].byMarkers) stack.pop()
  }
  for (const row of rows) {
    const marker = actionTransactionMarker(row.action)
    const actionKey = debugSequenceKey(row.phase, row.actionIndex)
    if (marker) {
      closeTaggedRuns()
      const asRow = (): OutlineMarker => ({
        kind: 'marker', key: actionKey, row, role: marker.role, sub: Boolean(marker.sub), name: marker.name,
        label: marker.role === 'end' ? `End ${ownName(marker.name)}` : ownName(marker.name),
      })
      if (marker.role === 'start' && !(marker.sub && showMarkers)) {
        const group: OutlineGroup = { kind: marker.sub ? 'subtransaction' : 'transaction', key: nextKey(marker.sub ? 'sub' : 'transaction', marker.name), name: marker.name, label: marker.name, start: row, children: [], stepCount: 0 }
        container().push(group)
        stack.push({ group, byMarkers: true })
        continue
      }
      if (marker.role === 'end' && !(marker.sub && showMarkers)) {
        const kind = marker.sub ? 'subtransaction' : 'transaction'
        let at = -1
        for (let index = stack.length - 1; index >= 0; index -= 1) {
          if (stack[index].group.kind === kind && stack[index].group.name === marker.name) { at = index; break }
        }
        if (at >= 0) {
          stack[at].group.end = row
          // An end marker shows as a row only when markers are shown.
          if (showMarkers) stack[at].group.children.push(asRow())
          stack.length = at
          continue
        }
      }
      container().push(asRow())
      continue
    }
    // A run of steps tagged with one transaction (no markers) is a group too.
    const tag = actionTransactionName(row.action)
    const top = stack[stack.length - 1]
    if (top && !top.byMarkers && top.group.name !== tag) stack.pop()
    const inMarkerTransaction = stack.some(open => open.byMarkers && open.group.kind === 'transaction')
    if (tag && !inMarkerTransaction && !(stack.length && !stack[stack.length - 1].byMarkers)) {
      const group: OutlineGroup = { kind: 'transaction', key: nextKey('transaction', tag), name: tag, label: tag, children: [], stepCount: 0 }
      container().push(group)
      stack.push({ group, byMarkers: false })
    }
    container().push({ kind: 'step', key: actionKey, row, label: '', title: '', badge: stepBadge(row.action) })
  }
  return root
}

/** Fold sub-transactions around one step, count steps, and name everything. */
function finishNodes(nodes: OutlineNode[], transactionName: string, measures: Map<string, OutlineMeasure>): OutlineNode[] {
  const out: OutlineNode[] = []
  for (const node of nodes) {
    if (node.kind !== 'subtransaction' && node.kind !== 'transaction') {
      out.push(node)
      continue
    }
    const inner = node.kind === 'transaction' ? node.name : transactionName
    node.children = finishNodes(node.children, inner, measures)
    node.stepCount = node.children.reduce((count, child) => count + (child.kind === 'step' ? 1 : child.kind === 'marker' ? 0 : child.stepCount), 0)
    const only = node.children.length === 1 ? node.children[0] : undefined
    if (node.kind === 'subtransaction' && node.start && node.end && only?.kind === 'step' && !only.measuredAs) {
      only.measuredAs = { name: node.name, start: node.start, end: node.end }
      measures.set(only.key, only.measuredAs)
      out.push(only)
      continue
    }
    out.push(node)
  }
  // Names: the readable form, without the enclosing transaction's prefix.
  for (const node of out) {
    if (node.kind === 'step') {
      const raw = node.row.action.name || `${node.row.phaseLabel} ${node.row.actionIndex + 1}`
      const measured = node.measuredAs ? ownName(node.measuredAs.name) : ''
      const parts = auraNameParts(measured || raw, node.row.action)
      node.label = dropNamePrefix(readableStepName(measured || raw, node.row.action), transactionName)
      if (parts?.controller) node.short = parts.method
      node.title = measured ? `${measured} — ${raw}` : raw
    } else if (node.kind === 'subtransaction') {
      node.label = dropNamePrefix(readableStepName(ownName(node.name), node.children.find((child): child is OutlineStep => child.kind === 'step')?.row.action), transactionName)
    } else if (node.kind === 'transaction') {
      node.label = dropNamePrefix(node.name, transactionName)
    }
  }
  // Nested transactions that share a prefix are told apart by the rest (the
  // script's top-level ones are compared across all phases, in buildOutlineModel).
  if (transactionName) {
    const transactions = out.filter((node): node is OutlineGroup => node.kind === 'transaction')
    const shared = commonNamePrefix(transactions.map(node => node.label))
    if (shared) for (const node of transactions) node.label = dropNamePrefix(node.label, shared)
  }
  // A name used more than once among its siblings gets an occurrence number.
  const counts = new Map<string, number>()
  for (const node of out) if (node.kind === 'step' || node.kind === 'subtransaction') counts.set(node.label, (counts.get(node.label) || 0) + 1)
  const seen = new Map<string, number>()
  for (const node of out) {
    if (node.kind !== 'step' && node.kind !== 'subtransaction') continue
    if ((counts.get(node.label) || 0) < 2) { delete node.occurrence; continue }
    const occurrence = (seen.get(node.label) || 0) + 1
    seen.set(node.label, occurrence)
    node.occurrence = occurrence
  }
  return out
}

function indexNodes(nodes: OutlineNode[], ancestors: string[], model: OutlineModel) {
  for (const node of nodes) {
    model.ancestorsByKey.set(node.key, ancestors)
    if (node.kind === 'step') {
      model.stepCount += 1
      model.rowKeyByAction.set(node.key, node.key)
      model.stepsByKey.set(node.key, node)
      if (node.measuredAs) {
        model.rowKeyByAction.set(debugSequenceKey(node.measuredAs.start.phase, node.measuredAs.start.actionIndex), node.key)
        model.rowKeyByAction.set(debugSequenceKey(node.measuredAs.end.phase, node.measuredAs.end.actionIndex), node.key)
      }
    } else if (node.kind === 'marker') {
      model.rowKeyByAction.set(node.key, node.key)
    } else {
      model.groupsByKey.set(node.key, node)
      if (node.start) model.rowKeyByAction.set(debugSequenceKey(node.start.phase, node.start.actionIndex), node.key)
      if (node.end) model.rowKeyByAction.set(debugSequenceKey(node.end.phase, node.end.actionIndex), node.key)
      indexNodes(node.children, [...ancestors, node.key], model)
    }
  }
}

/** The outline of a script's rows (from buildScriptPhaseActionRows). */
export function buildOutlineModel(rows: ScriptPhaseActionRow[], options: { showMarkers?: boolean } = {}): OutlineModel {
  const model: OutlineModel = { phases: [], stepCount: 0, rowKeyByAction: new Map(), ancestorsByKey: new Map(), measureByAction: new Map(), stepsByKey: new Map(), groupsByKey: new Map() }
  const occurrences = new Map<string, number>()
  for (const phase of PHASES) {
    const built = buildPhase(rows.filter(row => row.phase === phase), phase, Boolean(options.showMarkers), occurrences)
    built.children = finishNodes(built.children, '', model.measureByAction)
    model.phases.push(built)
  }
  // A script's transactions often share a long prefix ("iPadTest_LrgeBANs_…");
  // each is told apart by the rest, in every phase.
  const topTransactions = model.phases.flatMap(phase => phase.children.filter((node): node is OutlineGroup => node.kind === 'transaction'))
  const shared = commonNamePrefix(topTransactions.map(node => node.label))
  if (shared) for (const node of topTransactions) node.label = dropNamePrefix(node.label, shared)
  for (const phase of model.phases) {
    const before = model.stepCount
    indexNodes(phase.children, [phase.key], model)
    phase.stepCount = model.stepCount - before
  }
  return model
}

// --- Visible rows ------------------------------------------------------------------

export interface OutlineRow {
  key: string
  /** aria-level: a phase is 1. */
  level: number
  kind: 'phase' | 'transaction' | 'subtransaction' | 'step' | 'marker' | 'nested'
  label: string
  /** A shorter name to show when the label does not fit. */
  short?: string
  occurrence?: number
  title: string
  /** The method or type ("POST"), or a group's step count. */
  badge: string
  expandable: boolean
  expanded: boolean
  parentKey: string | null
  /** What selecting the row selects; a group selects its start marker. */
  select?: DebugRailRenderRow
  /** The rows inside a group (for its problem and breakpoint flags). */
  rows: ScriptPhaseActionRow[]
  measuredAs?: OutlineMeasure
  posInSet: number
  setSize: number
}

export interface OutlineViewState {
  isExpanded: (key: string) => boolean
  /** Containers (sequences, control flow) the reader opened, as buildDebugRailRenderRows keys them. */
  expandedContainers: Set<string>
  query: string
}

function rowItem(row: ScriptPhaseActionRow): DebugRailRenderRow {
  return { kind: 'action', key: debugSequenceKey(row.phase, row.actionIndex), depth: 0, row }
}

function nodeRows(node: OutlineNode): ScriptPhaseActionRow[] {
  if (node.kind === 'step') return node.measuredAs ? [node.measuredAs.start, node.row, node.measuredAs.end] : [node.row]
  if (node.kind === 'marker') return [node.row]
  return [...(node.start ? [node.start] : []), ...node.children.flatMap(nodeRows), ...(node.end ? [node.end] : [])]
}

function nodeMatches(node: OutlineNode | OutlinePhase, needle: string): boolean {
  const has = (value?: string) => Boolean(value && value.toLowerCase().includes(needle))
  if ('phase' in node) return has(node.label)
  if (node.kind === 'step') {
    const action = node.row.action
    return has(node.label) || has(node.title) || has(action.name) || has(action.type) || has(action.target) || has(node.measuredAs?.name)
  }
  if (node.kind === 'marker') return has(node.label) || has(node.name)
  return has(node.label) || has(node.name)
}

function nodeHasMatch(node: OutlineNode, needle: string): boolean {
  return nodeMatches(node, needle) || ((node.kind === 'transaction' || node.kind === 'subtransaction') && node.children.some(child => nodeHasMatch(child, needle)))
}

/**
 * The rows the outline draws, in order. A search keeps each match's groups
 * open and visible; a group whose own name matches shows as it is.
 */
export function visibleOutlineRows(model: OutlineModel, view: OutlineViewState): OutlineRow[] {
  const needle = view.query.trim().toLowerCase()
  const out: OutlineRow[] = []
  const walk = (nodes: OutlineNode[], level: number, parentKey: string, filtered: boolean) => {
    for (const node of nodes) {
      const matches = !filtered || nodeMatches(node, needle)
      if (filtered && !matches && !nodeHasMatch(node, needle)) continue
      if (node.kind === 'step') {
        const container = debugContainerKind(node.row.action)
        const expanded = Boolean(container) && view.expandedContainers.has(node.key)
        out.push({
          key: node.key, level, kind: 'step', label: node.label, short: node.short, occurrence: node.occurrence, title: node.title, badge: node.badge,
          expandable: Boolean(container), expanded, parentKey, select: rowItem(node.row), rows: nodeRows(node), measuredAs: node.measuredAs, posInSet: 0, setSize: 0,
        })
        if (expanded) {
          const nested = buildDebugRailRenderRows([node.row], view.expandedContainers).slice(1)
          const parents: string[] = [node.key]
          for (const item of nested) {
            parents.length = item.depth
            const nestedParent = parents[item.depth - 1] || node.key
            out.push({
              key: item.key, level: level + item.depth, kind: 'nested',
              label: item.kind === 'leaf' || item.kind === 'container' ? item.name : '',
              title: item.kind === 'leaf' || item.kind === 'container' ? item.name : '',
              badge: item.kind === 'leaf' ? humanActionType(item.method) : item.kind === 'container' ? humanActionType(item.containerType) : '',
              expandable: item.kind === 'container', expanded: item.kind === 'container' && item.expanded, parentKey: nestedParent,
              select: item, rows: [node.row], posInSet: 0, setSize: 0,
            })
            parents[item.depth] = item.key
          }
        }
        continue
      }
      if (node.kind === 'marker') {
        out.push({
          key: node.key, level, kind: 'marker', label: node.label, title: node.name,
          badge: node.role === 'start' ? (node.sub ? 'Sub-transaction' : 'Transaction') : '',
          expandable: false, expanded: false, parentKey, select: rowItem(node.row), rows: [node.row], posInSet: 0, setSize: 0,
        })
        continue
      }
      // A search opens every group on the way to a match.
      const forced = filtered && !matches
      const expanded = forced || view.isExpanded(node.key)
      out.push({
        key: node.key, level, kind: node.kind, label: node.label, occurrence: node.occurrence, title: node.name,
        badge: `${node.stepCount} step${node.stepCount === 1 ? '' : 's'}`,
        expandable: node.children.length > 0, expanded: expanded && node.children.length > 0, parentKey,
        select: node.start ? rowItem(node.start) : undefined, rows: nodeRows(node), posInSet: 0, setSize: 0,
      })
      if (expanded) walk(node.children, level + 1, node.key, forced)
    }
  }
  for (const phase of model.phases) {
    // An empty Init or Teardown takes no room; Actions always shows.
    if (!phase.children.length && phase.phase !== 'action') continue
    const phaseMatches = !needle || nodeMatches(phase, needle)
    const childMatch = needle && phase.children.some(child => nodeHasMatch(child, needle))
    if (needle && !phaseMatches && !childMatch) continue
    const expanded = (needle && !phaseMatches) || view.isExpanded(phase.key)
    out.push({
      key: phase.key, level: 1, kind: 'phase', label: phase.label, title: phase.label,
      badge: `${phase.stepCount} step${phase.stepCount === 1 ? '' : 's'}`,
      expandable: phase.children.length > 0, expanded: Boolean(expanded) && phase.children.length > 0, parentKey: null,
      rows: [], posInSet: 0, setSize: 0,
    })
    if (expanded) walk(phase.children, 2, phase.key, Boolean(needle) && !phaseMatches)
  }
  // Position among visible siblings, for assistive technology.
  const siblings = new Map<string | null, OutlineRow[]>()
  for (const row of out) {
    const list = siblings.get(row.parentKey) || []
    list.push(row)
    siblings.set(row.parentKey, list)
  }
  for (const list of siblings.values()) list.forEach((row, index) => { row.posInSet = index + 1; row.setSize = list.length })
  return out
}

/** The groups that start collapsed: all of them in a long script, none otherwise. */
export function outlineStartsCollapsed(model: OutlineModel): boolean {
  return model.stepCount > OUTLINE_COLLAPSE_STEP_THRESHOLD
}

// --- Editing markers ---------------------------------------------------------------

/** A transaction marker renamed from `from` to `to`, everywhere an import wrote the name. */
export function renameTransactionMarker(action: ScriptActionDefinition, from: string, to: string): ScriptActionDefinition {
  const params: Record<string, unknown> = { ...((action.params as Record<string, unknown> | undefined) || {}) }
  if (Array.isArray(params.args) && params.args[0] === from) params.args = [to, ...params.args.slice(1)]
  if (Array.isArray(params.call_args)) {
    const [first, ...rest] = params.call_args as unknown[]
    if (first && typeof first === 'object' && (first as Record<string, unknown>).value === from) {
      params.call_args = [{ ...(first as Record<string, unknown>), value: to, raw: JSON.stringify(to) }, ...rest]
    }
  }
  const name = action.name && action.name.includes(from) ? action.name.split(from).join(to) : action.name
  return { ...action, name, params } as ScriptActionDefinition
}
