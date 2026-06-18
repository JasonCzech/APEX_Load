import { describe, expect, it } from 'vitest'

import { formatKeyValues, formatList, inheritedPlaceholder, parseKeyValues, parseList } from './runtimeFieldParsers'

describe('runtimeFieldParsers', () => {
  it('renders inherited values as placeholders, falling back when unset', () => {
    // Concrete inherited values are stringified for the placeholder.
    expect(inheritedPlaceholder(30000)).toBe('30000')
    expect(inheritedPlaceholder('mozilla/5.0')).toBe('mozilla/5.0')
    // 0 and false are real inherited values, not "unset".
    expect(inheritedPlaceholder(0)).toBe('0')
    expect(inheritedPlaceholder(false)).toBe('false')
    // undefined/null/'' mean nothing is inherited → the fallback label.
    expect(inheritedPlaceholder(undefined)).toBe('Inherit')
    expect(inheritedPlaceholder(null)).toBe('Inherit')
    expect(inheritedPlaceholder('')).toBe('Inherit')
    expect(inheritedPlaceholder(undefined, 'Duration-driven')).toBe('Duration-driven')
  })

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
