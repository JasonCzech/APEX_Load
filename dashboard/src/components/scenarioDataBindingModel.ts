import type { LiveTableOptions, SharedDataPool } from '../types/dataPools'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'
import { LIVE_TABLE_KIND_LABELS, scriptDataUsage, usesTableSteps, type DataAliasUsage, type LiveTableKind } from './scriptDataUsageModel'

export const DEFAULT_LIVE_TABLE_OPTIONS: LiveTableOptions = {
  profile: 'native_queue', seed_policy: 'current_at_start', durability: 'memory', end_mode: 'discard', empty_policy: 'error',
}

/** Each alias the scripts read or change, with the columns they name. */
export function scenarioDataRequirements(scripts: StoredScript[]): Map<string, Set<string>> {
  const requirements = new Map<string, Set<string>>()
  for (const script of scripts) {
    if (!script.script) continue
    for (const usage of scriptDataUsage(script.script)) {
      const columns = requirements.get(usage.alias) || new Set<string>()
      usage.columns.forEach(column => columns.add(column))
      requirements.set(usage.alias, columns)
    }
  }
  return requirements
}
/** An alias the scenario's scripts need from a binding, across those scripts. */
export interface ScenarioDataNeed extends Omit<DataAliasUsage, 'steps' | 'suppliedByScript'> {
  /** The scripts that use it, by name. */
  scripts: string[]
}

/**
 * Each alias a binding must supply: every alias a script reads or changes,
 * except a {{data.…}} read that the script's own data source answers.
 */
export function scenarioDataNeeds(scripts: StoredScript[]): ScenarioDataNeed[] {
  const needs = new Map<string, ScenarioDataNeed>()
  for (const script of scripts) {
    if (!script.script) continue
    for (const usage of scriptDataUsage(script.script)) {
      if (usage.suppliedByScript && !usesTableSteps(usage)) continue
      const need = needs.get(usage.alias)
      if (!need) {
        needs.set(usage.alias, { alias: usage.alias, columns: [...usage.columns], operations: [...usage.operations], nativeOperations: [...usage.nativeOperations], mutates: usage.mutates, kinds: [...usage.kinds], readsEachIteration: usage.readsEachIteration, scripts: [script.name] })
        continue
      }
      const add = (list: string[], items: string[]) => items.forEach(item => { if (!list.includes(item)) list.push(item) })
      add(need.columns, usage.columns); add(need.operations, usage.operations); add(need.nativeOperations, usage.nativeOperations); add(need.scripts, [script.name])
      need.kinds = [...new Set([...need.kinds, ...usage.kinds])]
      need.mutates ||= usage.mutates
      need.readsEachIteration ||= usage.readsEachIteration
    }
  }
  return [...needs.values()]
}

/** The three tables a binding can be. */
export type DataTableChoice = 'snapshot' | 'run' | 'project'

export const DATA_TABLE_CHOICES: { value: DataTableChoice; label: string; lifetime: string }[] = [
  { value: 'snapshot', label: 'Read-only snapshot', lifetime: 'The pool’s rows are copied when the run starts; nothing the run does changes them.' },
  { value: 'run', label: 'Live table for this run', lifetime: 'One table every group in this run shares; it is discarded or saved when the run ends.' },
  { value: 'project', label: 'Project live table', lifetime: 'A table the project’s runs share; it keeps its rows after this run ends.' },
]

export function dataTableChoice(binding: ScenarioDataPoolBinding): DataTableChoice {
  return binding.access_mode !== 'live' ? 'snapshot' : binding.scope === 'project' ? 'project' : 'run'
}

/** A run table's starting kind: shared columns only for column steps. */
function profileFor(need?: Pick<ScenarioDataNeed, 'kinds'>): LiveTableKind {
  return need?.kinds.includes('vts_columns') && !need.kinds.includes('native_queue') ? 'vts_columns' : 'native_queue'
}

/**
 * The change that makes a binding the chosen table. A run table starts from
 * the pool (with the pool's columns) unless the scripts only add rows and no
 * pool is chosen: then it starts empty with the columns they name. A live
 * table hands out the next row each iteration when the scripts read
 * {{data.alias.column}}; table steps work beside that.
 */
