import { describe, expect, it } from 'vitest'
import { parseLiveTableSandboxSeeds } from './liveTableSandboxModel'
import { safeProfile } from './debuggerExperience'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'

const seed = { alias: 'work', columns: ['id', 'note'], rows: [{ values: { id: 'one', note: '' } }, { values: { id: 'two' } }] }
describe('isolated replay seed contract', () => {
  it('retains absent and empty cells and accepts explicit empty producer tables', () => {
    expect(parseLiveTableSandboxSeeds(JSON.stringify([seed]))).toEqual([seed])
    expect(parseLiveTableSandboxSeeds(JSON.stringify([{ ...seed, rows: [] }]))[0].rows).toEqual([])
    expect(parseLiveTableSandboxSeeds('')).toEqual([])
  })
  it.each([
    ['invalid JSON', '{'], ['nonarray', '{}'], ['too many tables', JSON.stringify(Array(5).fill(seed))],
    ['duplicate aliases', JSON.stringify([seed, { ...seed, alias: 'WORK' }])],
    ['run identity', JSON.stringify([{ ...seed, run_id: 'production' }])],
    ['row identity', JSON.stringify([{ ...seed, rows: [{ id: 'production', values: {} }] }])],
    ['unknown column', JSON.stringify([{ ...seed, rows: [{ values: { other: 'value' } }] }])],
    ['managed secret', JSON.stringify([{ ...seed, rows: [{ values: { id: '{{ secret.API_KEY}}' } }] }])],
    ['nonstrings', JSON.stringify([{ ...seed, rows: [{ values: { id: 1 } }] }])],
    ['byte limit', ' '.repeat(65537)],
    ['field limit', JSON.stringify([{ ...seed, rows: [{ values: { id: 'a'.repeat(8193) } }] }])],
  ])('rejects %s before replay', (_name, text) => {
    expect(() => parseLiveTableSandboxSeeds(text)).toThrow()
  })
  it('excludes inline seed values from persistent named profiles', () => {
    const profile = safeProfile('isolated', { ...DEFAULT_REPLAY_OPTIONS, liveTableSeedsText: JSON.stringify([seed]) })
    expect(JSON.stringify(profile)).not.toContain('liveTableSeedsText')
    expect(JSON.stringify(profile)).not.toContain('one')
  })
})
