import { describe, expect, it } from 'vitest'

describe('app shell layout', () => {
  it('keeps page feedback in flow and the topbar full width across app pages', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const appCss = readFileSync(`${cwd}/src/App.css`, 'utf8') as string

    expect(cssRule(appCss, '.main-topbar')).toContain('margin: -20px -36px 24px;')
    // The sticky offset must cancel .main-content's top padding or a strip of
    // scrolling content shows above the bar.
    expect(cssRule(appCss, '.main-topbar')).toContain('top: -20px;')
    expect(cssRule(appCss, '.main-topbar')).toContain('padding: 10px 36px;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('position: relative;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('margin: 0 0 20px;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('padding: 8px 0;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('position: relative;')
    expect(cssRule(appCss, '.main-topbar-feedback')).not.toContain('position: sticky;')
    expect(cssRule(appCss, '.main-topbar-feedback')).not.toContain('top: 57px;')
    expect(appCss).not.toContain(':has(.command-center-page) > .main-topbar')
    expect(appCss).not.toContain('margin: -20px 0 24px;')
    expect(appCss).not.toContain('padding: 10px 0;')
  })

  it('adds extra topbar spacing on primary navigation pages', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const appCss = readFileSync(`${cwd}/src/App.css`, 'utf8') as string

    expect(cssRule(appCss, '.main-content.page-dashboard > .main-topbar,\n.main-content.page-new-test > .main-topbar,\n.main-content.page-command-center > .main-topbar,\n.main-content.page-monitoring > .main-topbar,\n.main-content.page-fleet > .main-topbar,\n.main-content.page-docs > .main-topbar,\n.main-content.page-access > .main-topbar,\n.main-content.page-settings > .main-topbar')).toContain('margin-bottom: 30px;')
  })
})

describe('printing', () => {
  // File > Print… prints the page showing, in any theme: it uses the light
  // theme's colors (chart series included), and the report composer hides the
  // page behind it only while it is open.
  it('prints in the light palette and leaves pages alone outside the composer', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const read = (path: string) => readFileSync(`${cwd}/${path}`, 'utf8') as string
    const appCss = read('src/App.css')
    const composerCss = read('src/components/ReportComposer.css')

    const light = cssTokens(cssRule(read('src/index.css'), ":root[data-theme='light']"))
    const printed = cssTokens(cssRule(appCss.slice(appCss.indexOf('@media print {')), ':root[data-theme]'))
    const colors = ['--text-primary', '--text-secondary', '--text-muted', '--danger-text', '--warning-text', '--success-text']
    for (const name of [...colors, ...[1, 2, 3, 4, 5, 6, 7, 8].map(slot => `--chart-${slot}`)]) {
      expect(printed[name], name).toBe(light[name])
    }

    const composerPrint = composerCss.slice(composerCss.indexOf('@media print {'))
    expect(composerPrint).not.toMatch(/^\s*body \* \{/m)
    expect(composerPrint).toContain('body:has(> .report-composer-overlay) > :not(.report-composer-overlay) {')
  })
})

function cssTokens(rule: string): Record<string, string> {
  return Object.fromEntries(Array.from(rule.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g), match => [match[1], match[2].trim()]))
}

function cssRule(css: string, selector: string) {
  const start = css.indexOf(`${selector} {`)
  expect(start).toBeGreaterThanOrEqual(0)
  const end = css.indexOf('}', start)
  expect(end).toBeGreaterThan(start)
  return css.slice(start, end + 1)
}