export function tableChoicePatch(binding: ScenarioDataPoolBinding, choice: DataTableChoice, poolColumns: string[], need?: ScenarioDataNeed): Partial<ScenarioDataPoolBinding> {
  if (choice === 'snapshot') return { access_mode: 'read_only', scope: undefined, project_table_id: undefined, selection_strategy: 'sequential', live_options: undefined }
  const selection_strategy: ScenarioDataPoolBinding['selection_strategy'] = need?.readsEachIteration ? 'next' : 'manual'
  if (choice === 'project') return { access_mode: 'live', scope: 'project', project_table_id: '', pool_id: '', selection_strategy, live_options: { empty_policy: 'error', operation_timeout_ms: 120000 } }
  const producesOnly = Boolean(need && usesTableSteps(need) && [...need.operations, ...need.nativeOperations].every(operation => operation === 'push' || operation === 'create_column'))
  const empty = !binding.pool_id && producesOnly
  return {
    access_mode: 'live', scope: 'run', project_table_id: undefined, selection_strategy,
    live_options: { ...DEFAULT_LIVE_TABLE_OPTIONS, profile: profileFor(need), seed_policy: empty ? 'empty' : 'current_at_start', columns: empty ? [...need!.columns] : binding.pool_id ? poolColumns : [] },
  }
}

/**
 * A new binding for an alias the scripts need. Pools bind as read-only
 * snapshots; an alias the scripts run table steps on gets a live table for
 * this run, since a snapshot cannot run them.
 */
export function bindingForNeed(need: ScenarioDataNeed): ScenarioDataPoolBinding {
  const binding: ScenarioDataPoolBinding = { pool_id: '', alias: need.alias, access_mode: 'read_only', selection_strategy: 'sequential' }
  return usesTableSteps(need) ? { ...binding, ...tableChoicePatch(binding, 'run', [], need) } : binding
}

const listed = (items: string[]) => items.length < 2 ? items.join('') : `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`

/**
 * What the scripts need from a binding that it cannot give, found before
 * launch. `available` is what the bound table has: its columns (unknown when
 * empty) and, for a project table, its kind.
 */
export function dataBindingMismatches(binding: ScenarioDataPoolBinding, need: ScenarioDataNeed | undefined, available: { columns: string[]; kind?: LiveTableKind }): string[] {
  if (!need) return []
  const who = listed(need.scripts)
  const verb = need.scripts.length === 1 ? 's' : ''
  const problems: string[] = []
  const choice = dataTableChoice(binding)
  if (choice === 'snapshot' && usesTableSteps(need)) {
    problems.push(`${who} ${need.mutates ? `change${verb} rows in` : `read${verb} rows of`} ${binding.alias} with table steps, which a read-only snapshot cannot run. Choose a live table.`)
  }
  const kind = choice === 'run' ? binding.live_options?.profile || 'native_queue' : choice === 'project' ? available.kind : undefined
  for (const needed of need.kinds) {
    if (!kind || needed === kind) continue
    problems.push(`${who} ${needed === 'native_queue' ? `lease${verb} rows from` : `use${verb} column steps on`} ${binding.alias}, which needs a ${LIVE_TABLE_KIND_LABELS[needed]} table; this one is ${LIVE_TABLE_KIND_LABELS[kind]}.`)
  }
  const missing = available.columns.length ? need.columns.filter(column => !available.columns.includes(column)) : []
  if (missing.length) problems.push(`${binding.alias} has no ${missing.length === 1 ? 'column' : 'columns'} ${listed(missing)}, which ${who} use${verb}.`)
  if (choice !== 'snapshot' && binding.selection_strategy === 'manual' && need.readsEachIteration) {
    problems.push(`${who} read${verb} {{data.${binding.alias}.…}} every iteration, but this table hands out no row then. Under At each iteration, choose to read, remove or lease the next row.`)
  }
  return problems
}

/**
 * The columns a binding's table has, as far as the scenario can tell (empty
 * when it cannot): a snapshot has the pool's; a run table its declared
 * columns, plus the pool's when it starts from one; a project table its own.
 */
export function bindingColumns(binding: ScenarioDataPoolBinding, poolColumns: string[], projectTableColumns?: string[]): string[] {
  const choice = dataTableChoice(binding)
  if (choice === 'project') return projectTableColumns || []
  if (choice === 'snapshot') return poolColumns
  const options = { ...DEFAULT_LIVE_TABLE_OPTIONS, ...binding.live_options }
  if (options.seed_policy === 'original_seed') return []
  const declared = (options.columns || []).filter(Boolean)
  return options.seed_policy === 'empty' ? declared : [...new Set([...declared, ...poolColumns])]
}

