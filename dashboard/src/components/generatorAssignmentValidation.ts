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

  const spareBudget = assignment.recovery_spare_vusers ?? 0
  if (!Number.isSafeInteger(spareBudget) || spareBudget < 0 || spareBudget > 1000000) {
    return `${label}: recovery spare VUser budget must be a whole number from 0 to 1000000.`
  }
  if (spareBudget > 0 && (scope !== 'scenario' || !assignment.tolerate_generator_loss || assignment.source_affinity || assignment.mode === 'region_weighted' || assignment.mode === 'load_zone_weighted')) {
    return `${label}: recovery spare reservations require generator-loss tolerance with automatic or specific-host placement at scenario scope.`
  }

  if (assignment.java_host) {
    const profile = assignment.java_host
    if (scope !== 'group') return `${label}: Java execution profiles are selected per group.`
    if (!profile.profile_id.trim() || profile.profile_id !== profile.profile_id.trim() || profile.profile_id.length > 128) return `${label}: select an operator-approved Java profile.`
    if (!isPositiveSafeInteger(profile.preparation_ms) || profile.preparation_ms > 3600000
      || !isPositiveSafeInteger(profile.run_lifetime_ms) || profile.run_lifetime_ms > 86400000) return `${label}: Java preparation and lifetime limits must be positive and within one hour and 24 hours respectively.`
  }

  if (scope === 'group') {
    if (assignment.mode === 'region_weighted' || assignment.mode === 'load_zone_weighted') {
      return `${label}: weighted placement is supported only at scenario scope. Choose inherit, automatic, specific hosts, or required capabilities.`
    }
    if (assignment.tolerate_generator_loss) {
      return `${label}: generator-loss tolerance is supported only at scenario scope. Remove the group-level setting before saving.`
    }
    if (assignment.failover_policy) {
      return `${label}: controller-failover continuity is supported only at scenario scope. Remove the group-level setting before saving.`
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
