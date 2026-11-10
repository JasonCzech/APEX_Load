import { describe, expect, it } from 'vitest'

/** The z-index a rule sets, from a CSS file. */
function zIndex(css: string, selector: string): number {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const match = css.match(new RegExp(`(?:^|[}\\s,])${escaped}\\s*\\{[^}]*?z-index:\\s*(\\d+)`))
  if (!match) throw new Error(`no z-index for ${selector}`)
  return Number(match[1])
}

describe('waterfall tooltip layer', () => {
  it('paints above the overlays a waterfall is mounted in', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8') as string
    const tooltip = zIndex(read('./Waterfall.css'), '.wf-tooltip')
    // The baseline comparison drawer portals to body like the tooltip does.
    expect(tooltip).toBeGreaterThan(zIndex(read('../baselineComparison/BaselineComparisonWorkspace.css'), '.bc-overlay'))
    // The run report drawers' modal layer.
    expect(tooltip).toBeGreaterThan(zIndex(read('../Modal.css'), '.modal-overlay'))
  })
})
