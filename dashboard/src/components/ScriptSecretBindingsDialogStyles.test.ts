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

describe('Secret Bindings dialog layout', () => {
  it('keeps the shared-value checkbox compact and left aligned', async () => {
    const css = await readStylesheet(new URL('./ScriptSecretBindingsDialog.css', import.meta.url))
    const acknowledgement = cssRule(css, '.script-secret-impact-acknowledgement')
    const checkbox = cssRule(css, ".script-secret-impact-acknowledgement input[type='checkbox']")

    expect(css).toContain('.script-secret-value-control > input,')
    expect(acknowledgement).toContain('justify-content: flex-start;')
    expect(acknowledgement).toContain('justify-self: start;')
    expect(acknowledgement).toContain('text-align: left;')
    expect(checkbox).toContain('flex: 0 0 auto;')
    expect(checkbox).toContain('width: auto;')
  })
})
