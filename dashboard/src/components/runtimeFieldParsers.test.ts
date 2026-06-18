import { describe, expect, it } from 'vitest'

import { formatKeyValues, formatList, parseKeyValues, parseList } from './runtimeFieldParsers'

describe('runtimeFieldParsers', () => {
  it('parses comma- and newline-separated lists', () => {
    expect(parseList('a, b ,c')).toEqual(['a', 'b', 'c'])
    expect(parseList('a\nb\n c ')).toEqual(['a', 'b', 'c'])
    expect(parseList('  ,  ')).toEqual([])
  })

  it('formats lists for display', () => {
    expect(formatList(['a', 'b'])).toBe('a, b')
    expect(formatList([])).toBe('')
    expect(formatList(undefined)).toBe('')
  })

  it('round-trips key=value maps', () => {
    const map = parseKeyValues('k=v\n x = y \nbad-line\n=novalue')
    expect(map).toEqual({ k: 'v', x: 'y' }) // 'bad-line' (no =) and '=novalue' (empty key) dropped
    expect(formatKeyValues({ k: 'v', x: 'y' })).toBe('k=v\nx=y')
    expect(formatKeyValues(undefined)).toBe('')
  })
})
