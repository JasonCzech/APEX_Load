import { describe, expect, it } from 'vitest'
import { liveTableFailureNote, liveTableStepResult, parseLiveTableSandboxSeeds, sandboxSeedsForScript } from './liveTableSandboxModel'
import type { DataAliasUsage } from './scriptDataUsageModel'
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

describe('sandbox tables from the script', () => {
  const usage = (alias: string, extra: Partial<DataAliasUsage> = {}): DataAliasUsage => ({ alias, columns: [], operations: ['pop'], nativeOperations: [], mutates: true, kinds: [], readsEachIteration: false, suppliedByScript: false, steps: [], ...extra })
  it('adds a table per table-step alias the seeds lack, up to four', () => {
    const existing = [{ alias: 'Work', columns: ['id'], rows: [{ values: { id: 'kept' } }] }]
    const result = sandboxSeedsForScript(existing, [
      usage('work'), usage('jobs', { columns: ['job_id'], kinds: ['native_queue'] }), usage('grid', { kinds: ['vts_columns'] }),
      usage('pool_only', { operations: [], readsEachIteration: true }), usage('four'), usage('five'),
    ])
    expect(result.seeds).toEqual([existing[0],
      { alias: 'jobs', profile: 'native_queue', columns: ['job_id'], rows: [] },
      { alias: 'grid', profile: 'vts_columns', columns: ['id'], rows: [] },
      { alias: 'four', profile: 'native_queue', columns: ['id'], rows: [] },
    ])
    expect([result.added, result.left]).toEqual([['jobs', 'grid', 'four'], ['five']])
  })
})

describe('a table step’s replay result', () => {
  it('reads the step’s row, metadata and absent cells from its variables', () => {
    const extractions = { 'row.status': 'ok', 'row.revision': '4', 'row.record_id': 'r1', 'row.record_version': '2', 'row.count': '1', 'row.values.id': 'a', 'row.values.note': '', other: 'x' }
    expect(liveTableStepResult(extractions, 'row')).toEqual({ prefix: 'row', status: 'ok', recordId: 'r1', version: '2', revision: '4', count: '1', value: undefined, row: { id: 'a', note: '' }, rows: undefined, cells: undefined, columns: undefined })
    // Without the step (the script changed), the prefix the variables show is used.
    expect(liveTableStepResult(extractions)?.prefix).toBe('row')
    expect(liveTableStepResult({ 'r.status': 'empty', 'r.revision': '1' }, 'r')).toMatchObject({ status: 'empty', row: undefined })
    expect(liveTableStepResult({}, 'row')).toBeNull()
  })

  it('explains a failure without offering to run an unknown outcome again', () => {
    expect(liveTableFailureNote('live table operation outcome is unknown')).toContain('could make the change twice')
    expect(liveTableFailureNote('live_table lease reference is missing or belongs to another table')).toContain('No lease is held')
    expect(liveTableFailureNote('live_table returned no available result')).toContain('Add starting rows')
    expect(liveTableFailureNote('HTTP 500')).toBeNull()
  })
})
