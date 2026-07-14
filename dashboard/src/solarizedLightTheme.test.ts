import { describe, expect, it } from 'vitest'

describe('Solarized Light theme tokens', () => {
  it('keeps the accent family warm instead of blue-led', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8')
    const block = css.match(/:root\[data-theme='solarized-light'\] \{([\s\S]*?)\n\}/)?.[1] || ''

    expect(block).toContain('--accent: #5f6f00;')
    expect(block).toContain('--info: #657b83;')
    expect(block).toContain('--chart-1: #b58900;')
    expect(block).not.toContain('#268bd2')
  })
})
