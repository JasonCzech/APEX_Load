import { describe, expect, it } from 'vitest'

// UI labels follow the restyle's sentence case. Older screens still carry Title
// Case copies of labels that read in sentence case elsewhere; this ratchet keeps
// that debt from growing. Lower the budget whenever a group is normalized.
const CASE_VARIANT_GROUP_BUDGET = 249

const sources = import.meta.glob<string>(
  ['./**/*.{ts,tsx}', '!./**/*.test.{ts,tsx}', '!./types/generated/**', '!./test/**'],
  { query: '?raw', import: 'default', eager: true },
)
// Bundled guides render inside Help and quote UI labels in bold.
const guides = import.meta.glob<string>(
  ['../../docs/APPLICATION_USER_GUIDE.md', '../../docs/ADMIN_USER_GUIDE.md', '../../docs/SCRIPT_DEBUGGER_GUIDE.md', '../../docs/HELP_CENTER.md'],
  { query: '?raw', import: 'default', eager: true },
)

const LITERAL = /'([^'\n]{2,48})'|"([^"\n]{2,48})"|>([^<>{}\n]{2,48})</g
const LABEL_LIKE = /^[A-Z][A-Za-z0-9&/\-'’ .,()…]*$/

function uiLabels(): Map<string, Set<string>> {
  const byLowercase = new Map<string, Set<string>>()
  for (const source of Object.values(sources)) {
    for (const match of source.matchAll(LITERAL)) {
      const value = (match[1] ?? match[2] ?? match[3] ?? '').trim()
      const words = value.split(/\s+/).length
      if (!LABEL_LIKE.test(value) || words < 2 || words > 6) continue
      const key = value.toLowerCase()
      const variants = byLowercase.get(key) ?? new Set<string>()
      variants.add(value)
      byLowercase.set(key, variants)
    }
  }
  return byLowercase
}

describe('label casing', () => {
  const labels = uiLabels()

  it('does not add labels that differ from an existing label only by case', () => {
    const groups = [...labels.values()].filter(variants => variants.size > 1)
    expect(groups.length, groups.slice(0, 20).map(variants => [...variants].join(' | ')).join('\n')).toBeLessThanOrEqual(CASE_VARIANT_GROUP_BUDGET)
  })

  it('quotes UI labels in the bundled guides with the casing the UI uses', () => {
    const offenders: string[] = []
    for (const [path, guide] of Object.entries(guides)) {
      for (const match of guide.matchAll(/\*\*([^*\n]{2,60})\*\*/g)) {
        const quoted = match[1].trim().replace(/[.:]$/, '')
        const variants = labels.get(quoted.toLowerCase())
        if (!variants || variants.has(quoted)) continue
        // Flag a guide still quoting Title Case once the UI reads in sentence
        // case; lower-case prose emphasis and glossary terms are not quotes.
        const capitals = (text: string) => (text.match(/[A-Z]/g) || []).length
        if ([...variants].some(variant => capitals(variant) < capitals(quoted))) offenders.push(`${path.split('/').pop()}: "${quoted}" (UI: ${[...variants].join(' / ')})`)
      }
    }
    expect(offenders).toEqual([])
  })
})
