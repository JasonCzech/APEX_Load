import { type SecretBindingRequirement, type SecretBindingSelection } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

export const MAX_SECRET_BINDING_NAME_LENGTH = 249
const secretBindingNamePattern = /^[A-Z0-9_]{1,249}$/
const generatedSecretIdentitySuffixPattern = /_(?:[A-F0-9]{24}|[A-F0-9]{12}(?:_[2-9][0-9]*)?)$/

export function isValidSecretBindingName(value: string): boolean {
  return secretBindingNamePattern.test(value.trim())
}

export function secretBindingReference(value: string): string {
  return `{{secret.${value.trim()}}}`
}

// Importers append provenance-derived hexadecimal identities when authored
// names would otherwise collide or lose case-sensitive source identity. Keep
// those exact names for storage and execution while presenting the authored
// portion in operator-facing binding controls.
export function friendlySecretBindingName(value: string): string {
  const name = value.trim()
  const friendly = name.replace(generatedSecretIdentitySuffixPattern, '')
  return friendly || name
}

export function collisionSafeSecretName(base: string, ordinal: number): string {
  const suffix = `_${ordinal}`
  return `${base.slice(0, MAX_SECRET_BINDING_NAME_LENGTH - suffix.length)}${suffix}`
}

export function requirementsPreserveDestinationValues(
  requirements: readonly SecretBindingRequirement[],
): boolean {
  return (
    requirements.length > 0
    && requirements.every(requirement => requirement.destination_value_preserved === true)
  )
}

export function refreshedSecretBindingRequirements(error: unknown): SecretBindingRequirement[] | null {
  if (!error || typeof error !== 'object' || (error as { code?: unknown }).code !== 'secret_binding_requirements_changed') {
    return null
  }
  const payload = (error as { payload?: unknown }).payload
  if (!payload || typeof payload !== 'object') return null
  const requirements = (payload as { secret_binding_requirements?: unknown }).secret_binding_requirements
  if (!Array.isArray(requirements)) return null
  if (!requirements.every(requirement => (
    requirement &&
    typeof requirement === 'object' &&
    typeof (requirement as SecretBindingRequirement).id === 'string' &&
    typeof (requirement as SecretBindingRequirement).path === 'string' &&
    typeof (requirement as SecretBindingRequirement).suggested_name === 'string'
  ))) {
    return null
  }
  return requirements as SecretBindingRequirement[]
}

function belongsToScript(
  requirement: SecretBindingRequirement,
  scriptIndex: number,
  script: ScriptDefinition,
): boolean {
  if (typeof requirement.script_index === 'number') {
    return requirement.script_index === scriptIndex
  }
  if (requirement.script_name) {
    return requirement.script_name === script.name
  }
  return scriptIndex === 0
}

export function secretBindingRequirementsForScript(
  requirements: SecretBindingRequirement[] | undefined,
  scriptIndex: number,
  script: ScriptDefinition,
): SecretBindingRequirement[] {
  return (requirements ?? []).filter(requirement => belongsToScript(requirement, scriptIndex, script))
}

export function replaceSecretBindingRequirementsForScript(
  requirements: SecretBindingRequirement[] | undefined,
  scriptIndex: number,
  script: ScriptDefinition,
  replacements: SecretBindingRequirement[],
): SecretBindingRequirement[] {
  const existing = requirements ?? []
  const untouched = existing.filter(requirement => !belongsToScript(requirement, scriptIndex, script))
  // The apply endpoint re-discovers one script in isolation. Its refreshed
  // suggestions therefore cannot reproduce suffixes allocated across the
  // complete archive (for example API_TOKEN_2). Once a requirement has an
  // archive-scoped or user-selected logical name, retain that name while its
  // opaque requirement ID remains stable. A genuinely new requirement ID still
  // receives the server's current suggestion.
  const priorNamesByID = new Map(existing
    .filter(requirement => belongsToScript(requirement, scriptIndex, script))
    .map(requirement => [requirement.id, requirement.suggested_name]))
  const scopedReplacements = replacements.map(requirement => {
    const priorName = priorNamesByID.get(requirement.id)?.trim()
    return {
      ...requirement,
      ...(priorName ? { suggested_name: priorName } : {}),
      script_index: requirement.script_index ?? scriptIndex,
      script_name: requirement.script_name || script.name,
    }
  })
  return [...untouched, ...scopedReplacements]
}

export function updateSecretBindingRequirementNames(
  requirements: SecretBindingRequirement[] | undefined,
  selections: readonly SecretBindingSelection[],
): SecretBindingRequirement[] {
  const namesByRequirement = new Map(
    selections.map(selection => [selection.requirement_id, selection.secret_name.trim()]),
  )
  return (requirements ?? []).map(requirement => {
    const migratedName = namesByRequirement.get(requirement.id)
    if (!migratedName || migratedName === requirement.suggested_name) return requirement
    return {
      ...requirement,
      suggested_name: migratedName,
    }
  })
}
