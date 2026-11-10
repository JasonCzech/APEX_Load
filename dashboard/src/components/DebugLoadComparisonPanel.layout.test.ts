import { describe, expect, it } from 'vitest'

describe('Debug vs load table layout', () => {
  it('sizes all nine of its columns itself instead of inheriting the transaction table widths', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(new URL('./DebugCompletionSummary.css', import.meta.url), 'utf8') as string
    const widths = new Map<number, number>()
    for (const rule of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      const width = rule[2].match(/(?:^|;)\s*width:\s*([\d.]+)%/)
      for (const selector of width ? rule[1].split(',') : []) {
        const m = selector.trim().match(/^\.debug-load-table th:(?:first-child|nth-child\((\d+)\))$/)
        if (m) widths.set(m[1] ? Number(m[1]) : 1, Number(width![1]))
      }
    }
    expect([...widths.keys()].sort((a, b) => a - b)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9])
    expect([...widths.values()].reduce((a, b) => a + b, 0)).toBe(100)
    // No nowrap on the whole "debug → load" string: it overflowed the fixed cells.
    expect(css).not.toMatch(/\.debug-load-values\s*\{[^}]*nowrap/)
  })

  it('keeps the request and "Compared with" cells breakable so long names stay inside them', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(new URL('./DebugCompletionSummary.css', import.meta.url), 'utf8') as string
    for (const rule of css.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
      if (!/overflow-wrap:\s*normal/.test(rule[2])) continue
      for (const selector of rule[1].split(',').map(item => item.replace(/\/\*[\s\S]*?\*\//g, '').trim())) {
        if (!selector.startsWith('.debug-load-table')) continue
        // Only the numeric columns (3 and up) may stop breaking anywhere.
        expect(selector).toMatch(/td:nth-child\(n\+3\)$/)
      }
    }
  })
})
