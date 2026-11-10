import { describe, expect, it } from 'vitest'

describe('AnatomyDiffSection styles', () => {
  it('frames the reused statistic switcher outside the run report anatomy scopes', async () => {
    // The drawer is not inside .run-report-time-anatomy, so --rr-anatomy-rule
    // is undefined there: every border of .rr-anatomy-stat needs a fallback.
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(new URL('../runReport/TimeAnatomy.css', import.meta.url), 'utf8') as string
    const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].filter(rule => rule[1].includes('.rr-anatomy-stat'))
    const borders = rules.flatMap(rule => rule[2].match(/border[\w-]*:\s*[^;]*var\(--rr-anatomy-rule[^;]*/g) || [])
    expect(borders.length).toBeGreaterThanOrEqual(2)
    for (const border of borders) expect(border).toMatch(/var\(--rr-anatomy-rule,\s*var\(--border\)\)/)
  })
})
