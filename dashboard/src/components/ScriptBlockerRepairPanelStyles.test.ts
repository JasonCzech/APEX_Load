import { describe, expect, it } from 'vitest'

async function readStylesheet(path: URL) {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  return readFileSync(path, 'utf8') as string
}

function cssRule(css: string, selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const matches = Array.from(css.matchAll(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, 'g')))
  return matches.map(match => match[1]).join('\n')
}

describe('Blocked-step repair panel styles', () => {
  it('keeps the empty status live region in the accessibility tree without adding a gap', async () => {
    const css = await readStylesheet(new URL('./ScriptEditor.css', import.meta.url))
    // A region removed while empty inserts itself and its notice in one
    // update, which screen readers often do not announce.
    const empty = cssRule(css, '.script-blocker-repair-status:empty')
    expect(empty).not.toMatch(/display:\s*none|visibility:\s*hidden/)
    // Out of the panel's grid, so the empty paragraph takes no row and no gap.
    expect(empty).toContain('position: absolute;')
    expect(empty).toContain('clip: rect(0, 0, 0, 0);')
  })
})
