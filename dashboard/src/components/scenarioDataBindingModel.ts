import type { LiveTableOptions } from '../types/dataPools'
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
export function dataBindingErrors(bindings: ScenarioDataPoolBinding[]): string[] {
  const errors: string[] = []
  const aliases = new Set<string>()
  bindings.forEach((binding, index) => {
    if (binding.scope === 'project') {
      if (binding.access_mode !== 'live') errors.push(`Binding ${index + 1}: project tables require live access.`)
      if (!binding.project_table_id) errors.push(`Binding ${index + 1}: select a project live table.`)
      if (binding.pool_id) errors.push(`Binding ${index + 1}: a project table cannot also select a data pool.`)
      if (Object.keys(binding.live_options || {}).some(key => !['empty_policy', 'wait_ms', 'operation_timeout_ms'].includes(key))) errors.push(`Binding ${index + 1}: project tables use their own schema, capacity and lifecycle; remove run seed and output settings.`)
    } else if (!binding.pool_id && !(binding.access_mode === 'live' && ['empty', 'original_seed'].includes(binding.live_options?.seed_policy || ''))) errors.push(`Binding ${index + 1}: select a data pool.`)
    if (!/^[A-Za-z_][\w-]*$/.test(binding.alias)) errors.push(`Binding ${index + 1}: use a letter or underscore followed by letters, numbers, underscores or hyphens for the alias.`)
    if (aliases.has(binding.alias.toLowerCase())) errors.push(`Duplicate data alias: ${binding.alias}. Each binding needs a unique alias.`)
    aliases.add(binding.alias.toLowerCase())
    if (binding.access_mode === 'live') {
      const options = binding.live_options || {}
      const columns = (options.columns || []).map(column => column.trim())
      if (binding.alias.length > 64) errors.push(`Binding ${index + 1}: live table aliases must be at most 64 characters.`)
      if (options.seed_policy === 'original_seed' && !options.seed_run_id?.trim()) errors.push(`Binding ${index + 1}: enter the source run ID for its original seed.`)
      if (options.seed_policy === 'empty' && columns.length === 0) errors.push(`Binding ${index + 1}: an empty live table requires declared columns.`)
      if (columns.length > 128 || columns.some(column => !column || new TextEncoder().encode(column).length > 128 || ['\0', '\r', '\n'].some(character => column.includes(character))) || new Set(columns).size !== columns.length) errors.push(`Binding ${index + 1}: use at most 128 distinct, nonempty column names of at most 128 bytes without line breaks.`)
      if (options.profile === 'vts_columns' && (binding.selection_strategy === 'lease' || options.end_mode === 'write_back')) errors.push(`Binding ${index + 1}: leases and write-back require a native work queue.`)
      if (options.end_mode === 'write_back' && (!binding.pool_id || options.seed_policy === 'empty')) errors.push(`Binding ${index + 1}: write-back requires a source data pool.`)
      if (options.end_mode === 'save_as_new' && !options.output_pool_name?.trim()) errors.push(`Binding ${index + 1}: enter a name for the new data pool.`)
      if ((options.durability === 'durable' || binding.scope === 'project') && (options.operation_timeout_ms ?? 120000) < 65000) errors.push(`Binding ${index + 1}: durable recovery requires an operation timeout of at least 65000 ms.`)
      if (options.empty_policy === 'wait' && ((options.wait_ms ?? 10000) <= 0 || (options.wait_ms ?? 10000) > 60000)) errors.push(`Binding ${index + 1}: maximum wait must be between 1 and 60000 ms.`)
      if (options.empty_policy === 'wait' && (options.operation_timeout_ms ?? (options.durability === 'durable' || binding.scope === 'project' ? 120000 : 10000)) < (options.wait_ms ?? 10000)) errors.push(`Binding ${index + 1}: operation timeout must cover the maximum wait.`)
    }
  })
  return errors
}
