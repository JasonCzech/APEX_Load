import { describe, expect, it } from 'vitest'

describe('app shell layout', () => {
  it('keeps the sticky page banner full width across app pages', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const appCss = readFileSync(`${cwd}/src/App.css`, 'utf8') as string

    expect(cssRule(appCss, '.main-topbar')).toContain('margin: -20px -36px 24px;')
    expect(cssRule(appCss, '.main-topbar')).toContain('padding: 10px 36px;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('margin: -20px 0 20px;')
    expect(cssRule(appCss, '.main-topbar-feedback')).toContain('padding: 8px 0;')
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

function cssRule(css: string, selector: string) {
  const start = css.indexOf(`${selector} {`)
  expect(start).toBeGreaterThanOrEqual(0)
  const end = css.indexOf('}', start)
  expect(end).toBeGreaterThan(start)
  return css.slice(start, end + 1)
}
