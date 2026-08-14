import { type BrunoSecretRequirement } from '../types/scriptImports'

export function aggregateBrunoSecretRequirements(
  requirements: readonly BrunoSecretRequirement[],
): BrunoSecretRequirement[] {
  const byID = new Map<string, {
    requirement: BrunoSecretRequirement
    affectedPaths: Set<string>
  }>()
  for (const requirement of requirements) {
    const existing = byID.get(requirement.id)
    const affectedPaths = requirement.affected_paths?.length
      ? requirement.affected_paths
      : requirement.path ? [requirement.path] : []
    if (!existing) {
      byID.set(requirement.id, {
        requirement: { ...requirement },
        affectedPaths: new Set(affectedPaths),
      })
      continue
    }
    affectedPaths.forEach(path => existing.affectedPaths.add(path))
    existing.requirement = {
      ...existing.requirement,
      display_label: existing.requirement.display_label || requirement.display_label,
      source_variable: existing.requirement.source_variable || requirement.source_variable,
      unresolved_source_variable: existing.requirement.unresolved_source_variable === true || requirement.unresolved_source_variable === true,
      source_value_available: existing.requirement.source_value_available === true || requirement.source_value_available === true,
      destination_value_preserved: existing.requirement.destination_value_preserved === true && requirement.destination_value_preserved === true,
      preservation_unavailable_reason: existing.requirement.preservation_unavailable_reason || requirement.preservation_unavailable_reason,
    }
  }
  return Array.from(byID.values(), ({ requirement, affectedPaths }) => ({
    ...requirement,
    affected_paths: Array.from(affectedPaths).sort(),
  }))
}
