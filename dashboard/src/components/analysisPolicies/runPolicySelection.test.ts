import { describe, expect, it } from 'vitest'
import { cloneRunPolicySelections, runPolicySelectionProblem } from './runPolicySelection'

describe('immutable run policy selections', () => {
  it('keeps omission and explicit defaults distinct and clones both kinds', () => {
    expect(cloneRunPolicySelections(undefined)).toBeUndefined()
    expect(cloneRunPolicySelections({})).toEqual({})
    const source = { browser: { id: 'b', revision: 2 }, journey: { id: 'j', revision: 1 } }
    const copy = cloneRunPolicySelections(source)!
    copy.browser!.revision = 3
    copy.journey!.id = 'other'
    expect(source).toEqual({ browser: { id: 'b', revision: 2 }, journey: { id: 'j', revision: 1 } })
    expect(runPolicySelectionProblem(source)).toBe('')
    expect(runPolicySelectionProblem(undefined)).toBe('')
    expect(runPolicySelectionProblem({})).toBe('')
  })
  it.each([null, [], 'bad', { telemetry: { id: 'x', revision: 1 } }, { browser: null }, { browser: { id: '', revision: 1 } }, { browser: { id: ' x', revision: 1 } }, { browser: { id: 'x', revision: 0 } }, { browser: { id: 'x', revision: 1.5 } }, { browser: { id: 'x', revision: Number.NaN } }, { browser: { id: 'x', revision: Number.MAX_SAFE_INTEGER + 1 } }])('refuses invalid selection %j', value => {
    expect(runPolicySelectionProblem(value)).not.toBe('')
  })
})

 it('rejects caller-supplied pin digests instead of submitting them as selections', () => {
  expect(runPolicySelectionProblem({ browser: { id: 'chosen', revision: 1, sha256: 'client-minted' } })).not.toBe('')
 })
