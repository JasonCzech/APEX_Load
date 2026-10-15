import { describe, expect, it } from 'vitest'

// Search and filter boxes turn browser autofill off: the desktop web view
// offered remembered entries over them, covering the rows being filtered. This
// keeps every new search box doing the same.
const sources = import.meta.glob<string>(
  ['./**/*.tsx', '!./**/*.test.tsx'],
  { query: '?raw', import: 'default', eager: true },
)

// An <input ...> element up to its closing "/>" (props may hold arrow
// functions, so a bare ">" does not end it).
const INPUT_ELEMENT = /<input\b[\s\S]*?\/>/g
const SEARCH_TYPE = /\btype=(?:"search"|'search'|\{\s*["']search["']\s*\})/

function searchInputsWithoutAutocomplete(): string[] {
  const missing: string[] = []
  for (const [path, source] of Object.entries(sources)) {
    for (const match of source.matchAll(INPUT_ELEMENT)) {
      if (!SEARCH_TYPE.test(match[0]) || match[0].includes('autoComplete=')) continue
      missing.push(`${path}:${source.slice(0, match.index).split('\n').length}`)
    }
  }
  return missing
}

describe('search inputs', () => {
  it('turn browser autofill off', () => {
    expect(searchInputsWithoutAutocomplete()).toEqual([])
  })

  it('finds search inputs however the type is written', () => {
    const inputs = [`<input type='search' value={q} />`, `<input\n  type={"search"}\n  onChange={e => set(e.target.value)}\n/>`, `<input type="search" autoComplete="off" />`, `<input type="text" />`]
    expect(inputs.filter(input => [...input.matchAll(INPUT_ELEMENT)].some(match => SEARCH_TYPE.test(match[0]) && !match[0].includes('autoComplete=')))).toHaveLength(2)
  })
})
