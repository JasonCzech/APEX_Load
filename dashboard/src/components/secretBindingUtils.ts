import { type SecretBindingRequirement, type SecretBindingSelection } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

export const MAX_SECRET_BINDING_NAME_LENGTH = 249
const secretBindingNamePattern = /^[A-Z0-9_]{1,249}$/
const nativeBrunoSourceIdentitySuffixPattern = /^(.+)_([A-F0-9]{12}|[A-F0-9]{24})$/
// An older native Bruno header, query, body or auth secret was named after the
// request's path, then the kind and field, then the identity:
// CTA_ORDERS_GET_ORDER_HEADER_X_API_KEY_84921F7A1055. Its label is the name a
// new import gives the same field (X_API_KEY, or AUTH_PASSWORD for auth). A
// header, query or body marker wins over AUTH, which header names often hold
// (X_CDF_AUTH_TOKEN).
const nativeBrunoPathPrefixedFieldPattern = /^(?:.*_)?(?:HEADER|QUERY|BODY_JSON|BODY_XML|BODY_FORM|BODY_MULTIPART)_([A-Z0-9][A-Z0-9_]*)_[A-F0-9]{12}$/
const nativeBrunoPathPrefixedAuthPattern = /^(?:.*_)?AUTH_([A-Z0-9][A-Z0-9_]*)_[A-F0-9]{12}$/
// Before readable numbering, every importer's shared allocator named a second
// credential wanting a taken name with 12 hex digits of its source identity.
const importedCollisionSuffixPattern = /^(.+)_((?=[A-F]*[0-9])[A-F0-9]{12})$/

export function isValidSecretBindingName(value: string): boolean {
  return secretBindingNamePattern.test(value.trim())
}

export function secretBindingReference(value: string): string {
  return `{{secret.${value.trim()}}}`
}

function isNativeBrunoImport(script: ScriptDefinition): boolean {
  const imported = script.metadata?.import
  return imported?.source === 'bruno'
    && imported.preserved?.native_importer === 'apexload-bruno-native-importer'
}

function friendlySecretBindingBaseName(value: string, script: ScriptDefinition | null | undefined): string {
  const name = value.trim()
  if (!script) return name
  if (isNativeBrunoImport(script)) {
    const field = name.match(nativeBrunoPathPrefixedFieldPattern)?.[1]
    if (field) return field
    const authField = name.match(nativeBrunoPathPrefixedAuthPattern)?.[1]
    if (authField) return `AUTH_${authField}`
    return name.match(nativeBrunoSourceIdentitySuffixPattern)?.[1] || name
  }
  if (script.metadata?.import?.source) return name.match(importedCollisionSuffixPattern)?.[1] || name
  return name
}

// Older imports named secrets with a hashed identity: a native Bruno import
// always (48 or 96 bits, because Bruno names are case-sensitive), any other
// import on a name collision (48 bits). Simplify only those known shapes, in
// an imported script. When two exact references simplify to the same label,
// retain a stable visible discriminator so operators cannot confuse their
// write-only values. Pass every secret the script references, not only the
// ones a surface lists, so a name reads the same on every surface.
export function secretBindingDisplayNames(
  values: readonly string[],
  script: ScriptDefinition | null | undefined,
): Map<string, string> {
  const exactNames = Array.from(new Set(values.map(value => value.trim()).filter(Boolean)))
  const baseByExactName = new Map(exactNames.map(name => [name, friendlySecretBindingBaseName(name, script)]))
  const exactNamesByBase = new Map<string, string[]>()
  for (const name of exactNames) {
    const base = baseByExactName.get(name) || name
    exactNamesByBase.set(base, [...(exactNamesByBase.get(base) || []), name])
  }

  const result = new Map<string, string>()
  for (const [base, names] of exactNamesByBase) {
    const sortedNames = names.slice().sort((left, right) => left.localeCompare(right))
    for (const [index, name] of sortedNames.entries()) {
      result.set(name, sortedNames.length === 1 ? base : `${base} · source ${index + 1}`)
    }
  }
  return result
}

// The label for one secret name: its display name, or the name itself.
export function secretDisplayName(labels: ReadonlyMap<string, string>, name: string): string {
  return labels.get(name.trim()) || name.trim()
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

/** Whether the server can swap this occurrence for a project-secret reference. */
export function requirementCanUseProjectSecret(requirement: SecretBindingRequirement): boolean {
  return !requirement.rebind_unavailable_reason
}

/**
 * Whether review binds this occurrence to a project secret by default: its
 * imported value is not kept, and a project secret can replace it. An
 * occurrence that can be neither stays as recorded.
 */
export function requirementNeedsSecretBinding(requirement: SecretBindingRequirement): boolean {
  return requirement.destination_value_preserved !== true && requirementCanUseProjectSecret(requirement)
}

// A rebind refusal carries the refreshed review too: the selection could not
// take a project secret, and the refreshed requirements say so.
const REFRESHING_SECRET_BINDING_CODES: ReadonlySet<unknown> = new Set([
  'secret_binding_requirements_changed',
  'secret_binding_rebind_unavailable',
])

export function refreshedSecretBindingRequirements(error: unknown): SecretBindingRequirement[] | null {
  if (!error || typeof error !== 'object' || !REFRESHING_SECRET_BINDING_CODES.has((error as { code?: unknown }).code)) {
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
