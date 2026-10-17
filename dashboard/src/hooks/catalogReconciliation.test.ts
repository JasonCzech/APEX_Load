import { describe, expect, it } from 'vitest'
import { catalogSnapshotCaughtUp, reconcileCommittedCatalog } from './catalogReconciliation'

type Row = { id: string; scope: string; name: string; revision: string }
const row = (id: string, name = id, revision = '2026-01-02T00:00:00Z', scope = 'a'): Row => ({ id, name, revision, scope })
const equal = (left: Row, right: Row) => JSON.stringify(left) === JSON.stringify(right)
const acknowledged = (listed: Row, committed: Row) => catalogSnapshotCaughtUp(listed, committed, equal, value => value.revision)
const policy = { identity: (value: Row) => value.id, belongsToScope: (value: Row) => value.scope === 'a', acknowledged, tombstones: new Set<string>() }

describe('catalog reconciliation', () => {
  it('retains authoritative rows over stale reads and includes missing committed rows without mutating inputs', () => {
    const renamed = row('one', 'Renamed')
    const created = row('two', 'Created')
    const pending = new Map([['one', renamed], ['two', created]])
    const listed = [row('one', 'Old', '2026-01-01T00:00:00Z')]
    const result = reconcileCommittedCatalog(listed, pending, policy)
    expect(result.rows).toEqual([renamed, created])
    expect(result.committedById).not.toBe(pending)
    expect(pending.size).toBe(2)
    expect(listed[0].name).toBe('Old')
  })

  it.each([
    ['equivalent acknowledgement', row('one'), true],
    ['newer revision', row('one', 'Newest', '2026-01-03T00:00:00Z'), true],
    ['equal revision with different content', row('one', 'Different'), false],
    ['older revision', row('one', 'Old', '2026-01-01T00:00:00Z'), false],
    ['invalid revision', row('one', 'Invalid', 'unknown'), false],
  ])('handles %s', (_label, listed, caughtUp) => {
    const committed = row('one')
    const pending = new Map([['one', committed]])
    const result = reconcileCommittedCatalog([listed], pending, policy)
    expect(result.rows).toEqual([caughtUp ? listed : committed])
    expect(result.committedById.has('one')).toBe(!caughtUp)
    expect(pending.has('one')).toBe(true)
  })

  it('keeps deletion tombstones over both a stale list and pending mutations', () => {
    const value = row('one')
    const result = reconcileCommittedCatalog([value], new Map([['one', value]]), { ...policy, tombstones: new Set(['one']) })
    expect(result.rows).toEqual([])
  })

  it('keeps pending mutations for other scopes without publishing or acknowledging them', () => {
    const foreign = row('foreign', 'Foreign', '2026-01-02T00:00:00Z', 'b')
    const pending = new Map([['foreign', foreign]])
    const first = reconcileCommittedCatalog([foreign], pending, policy)
    expect(first.rows).toEqual([])
    expect(first.committedById.get('foreign')).toBe(foreign)
    const second = reconcileCommittedCatalog([], first.committedById, { ...policy, belongsToScope: value => value.scope === 'b' })
    expect(second.rows).toEqual([foreign])
  })
})
