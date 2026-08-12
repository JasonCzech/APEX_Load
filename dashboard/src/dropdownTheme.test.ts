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
    const disabledSelectRule = css.match(/\nselect:disabled \{([\s\S]*?)\n\}/)?.[1] || ''
    const solarizedDarkBlock = css.match(/:root\[data-theme='solarized-dark'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const monokaiBlock = css.match(/:root\[data-theme='monokai-dimmed'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const lightBlock = css.match(/:root\[data-theme='light'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const solarizedLightBlock = css.match(/:root\[data-theme='solarized-light'\] \{([\s\S]*?)\n\}/)?.[1] || ''
    const baseSelectPickerRules = Array.from(css.matchAll(/select:not\(\[multiple\]\)::picker\(select\) \{([\s\S]*?)\n {2}\}/g))
    const baseSelectPickerRule = baseSelectPickerRules[baseSelectPickerRules.length - 1]?.[1] || ''

    expect(rootBlock).toContain('--dropdown-menu-bg: var(--bg-secondary);')
    expect(rootBlock).toContain('--select-bg: var(--bg-secondary);')
    expect(rootBlock).toContain('--select-menu-bg: var(--dropdown-menu-bg);')
    expect(rootBlock).toContain('color-scheme: dark;')
    expect(selectRule).toContain('color-scheme: inherit;')
    expect(selectRule).toContain('border: 1px solid var(--border);')
    expect(selectRule).toContain('background-color: var(--select-bg) !important;')
    expect(selectRule).toContain('color: var(--text-primary) !important;')
    expect(plainSelectArrowRule).toContain('appearance: none;')
    expect(plainSelectArrowRule).toContain('background-image:')
    expect(selectMenuRule).toContain('background-color: var(--select-menu-bg);')
    expect(selectMenuRule).toContain('color: var(--text-primary);')
    expect(disabledSelectRule).toContain('color: var(--text-muted) !important;')
    expect(css).toContain('@supports (appearance: base-select)')
    expect(baseSelectPickerRule).toContain('background: var(--dropdown-menu-bg);')
    expect(baseSelectPickerRule).toContain('box-shadow: var(--shadow-md);')
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

  it('uses the shared opaque dropdown surface for application-owned menus', async () => {
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const menuRules = [
      ['src/components/Sidebar.css', '.project-menu'],
      ['src/components/Pages.css', '.dashboard-workspace-tab-menu'],
      ['src/components/ScriptEditor.css', '.action-parameter-menu'],
      ['src/components/ScriptEditor.css', '.debugger-settings-popover'],
      ['src/components/TestExecution.css', '.chart-context-menu'],
      ['src/components/ClientDownloadsMenu.css', '.client-downloads-panel'],
      ['src/components/BuildBadge.css', '.build-badge-popover'],
      ['src/components/CommandPalette.css', '.command-palette'],
    ] as const

    for (const [path, selector] of menuRules) {
      const css = readFileSync(path, 'utf8') as string
      const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
      const rule = css.match(new RegExp(`${escapedSelector} \\{([\\s\\S]*?)\\n\\}`))?.[1] || ''
      expect(rule, selector).toContain('var(--dropdown-menu-bg')
      expect(rule, selector).not.toContain('transparent')
    }
  })
})
