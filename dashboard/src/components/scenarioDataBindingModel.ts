import type { ScenarioDataPoolBinding, StoredScript } from '../types'

export function scenarioDataRequirements(scripts: StoredScript[]): Map<string, Set<string>> {
  const requirements = new Map<string, Set<string>>()
  for (const script of scripts) {
    const text = JSON.stringify(script.script || {})
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
    if (!binding.pool_id) errors.push(`Binding ${index + 1}: select a data pool.`)
    if (!/^[A-Za-z_][\w-]*$/.test(binding.alias)) errors.push(`Binding ${index + 1}: use a letter or underscore followed by letters, numbers, underscores or hyphens for the alias.`)
    if (aliases.has(binding.alias)) errors.push(`Duplicate data alias: ${binding.alias}. Each binding needs a unique alias.`)
    aliases.add(binding.alias)
  })
  return errors
}
