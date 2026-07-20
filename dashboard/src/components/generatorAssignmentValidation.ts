import { GeneratorAssignmentSettings } from '../types'

export type GeneratorAssignmentScope = 'scenario' | 'group'

export function isPositiveSafeInteger(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0
}

function validateWeights(weights: Record<string, number> | undefined, label: string, kind: string): string | undefined {
  const entries = Object.entries(weights || {})
  if (entries.length === 0) return `${label}: add at least one ${kind} weight.`
  for (const [name, value] of entries) {
    if (!name.trim()) return `${label}: ${kind} weight names cannot be empty.`
    if (!isPositiveSafeInteger(value)) return `${label}: ${kind} weights must be positive whole numbers.`
  }
  return undefined
}

export function validateGeneratorAssignment(
  assignment: GeneratorAssignmentSettings | undefined,
  scope: GeneratorAssignmentScope,
  label: string,
): string | undefined {
  if (!assignment) return undefined

  if (scope === 'group') {
    if (assignment.mode === 'region_weighted' || assignment.mode === 'load_zone_weighted') {
      return `${label}: weighted placement is supported only at scenario scope. Choose inherit, automatic, specific hosts, or required capabilities.`
    }
    if (assignment.tolerate_generator_loss) {
      return `${label}: generator-loss tolerance is supported only at scenario scope. Remove the group-level setting before saving.`
    }
  }

  if (assignment.mode === 'specific_hosts') {
    if (!(assignment.selected_targets || []).some(target => target.trim())) {
      return `${label}: select at least one load generator host for specific-host placement.`
    }
  }
  if (assignment.mode === 'region_weighted') {
    return validateWeights(assignment.region_weights, label, 'region')
  }
  if (assignment.mode === 'load_zone_weighted') {
    return validateWeights(assignment.load_zone_weights, label, 'load-zone')
  }
  return undefined
}
