import { describe, expect, it } from 'vitest'
import { brunoMissingVariables, brunoVariableCandidate, brunoCandidateNeedsSecrets, suggestedInputSecretName, unusedSecretName } from './brunoReviewState'
import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'
const candidate = (missing_variables: string[]) => ({ missing_variables, script: { name: 'API' } } as BrunoNativeScriptCandidate)
const shared = { candidates: [{ id: 'env-id', kind: 'source_variable', name: 'env', source_variable: 'env' }], summary: {} } as SharedValues

describe('Bruno review readiness', () => {
  it('counts selected APIs only and resolves explicitly supplied literal inputs', () => {
    expect(brunoMissingVariables([candidate(['env', '$isoTimestamp']), candidate(['env'])], shared, { 'env-id': 'qa' })).toEqual(['$isoTimestamp'])
    expect(brunoMissingVariables([], shared, {})).toEqual([])
    expect(brunoMissingVariables([candidate(['env'])], shared, { 'env-id': ' ' })).toEqual(['env'])
  })
  it('does not treat arbitrary values or optional substitutions as producers', () => {
    expect(brunoMissingVariables([candidate(['env'])], shared, { env: 'qa' })).toEqual(['env'])
    expect(brunoVariableCandidate({ ...shared, candidates: [{ ...shared.candidates[0], kind: 'repeated_literal' }] }, 'env')).toBeUndefined()
    expect(brunoVariableCandidate({ ...shared, candidates: [{ ...shared.candidates[0], credential_shaped: true }] }, 'env')).toBeUndefined()
  })
  it('distinguishes preserved credentials from missing binding requirements', () => {
    expect(brunoCandidateNeedsSecrets({ ...candidate([]), secret_requirements: [{ destination_value_preserved: true }] } as BrunoNativeScriptCandidate)).toBe(false)
    expect(brunoCandidateNeedsSecrets({ ...candidate([]), secret_requirements: [{}] } as BrunoNativeScriptCandidate)).toBe(true)
    expect(brunoCandidateNeedsSecrets({ ...candidate([]), secret_requirements: [{ id: 'secret' }] } as BrunoNativeScriptCandidate, { secret: { readyForExecution: true } })).toBe(false)
  })
})

describe('credential input secret names', () => {
  it('names a secret after the input, numbered past the names the project holds', () => {
    expect(suggestedInputSecretName('apiToken')).toBe('API_TOKEN')
    expect(suggestedInputSecretName('x-session.id')).toBe('X_SESSION_ID')
    expect(unusedSecretName('API_TOKEN', [])).toBe('API_TOKEN')
    expect(unusedSecretName('API_TOKEN', ['API_TOKEN', 'API_TOKEN_2'])).toBe('API_TOKEN_3')
    const long = 'A'.repeat(249)
    expect(unusedSecretName(long, [long])).toBe(`${'A'.repeat(247)}_2`)
  })
})
