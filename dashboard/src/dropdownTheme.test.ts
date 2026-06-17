import { describe, expect, it } from 'vitest'

describe('dropdown theme styling', () => {
  it('uses theme tokens for native select controls and option menus', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8') as string

    const rootBlock = css.match(/:root \{([\s\S]*?)\n\}/)?.[1] || ''
    const selectRule = css.match(/\nselect \{([\s\S]*?)\n\}/)?.[1] || ''
    const plainSelectArrowRule = css.match(/\nselect:not\(\[multiple\]\):not\(\[class\]\) \{([\s\S]*?)\n\}/)?.[1] || ''
    const selectMenuRule = css.match(/\nselect option,\nselect optgroup \{([\s\S]*?)\n\}/)?.[1] || ''
    const solarizedDarkBlock = css.match(/:root\[data-theme='solarized-dark'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const monokaiBlock = css.match(/:root\[data-theme='monokai-dimmed'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const lightBlock = css.match(/:root\[data-theme='light'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const solarizedLightBlock = css.match(/:root\[data-theme='solarized-light'\] \{([\s\S]*?)\n\}/)?.[1] || ''

    expect(rootBlock).toContain('--select-bg: var(--bg-secondary);')
    expect(rootBlock).toContain('--select-menu-bg: var(--bg-elevated);')
    expect(rootBlock).toContain('color-scheme: dark;')
    expect(selectRule).toContain('color-scheme: inherit;')
    expect(selectRule).toContain('border: 1px solid var(--border);')
    expect(selectRule).toContain('background-color: var(--select-bg);')
    expect(selectRule).toContain('color: var(--text-primary);')
    expect(plainSelectArrowRule).toContain('appearance: none;')
    expect(plainSelectArrowRule).toContain('background-image:')
    expect(selectMenuRule).toContain('background-color: var(--select-menu-bg);')
    expect(selectMenuRule).toContain('color: var(--text-primary);')
    expect(solarizedDarkBlock).toContain('color-scheme: dark;')
    expect(monokaiBlock).toContain('color-scheme: dark;')
    expect(lightBlock).toContain('color-scheme: light;')
    expect(solarizedLightBlock).toContain('color-scheme: light;')
  })

  it('does not leave dropdown backgrounds on the undefined legacy bg token', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const pagesCss = readFileSync(new URL('./components/Pages.css', import.meta.url), 'utf8') as string
    const executionCss = readFileSync(new URL('./components/TestExecution.css', import.meta.url), 'utf8') as string

    expect(pagesCss).not.toContain('var(--bg)')
    expect(executionCss).not.toContain('var(--bg)')
  })
})
