import type { LiveTableOptions, SharedDataPool } from '../types/dataPools'
import type { ScenarioDataPoolBinding, StoredScript } from '../types'

export const DEFAULT_LIVE_TABLE_OPTIONS: LiveTableOptions = {
  profile: 'native_queue', seed_policy: 'current_at_start', durability: 'memory', end_mode: 'discard', empty_policy: 'error',
}

export function scenarioDataRequirements(scripts: StoredScript[]): Map<string, Set<string>> {
  const requirements = new Map<string, Set<string>>()
  for (const script of scripts) {
    for (const declaration of script.script?.config?.native_services?.live_tables || []) requirements.set(declaration.alias, requirements.get(declaration.alias) || new Set<string>())
    const literalSourceOwners = new WeakSet<object>()
    // Traverse executable action locations only. Request parameters and data
    // rows can contain action-shaped JSON without becoming executable DSL.
    const pending = [...(script.script?.init || []), ...(script.script?.actions || []), ...(script.script?.teardown || [])]
    while (pending.length > 0) {
      const action = pending.pop()!
      if (action.type === 'native_javascript' && action.params) {
        literalSourceOwners.add(action.params)
      }
      if (action.type === 'live_table' && action.params) {
        const params = action.params
        if (typeof params.alias === 'string') {
          const columns = requirements.get(params.alias) || new Set<string>()
          if (Array.isArray(params.columns)) params.columns.forEach(column => { if (typeof column === 'string') columns.add(column) })
          for (const field of ['values', 'expected']) if (params[field] && typeof params[field] === 'object') Object.keys(params[field] as object).forEach(column => columns.add(column))
          requirements.set(params.alias, columns)
        }
      }
      const control = action.control
      if (control) pending.push(...(control.steps || []), ...(control.else || []), ...(control.default || []), ...(control.cases || []).flatMap(branch => branch.steps || []), ...(control.branches || []).flatMap(branch => branch.steps || []))
    }
    // Only the action's own source is literal. Nested input values still
    // resolve templates, even if their JSON happens to resemble an action.
    const text = JSON.stringify(script.script || {}, function (this: object, key: string, value: unknown) {
      return key === 'source' && literalSourceOwners.has(this) ? undefined : value
    })
    for (const match of text.matchAll(/\{\{\s*data\.([\w-]+)\.([\w-]+)\s*\}\}/g)) {
      const columns = requirements.get(match[1]) || new Set<string>()
      columns.add(match[2]); requirements.set(match[1], columns)
    }
  }
  return requirements
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
