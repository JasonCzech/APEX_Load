import type { ScriptActionDefinition as Action, ScriptDefinition } from '../types/scripts'
import { placeSteps, stepOutputs, stepsBefore, templateTokens, type InsertionPoint, type StepPosition } from './referenceScopeModel'

type Json = Record<string, unknown>

function isRecord(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export type LiveTableKind = 'native_queue' | 'vts_columns'

/**
 * Each live-table operation: whether it changes the table, and the table kind
 * it needs (pkg/dsl liveTableVariants; the model test checks both against the
 * catalog fixture).
 */
export const LIVE_TABLE_OPERATIONS: Record<string, { mutates: boolean; kind?: LiveTableKind }> = {
  read: { mutates: false },
  next: { mutates: false },
  read_column: { mutates: false, kind: 'vts_columns' },
  push: { mutates: true },
  update: { mutates: true },
  increment: { mutates: true },
  clear: { mutates: true },
  pop: { mutates: true },
  lease: { mutates: true, kind: 'native_queue' },
  renew: { mutates: true, kind: 'native_queue' },
  ack: { mutates: true, kind: 'native_queue' },
  release: { mutates: true, kind: 'native_queue' },
  create_column: { mutates: true, kind: 'vts_columns' },
  rotate: { mutates: true, kind: 'vts_columns' },
}

export const LIVE_TABLE_KIND_LABELS: Record<LiveTableKind, string> = { native_queue: 'work queue', vts_columns: 'shared columns' }

export interface DataAliasStep { position: StepPosition; name: string; operation: string }

/** One alias a script reads or changes, as the reference scanner finds it. */
export interface DataAliasUsage {
  alias: string
  /** Columns the script names, in first-use order. */
  columns: string[]
  /** Table-step operations, in script order. */
  operations: string[]
  /** Operations its native code declares (config.native_services). */
  nativeOperations: string[]
  /** Whether any of them changes rows. */
  mutates: boolean
  /** The table kinds they need; two means no one table runs them all. */
  kinds: LiveTableKind[]
  /** Steps read {{data.alias.column}}: a row the binding hands out each iteration. */
  readsEachIteration: boolean
  /** A data source of the script's own supplies this name. */
  suppliedByScript: boolean
  steps: DataAliasStep[]
}

const DATA_REFERENCE = /^data\.([\w-]+)\.([\w-]+)$/
const textCache = new WeakMap<object, string>()

/**
 * A step's own fields as text, without its child steps (they are visited as
 * steps) and without a native script's source, which is literal code.
 */
function ownText(action: Action): string {
  const cached = textCache.get(action)
  if (cached !== undefined) return cached
  const text = JSON.stringify(action, function (this: unknown, key: string, value: unknown) {
    if (this === action && key === 'control' && isRecord(value)) return { condition: value.condition, for_each: value.for_each }
    if (this === action.params && key === 'source' && action.type === 'native_javascript') return undefined
    return value
  })
  textCache.set(action, text)
  return text
}

/** A script-level setting as text; an object's text is kept until it is replaced. */
function settingText(value: unknown): string {
  if (typeof value !== 'object' || value === null) return JSON.stringify(value) ?? ''
  const cached = textCache.get(value)
  if (cached !== undefined) return cached
  const text = JSON.stringify(value)
  textCache.set(value, text)
  return text
}

const asStrings = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []

/**
 * Every alias a script reads or changes: its table steps, the columns they
 * and {{data.alias.column}} references name, and what native code declares.
 */
export function scriptDataUsage(script: ScriptDefinition): DataAliasUsage[] {
  const byAlias = new Map<string, DataAliasUsage>()
  const entry = (alias: string) => {
    let usage = byAlias.get(alias)
    if (!usage) {
      usage = { alias, columns: [], operations: [], nativeOperations: [], mutates: false, kinds: [], readsEachIteration: false, suppliedByScript: false, steps: [] }
      byAlias.set(alias, usage)
    }
    return usage
  }
  const addColumn = (usage: DataAliasUsage, column: string) => { if (column && !usage.columns.includes(column)) usage.columns.push(column) }
  const native = (script.config as unknown as Json | undefined)?.native_services
  for (const table of isRecord(native) && Array.isArray(native.live_tables) ? native.live_tables.filter(isRecord) : []) {
    if (typeof table.alias === 'string' && table.alias) entry(table.alias).nativeOperations = asStrings(table.operations)
  }
  const prefixes = new Map<string, DataAliasUsage>()
  // Script-level settings (config, parameters, data sources) can hold references too.
  const texts: string[] = Object.entries(script).filter(([key]) => !['init', 'actions', 'teardown'].includes(key)).map(([, value]) => settingText(value))
  for (const { action, position } of placeSteps(script)) {
    texts.push(ownText(action))
    if (action.type !== 'live_table' || !isRecord(action.params)) continue
    const params = action.params
    if (typeof params.alias !== 'string' || !params.alias) continue
    const usage = entry(params.alias)
    const operation = typeof params.operation === 'string' ? params.operation : ''
    if (operation && !usage.operations.includes(operation)) usage.operations.push(operation)
    usage.steps.push({ position, name: action.name || operation || 'Live table', operation })
    asStrings(params.columns).forEach(column => addColumn(usage, column))
    for (const key of ['values', 'expected']) if (isRecord(params[key])) Object.keys(params[key] as Json).forEach(column => addColumn(usage, column))
    if (typeof params.result_prefix === 'string' && params.result_prefix) prefixes.set(params.result_prefix, usage)
  }
  for (const text of texts) {
    for (const token of templateTokens(text)) {
      if (token.root === 'data') {
        const match = DATA_REFERENCE.exec(token.expression)
        if (!match) continue
        const usage = entry(match[1])
        usage.readsEachIteration = true
        addColumn(usage, match[2])
      } else if (token.root === 'var') {
        // A column read from a table step's result: {{var.<prefix>.values.<column>}}.
        for (const [prefix, usage] of prefixes) {
          const lead = `var.${prefix}.values.`
          if (token.expression.startsWith(lead) && /^[\w-]+$/.test(token.expression.slice(lead.length))) addColumn(usage, token.expression.slice(lead.length))
        }
      }
    }
  }
  const sources = new Set((script.data_sources || []).map(source => source.name))
  for (const usage of byAlias.values()) {
    usage.suppliedByScript = sources.has(usage.alias)
    const operations = [...usage.operations, ...usage.nativeOperations]
    usage.mutates = operations.some(operation => LIVE_TABLE_OPERATIONS[operation]?.mutates)
    usage.kinds = [...new Set(operations.map(operation => LIVE_TABLE_OPERATIONS[operation]?.kind).filter((kind): kind is LiveTableKind => Boolean(kind)))]
  }
  return [...byAlias.values()]
}

/** Whether an alias needs a live table: table steps or native code use it. */
export function usesTableSteps(usage: Pick<DataAliasUsage, 'operations' | 'nativeOperations'>): boolean {
  return usage.operations.length + usage.nativeOperations.length > 0
}

/**
 * Names in the variable namespace: what steps save, live-table result
 * prefixes, and data rows (an alias's columns arrive as <alias>.<column>).
 */
export function takenVariableNames(script: ScriptDefinition): Set<string> {
  const taken = new Set<string>()
  for (const { action } of placeSteps(script)) {
    for (const output of stepOutputs(action)) taken.add(output.expression.slice('var.'.length).split('.')[0])
    if (isRecord(action.params) && typeof action.params.result_prefix === 'string' && action.params.result_prefix) taken.add(action.params.result_prefix)
  }
  for (const source of script.data_sources || []) if (source.name) taken.add(source.name)
  for (const usage of scriptDataUsage(script)) taken.add(usage.alias)
  return taken
}

/** Every step's name, lower-cased, as duplicate-name checks compare them. */
export function scriptActionNames(script: ScriptDefinition): Set<string> {
  return new Set(placeSteps(script).map(({ action }) => (action.name || '').toLowerCase()).filter(Boolean))
}

/** The variables (without `var.`) that steps running before `point` set. */
export function earlierVariables(script: ScriptDefinition, point: InsertionPoint): Set<string> {
  return new Set(stepsBefore(script, point).flatMap(({ action }) => stepOutputs(action).map(output => output.expression.slice('var.'.length))))
}

/** `base`, or `base_2`, `base_3`…: the first not taken. */
export function uniqueVariableName(base: string, taken: ReadonlySet<string>): string {
  let name = base
  for (let suffix = 2; taken.has(name); suffix++) name = `${base}_${suffix}`
  return name
}

const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function walk(actions: Action[], visit: (action: Action) => void) {
  for (const action of actions) {
    visit(action)
    const control = action.control as unknown as Json | undefined
    if (!control) continue
    for (const key of ['steps', 'else', 'default']) if (Array.isArray(control[key])) walk(control[key] as Action[], visit)
    for (const key of ['cases', 'branches']) if (Array.isArray(control[key])) for (const item of control[key] as Json[]) if (Array.isArray(item?.steps)) walk(item.steps as Action[], visit)
  }
}

/**
 * Copies of `actions` (a saved block being inserted) whose live-table result
 * prefixes are not already taken, with the block's own references and lease
 * references following each rename. Marks every name it keeps or makes as taken.
 */
export function withUniqueResultPrefixes(actions: Action[], taken: Set<string>): { actions: Action[]; renamed: Record<string, string> } {
  const names = new Map<string, string>()
  walk(actions, action => {
    const prefix = action.type === 'live_table' && isRecord(action.params) ? action.params.result_prefix : undefined
    if (typeof prefix !== 'string' || !prefix || names.has(prefix)) return
    const name = uniqueVariableName(prefix, taken)
    taken.add(name)
    names.set(prefix, name)
  })
  const changes = [...names].filter(([from, to]) => from !== to)
  if (!changes.length) return { actions: structuredClone(actions), renamed: {} }
  const renamed = new Map(changes)
  const patterns = changes.map(([from, to]) => [new RegExp(`(\\{\\{\\s*var\\.)${escapeRegExp(from)}(?=[.\\s}])`, 'g'), to] as const)
  const rewrite = (value: unknown, key?: string): unknown => {
    if (typeof value === 'string') {
      if ((key === 'result_prefix' || key === 'lease_ref') && renamed.has(value)) return renamed.get(value)
      return patterns.reduce((text, [pattern, to]) => text.replace(pattern, `$1${to}`), value)
    }
    if (Array.isArray(value)) return value.map(item => rewrite(item))
    if (isRecord(value)) return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, rewrite(item, name)]))
    return value
  }
  return { actions: rewrite(actions) as Action[], renamed: Object.fromEntries(changes) }
}
