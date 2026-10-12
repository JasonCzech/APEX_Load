import { describe, expect, it } from 'vitest'

import { filterAndSortRows } from './commandCenterTableSort'

describe('filterAndSortRows', () => {
  const rows = [
    { id: 'container', memory: 40 },
    { id: 'small', memoryMb: 60 },
    { id: 'large', memoryMb: 900 },
    { id: 'busy', memory: 85 },
  ]

  it('sorts memory by share, and generators reporting MB by MB among themselves', () => {
    expect(filterAndSortRows(rows, '', { key: 'memory', direction: 'asc' }).map(row => row.id)).toEqual(['small', 'large', 'container', 'busy'])
    expect(filterAndSortRows(rows, '', { key: 'memory', direction: 'desc' }).map(row => row.id)).toEqual(['busy', 'container', 'large', 'small'])
  })

  it('filters on any field', () => {
    expect(filterAndSortRows(rows, 'larg', { key: 'id', direction: 'asc' }).map(row => row.id)).toEqual(['large'])
  })
})
