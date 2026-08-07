import { describe, expect, it } from 'vitest'

async function readStylesheet(path: URL): Promise<string> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  return readFileSync(path, 'utf8')
}

function ruleBody(stylesheet: string, pattern: RegExp): string {
  return stylesheet.match(pattern)?.[1] || ''
}

describe('Test Execution chart axis styles', () => {
  it('shows a focus ring on the focusable y-axis tick instead of suppressing the outline', async () => {
    const css = await readStylesheet(new URL('./TestExecution.css', import.meta.url))
    const focusRule = ruleBody(css, /\.chart-y-axis-click-target:focus-visible \{([\s\S]*?)\n\}/)

    expect(focusRule).toContain('outline: 2px solid var(--accent);')
    expect(focusRule).not.toContain('outline: none;')
  })

  it('does not hang the focus affordance off the recharts axis line, which is not a tick descendant', async () => {
    const css = await readStylesheet(new URL('./TestExecution.css', import.meta.url))

    expect(css).not.toContain('.chart-y-axis-click-target:focus-visible .recharts-cartesian-axis-line')
  })
})
