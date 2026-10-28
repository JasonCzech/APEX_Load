import { test, expect, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

for (const theme of ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']) {
  test(`shared controls follow ${theme} including focus and help`, async ({ page }, testInfo) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    const scope = scopeQuery(workspace)
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(`/#/settings?setting=preferences&${scope}`)
    // Seeding after login happens outside React; reload its workspace catalog.
    await page.reload()
    await page.getByRole('combobox', { name: 'Dashboard Theme', exact: true }).selectOption(theme)
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    const themeSelect = page.getByRole('combobox', { name: 'Dashboard color theme', exact: true })
    await themeSelect.focus()
    await expect(themeSelect).toHaveCSS('outline-style', 'solid')
    await expect(themeSelect).toHaveCSS('outline-width', '2px')
    const nav = page.getByRole('button', { name: 'Scripts', exact: true })
    await nav.focus()
    const tooltip = page.getByRole('tooltip', { name: 'Scripts', exact: true })
    await expect(tooltip).toBeVisible()
    await expect(nav).not.toHaveAttribute('title')
    const colors = await tooltip.evaluate(element => {
      const style = getComputedStyle(element)
      const root = getComputedStyle(document.documentElement)
      return { background: style.backgroundColor, expected: root.getPropertyValue('--bg-primary').trim(), text: style.color, expectedText: root.getPropertyValue('--text-primary').trim() }
    })
    // Minified builds shorten tokens such as #ffffff to #fff.
    const rgb = (hex: string) => {
      const digits = hex.slice(1)
      const full = digits.length === 3 ? digits.split('').map(digit => digit + digit).join('') : digits
      return `rgb(${full.match(/../g)!.map(v => parseInt(v, 16)).join(', ')})`
    }
    expect(colors.background).toBe(rgb(colors.expected))
    expect(colors.text).toBe(rgb(colors.expectedText))
    await page.keyboard.press('Escape')
    await expect(tooltip).toHaveCount(0)
    await nav.hover()
    await expect(tooltip).toBeVisible()
    await page.keyboard.press('Escape')
    // The sidebar starts as the icon rail; expanded, labels are visible and the
    // popup is suppressed. Escape leaves the sidebar expanded.
    await page.getByRole('button', { name: 'Expand sidebar', exact: true }).click()
    await nav.focus()
    await expect(tooltip).toHaveCount(0)
    expect(await nav.evaluate(element => getComputedStyle(element, '::after').content)).toBe('none')
    await page.keyboard.press('Escape')
    await expect(page.getByRole('button', { name: 'Collapse sidebar', exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Collapse sidebar', exact: true }).click()
    await page.goto(`/#/settings?setting=platform&${scope}`)
    const checkbox = page.getByRole('checkbox', { name: 'Desktop Client Promotion', exact: true })
    await expect(checkbox).toBeVisible()
    const accent = await checkbox.evaluate(element => ({ actual: getComputedStyle(element).accentColor, expected: getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() }))
    expect(accent.actual).toBe(rgb(accent.expected))
    await expect(checkbox).toHaveCSS('width', '16px')
    await page.goto(`/#/access?setting=audit&${scope}`)
    const from = page.getByLabel('From (local time)', { exact: true })
    await expect(from).toBeVisible()
    // Themed, not the browser default: the field follows the theme surface token.
    const field = await from.evaluate(element => ({ background: getComputedStyle(element).backgroundColor, expected: getComputedStyle(document.documentElement).getPropertyValue('--bg-secondary').trim() }))
    expect(field.background).toBe(rgb(field.expected))
    await expect(from).not.toHaveCSS('border-top-color', 'rgb(118, 118, 118)')
    expect(await from.evaluate(element => element.getBoundingClientRect().height)).toBeGreaterThanOrEqual(34)
    await from.fill('2026-09-01T09:30')
    await expect(from).toHaveValue('2026-09-01T09:30')
    await expect(page.getByText('Audit actor type', { exact: true })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath(`${theme}-audit-controls.png`), fullPage: true })
    await page.goto(`/#/results?${scope}`)
    await page.getByRole('button', { name: 'Create a test', exact: true }).click()
    await expect(page.locator('#app-page-title')).toHaveText('New run')
    await page.goto(`/#/tests?view=builder&${scope}`)
    const copy = page.getByRole('button', { name: /^Copy existing scenario/ })
    await expect(copy).toBeDisabled()
    const idleColor = await copy.evaluate(element => getComputedStyle(element).backgroundColor)
    await copy.hover()
    await expect(copy).toHaveCSS('background-color', idleColor)
    await expect(copy).toHaveAttribute('aria-pressed', 'false')
    await page.goto(`/#/scripts?${scope}`)
    await expect(page.getByRole('button', { name: 'Create your first script', exact: true })).toBeVisible()
    await expect(page.locator('.scripts-library-table')).toHaveCount(0)
    // Field labels use sentence case in every theme (shared style; themes only change colour).
    await expect(page.getByText('Assigned application', { exact: true })).toHaveCSS('text-transform', 'none')
    await expect(page.getByText('Assigned release', { exact: true })).toHaveCSS('text-transform', 'none')

  })
}