/** Each problem with the bindings, tagged with the binding it belongs to. */
function dataBindingIssues(bindings: ScenarioDataPoolBinding[]): { index: number; message: string }[] {
  const issues: { index: number; message: string }[] = []
  const aliases = new Set<string>()
  bindings.forEach((binding, index) => {
    const add = (message: string) => issues.push({ index, message })
    if (binding.scope === 'project') {
      if (binding.access_mode !== 'live') add(`Binding ${index + 1}: project tables require live access.`)
      if (!binding.project_table_id) add(`Binding ${index + 1}: select a project live table.`)
      if (binding.pool_id) add(`Binding ${index + 1}: a project table cannot also select a data pool.`)
      if (Object.keys(binding.live_options || {}).some(key => !['empty_policy', 'wait_ms', 'operation_timeout_ms'].includes(key))) add(`Binding ${index + 1}: project tables use their own schema, capacity and lifecycle; remove run seed and output settings.`)
    } else if (!binding.pool_id && !(binding.access_mode === 'live' && ['empty', 'original_seed'].includes(binding.live_options?.seed_policy || ''))) add(`Binding ${index + 1}: select a data pool.`)
    if (!/^[A-Za-z_][\w-]*$/.test(binding.alias)) add(`Binding ${index + 1}: use a letter or underscore followed by letters, numbers, underscores or hyphens for the alias.`)
    if (binding.alias && aliases.has(binding.alias.toLowerCase())) add(`Duplicate data alias: ${binding.alias}. Each binding needs a unique alias.`)
    aliases.add(binding.alias.toLowerCase())
    if (binding.access_mode === 'live') {
      const options = binding.live_options || {}
      const columns = (options.columns || []).map(column => column.trim())
      if (binding.alias.length > 64) add(`Binding ${index + 1}: live table aliases must be at most 64 characters.`)
      if (options.seed_policy === 'original_seed' && !options.seed_run_id?.trim()) add(`Binding ${index + 1}: enter the source run ID for its original seed.`)
      if (options.seed_policy === 'empty' && columns.length === 0) add(`Binding ${index + 1}: an empty live table requires declared columns.`)
      if (columns.length > 128 || columns.some(column => !column || new TextEncoder().encode(column).length > 128 || ['\0', '\r', '\n'].some(character => column.includes(character))) || new Set(columns).size !== columns.length) add(`Binding ${index + 1}: use at most 128 distinct, nonempty column names of at most 128 bytes without line breaks.`)
      if (options.profile === 'vts_columns' && (binding.selection_strategy === 'lease' || options.end_mode === 'write_back')) add(`Binding ${index + 1}: leases and write-back require a native work queue.`)
      if (options.end_mode === 'write_back' && (!binding.pool_id || options.seed_policy === 'empty')) add(`Binding ${index + 1}: write-back requires a source data pool.`)
      if (options.end_mode === 'save_as_new' && !options.output_pool_name?.trim()) add(`Binding ${index + 1}: enter a name for the new data pool.`)
      if ((options.durability === 'durable' || binding.scope === 'project') && (options.operation_timeout_ms ?? 120000) < 65000) add(`Binding ${index + 1}: durable recovery requires an operation timeout of at least 65000 ms.`)
      if (options.empty_policy === 'wait' && ((options.wait_ms ?? 10000) <= 0 || (options.wait_ms ?? 10000) > 60000)) add(`Binding ${index + 1}: maximum wait must be between 1 and 60000 ms.`)
      if (options.empty_policy === 'wait' && (options.operation_timeout_ms ?? (options.durability === 'durable' || binding.scope === 'project' ? 120000 : 10000)) < (options.wait_ms ?? 10000)) add(`Binding ${index + 1}: operation timeout must cover the maximum wait.`)
    }
  })
  return issues
}

export function dataBindingErrors(bindings: ScenarioDataPoolBinding[]): string[] {
  return dataBindingIssues(bindings).map(issue => issue.message)
}

/** A binding just added: nothing chosen or typed yet. Its errors wait until it is. */
export function isUntouchedDataBinding(binding: ScenarioDataPoolBinding): boolean {
  return !binding.pool_id && !binding.alias && binding.access_mode !== 'live' && !binding.project_table_id
}

