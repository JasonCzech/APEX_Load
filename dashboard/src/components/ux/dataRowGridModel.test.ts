import { describe, expect, it } from 'vitest'
import { isTabularText, newColumnError, orderedGridColumns, parseTabularPaste, rowsToCSV } from './dataRowGridModel'

describe('dataRowGridModel', () => {
  it('orders columns by the stored order, then any other used column alphabetically', () => {
    const rows: Record<string, string>[] = [{ email: 'a', zip: '1', id: 'x' }, { region: 'east' }]
    expect(orderedGridColumns(['zip', 'missing', 'id', 'zip'], rows)).toEqual(['zip', 'id', 'email', 'region'])
    expect(orderedGridColumns(['zip', 'missing'], rows, true)).toEqual(['zip', 'missing', 'email', 'id', 'region'])
    expect(orderedGridColumns(undefined, [])).toEqual([])
  })

  it('explains why a column name cannot be added', () => {
    expect(newColumnError('  ', ['a'])).toBe('Enter a column name.')
    expect(newColumnError(' a ', ['a'])).toBe('There is already a column named "a".')
    expect(newColumnError('A', ['a'])).toBe('')
    expect(newColumnError('b', ['a'], 1)).toBe('This table can have at most 1 columns.')
    expect(newColumnError('customer id', [])).toContain('Use only letters, numbers, underscores and hyphens')
    expect(newColumnError('order-no_2', [])).toBe('')
  })

  it('treats only multi-cell text as a spreadsheet paste', () => {
    expect(isTabularText('C-1')).toBe(false)
    expect(isTabularText('C-1\n')).toBe(false)
    expect(isTabularText('C-1\tC-2')).toBe(true)
    expect(isTabularText('C-1\r\nC-2')).toBe(true)
  })

  it('fills columns from the pasted cell, or by a header that names them', () => {
    expect(parseTabularPaste('a1\tb1\nA2\tB2\n', ['x', 'a', 'b'], 1)).toEqual({ rows: [{ a: 'a1', b: 'b1' }, { a: 'A2', b: 'B2' }], usedHeader: false, droppedValues: 0, unknownColumns: [] })
    expect(parseTabularPaste('b\ta\r\n2\t1\r\n\r\n', ['a', 'b'])).toEqual({ rows: [{ b: '2', a: '1' }], usedHeader: true, droppedValues: 0, unknownColumns: [] })
    // A single line is data even if it matches the column names.
    expect(parseTabularPaste('a\tb', ['a', 'b'])).toEqual({ rows: [{ a: 'a', b: 'b' }], usedHeader: false, droppedValues: 0, unknownColumns: [] })
    // Values past the last column are counted, empty ones kept as empty.
    expect(parseTabularPaste('1\t\t3\t4', ['a', 'b'], 0)).toEqual({ rows: [{ a: '1', b: '' }], usedHeader: false, droppedValues: 2, unknownColumns: [] })
    // A header naming a column the table lacks is reported, not pasted as data.
    expect(parseTabularPaste('a\tphone\n1\t555', ['a', 'b', 'c'])).toEqual({ rows: [], usedHeader: true, droppedValues: 0, unknownColumns: ['phone'] })
    // One data value equal to a column name doesn't make the line a header.
    expect(parseTabularPaste('east\tstatus\tx-1\nwest\tok\tx-2', ['region', 'status', 'code'])).toMatchObject({ usedHeader: false, unknownColumns: [], rows: [{ region: 'east', status: 'status', code: 'x-1' }, { region: 'west', status: 'ok', code: 'x-2' }] })
  })

  it('writes rows as CSV with quoting where needed', () => {
    expect(rowsToCSV(['id', 'note'], [{ id: '1', note: 'a, "b"' }, { id: ' 2' }])).toBe('id,note\r\n1,"a, ""b"""\r\n" 2",\r\n')
  })
})
