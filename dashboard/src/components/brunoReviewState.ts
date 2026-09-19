import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'

export function brunoVariableCandidate(shared: SharedValues | undefined, variable: string) {
  return shared?.candidates.find(value => value.kind === 'source_variable'
    && !value.credential_shaped && (value.source_variable || value.name) === variable)
}

export function brunoMissingVariables(candidates: BrunoNativeScriptCandidate[], shared: SharedValues | undefined, values: Record<string, string>) {
  return [...new Set(candidates.flatMap(candidate => candidate.missing_variables || []))]
    .filter(variable => {
      const candidate = brunoVariableCandidate(shared, variable)
      return !candidate || !values[candidate.id]?.trim()
    }).sort()
}

export function brunoCandidateNeedsSecrets(candidate: BrunoNativeScriptCandidate, destinations: Readonly<Record<string, { readyForExecution?: boolean }>> = {}) {
  return (candidate.secret_requirements || []).some(value =>
    destinations[value.id]?.readyForExecution === undefined
      ? value.destination_value_preserved !== true
      : !destinations[value.id].readyForExecution)
}