/** dataBindingErrors without the errors of untouched bindings. */
export function touchedDataBindingErrors(bindings: ScenarioDataPoolBinding[]): string[] {
  return dataBindingIssues(bindings).filter(issue => !isUntouchedDataBinding(bindings[issue.index])).map(issue => issue.message)
}

/** Why a live binding cannot take a column of this name ('' when it can). */
export function bindingColumnProblem(name: string, columns: readonly string[]): string {
  if (!name) return 'Enter a column name.'
  if (new TextEncoder().encode(name).length > 128 || /[\0\r\n]/.test(name)) return 'Use at most 128 bytes, without line breaks.'
  if (columns.includes(name)) return `There is already a column named "${name}".`
  if (columns.length >= 128) return 'A live table takes at most 128 columns.'
  return ''
}

/** An alias from a pool's name ("Customer accounts" → customer_accounts), unique among taken. */
export function aliasForPool(name: string, taken: readonly string[]): string {
  let base = name.toLowerCase().replace(/[^a-z0-9_-]+/g, '_').replace(/^[_-]+|[_-]+$/g, '').slice(0, 56)
  if (!base) base = 'data'
  if (!/^[a-z_]/.test(base)) base = `_${base}`
  const used = new Set(taken.map(alias => alias.toLowerCase()))
  let alias = base
  for (let suffix = 2; used.has(alias); suffix++) alias = `${base}_${suffix}`
  return alias
}

/** A pool's columns in its own order (or its schema's, for older pools). */
export function dataPoolColumnNames(pool?: SharedDataPool): string[] {
  return pool?.columns?.length ? [...pool.columns] : Object.keys(pool?.schema || {})
}

function liveScopeLabel(binding: ScenarioDataPoolBinding): string {
  return binding.scope === 'project' ? 'shared across project runs' : 'this run only'
}

/** A live binding kept as saved: "work — live table, this run only". */
export function liveBindingSummary(binding: ScenarioDataPoolBinding): string {
  return `${binding.alias || 'Unnamed alias'} — live table, ${liveScopeLabel(binding)}`
}

/** A scenario's live tables, as a run of it will use them. */
export function scenarioLiveTables(bindings?: ScenarioDataPoolBinding[]): { list: string; runScoped: boolean } | null {
  const live = (bindings || []).filter(binding => binding.access_mode === 'live')
  if (live.length === 0) return null
  return {
    list: live.map(binding => `${binding.alias || 'Unnamed alias'} (${liveScopeLabel(binding)})`).join(', '),
    runScoped: live.some(binding => binding.scope !== 'project'),
  }
}

/**
 * One binding as a launch review states it: what data the run reads, and for a
 * live table where it starts and what happens to it at the end. writesBack marks
 * a binding that changes its source pool when the run ends.
 */
export function describeDataBinding(binding: ScenarioDataPoolBinding, poolName: (id: string) => string | undefined): { text: string; writesBack: boolean } {
  const alias = binding.alias || 'Unnamed alias'
  const pool = binding.pool_name || (binding.pool_id ? poolName(binding.pool_id) || binding.pool_id : '')
  if (binding.access_mode !== 'live') {
    return { text: `${alias}: ${pool || 'no pool'} (read-only snapshot, ${binding.selection_strategy || 'sequential'} rows)`, writesBack: false }
  }
  if (binding.scope === 'project') {
    return { text: `${alias}: project live table, shared across project runs`, writesBack: false }
  }
  const options = { ...DEFAULT_LIVE_TABLE_OPTIONS, ...binding.live_options }
  const seed = options.seed_policy === 'empty' || (!binding.pool_id && options.seed_policy !== 'original_seed')
    ? 'starts empty'
    : options.seed_policy === 'original_seed'
      ? `starts from run ${options.seed_run_id || '(not set)'}'s original rows`
      : `starts from ${pool}`
  const end = options.end_mode === 'write_back'
    ? `written back to ${pool || 'its pool'} at the end, changing that pool`
    : options.end_mode === 'save_as_new'
      ? `saved as a new pool${options.output_pool_name ? ` "${options.output_pool_name}"` : ''} at the end`
      : 'discarded at the end'
  return { text: `${alias}: live table for this run, ${seed}, ${end}`, writesBack: options.end_mode === 'write_back' }
}
