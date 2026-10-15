import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'

export function brunoVariableCandidate(shared: SharedValues | undefined, variable: string) {
  return shared?.candidates.find(value => value.kind === 'source_variable'
    && !value.credential_shaped && (value.source_variable || value.name) === variable)
}

/** How an input is supplied when it is not one value for every user. */
export interface BrunoInputBinding {
  mode: 'data' | 'generated' | 'secret'
  alias?: string
  column?: string
  generator?: 'uuid' | 'timestamp' | 'digits'
  digits?: number
  /** The project secret a credential input reads. */
  secret?: string
}

const dataReferenceName = /^[A-Za-z0-9_-]{1,128}$/
const projectSecretName = /^[A-Z0-9_]{1,249}$/

/** apiToken → API_TOKEN: the project secret name a credential input suggests. */
export function suggestedInputSecretName(variable: string): string {
  return variable
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/[^A-Za-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .toUpperCase()
    .slice(0, 249)
}

/**
 * name, or name_2, name_3… when the project already holds it, so a credential
 * input never reads another credential's secret by default (as Bruno's
 * credential review numbers a name the project already uses).
 */
export function unusedSecretName(name: string, taken: readonly string[]): string {
  const used = new Set(taken)
  if (!used.has(name)) return name
  for (let number = 2; ; number++) {
    const suffix = `_${number}`
    const candidate = name.slice(0, 249 - suffix.length) + suffix
    if (!used.has(candidate)) return candidate
  }
}

/** Whether a binding names a source the server accepts. */
export function brunoInputBindingComplete(binding: BrunoInputBinding | undefined): boolean {
  if (!binding) return false
  if (binding.mode === 'data') return dataReferenceName.test(binding.alias || '') && dataReferenceName.test(binding.column || '')
  if (binding.mode === 'secret') return projectSecretName.test(binding.secret || '')
  if (binding.generator === 'digits') return Number.isInteger(binding.digits) && binding.digits! >= 1 && binding.digits! <= 64
  return binding.generator === 'uuid' || binding.generator === 'timestamp'
}

export function brunoMissingVariables(candidates: BrunoNativeScriptCandidate[], shared: SharedValues | undefined, values: Record<string, string>, bindings: Record<string, BrunoInputBinding> = {}) {
  return [...new Set(candidates.flatMap(candidate => candidate.missing_variables || []))]
    .filter(variable => {
      const candidate = brunoVariableCandidate(shared, variable)
      return !candidate || (!values[candidate.id]?.trim() && !brunoInputBindingComplete(bindings[candidate.id]))
    }).sort()
}

export function brunoCandidateNeedsSecrets(candidate: BrunoNativeScriptCandidate, destinations: Readonly<Record<string, { readyForExecution?: boolean }>> = {}) {
  return (candidate.secret_requirements || []).some(value =>
    destinations[value.id]?.readyForExecution === undefined
      ? value.destination_value_preserved !== true
      : !destinations[value.id].readyForExecution)
}

