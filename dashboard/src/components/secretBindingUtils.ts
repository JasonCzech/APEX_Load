import { type SecretBindingRequirement } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

export const MAX_SECRET_BINDING_NAME_LENGTH = 249
const secretBindingNamePattern = /^[A-Z0-9_]{1,249}$/

export function isValidSecretBindingName(value: string): boolean {
  return secretBindingNamePattern.test(value.trim())
}

export function secretBindingReference(value: string): string {
  return `{{secret.${value.trim()}}}`
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
  const untouched = (requirements ?? []).filter(requirement => !belongsToScript(requirement, scriptIndex, script))
  const scopedReplacements = replacements.map(requirement => ({
    ...requirement,
    script_index: requirement.script_index ?? scriptIndex,
    script_name: requirement.script_name || script.name,
  }))
  return [...untouched, ...scopedReplacements]
}
