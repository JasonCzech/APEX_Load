import { describe, expect, it } from 'vitest'

import { autoAllocatesUniqueRows } from './uniqueRowAllocation'

describe('autoAllocatesUniqueRows', () => {
  it.each([
    ['unique list without settings', { type: 'list', selection: 'unique' }, true],
    ['unique file without settings', { type: 'file', selection: 'unique' }, true],
    ['explicit auto_allocate', { type: 'list', selection: 'unique', auto_allocate: true }, true],
    ['explicit block size', { type: 'list', selection: 'unique', block_size: 3 }, false],
    ['one value per virtual user', { type: 'list', selection: 'unique', value_for_each_vuser: true }, false],
    ['unique counter', { type: 'counter', selection: 'unique', block_size: 5 }, false],
    ['sequential list', { type: 'list', selection: 'sequential' }, false],
  ])('%s', (_name, parameter, expected) => {
    expect(autoAllocatesUniqueRows(parameter)).toBe(expected)
  })
})
