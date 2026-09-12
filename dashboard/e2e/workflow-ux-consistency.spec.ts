import { test, expect, seedScript } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

const themes = ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']
for (const theme of themes) for (const width of [923, 1440]) {
  test(`UX structure remains usable in ${theme} at ${width}px`, async ({ page, request }, testInfo) => {
    test.setTimeout(120_000)
    const workspace = await createWorkspace(request)
    await seedScript(request, workspace)
    await page.setViewportSize({ width, height: 1000 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto(`/#/settings?setting=preferences&${scopeQuery(workspace)}`)
    await page.getByRole('combobox', { name: 'Dashboard Theme', exact: true }).selectOption(theme)
    const routes = [['dashboard', 'Dashboard'], ['scripts', 'Script Library'], ['tests', 'Scenario Library'], ['new-test', 'New Test'], ['results', 'Results Library'], ['repository', 'Application Repository'], ['monitoring', 'Monitoring'], ['docs', 'Help & documentation'], ['access', 'Access'], ['settings', 'Settings'], ['command-center', 'Command Center']]
    for (const [route, titleText] of routes) {
      await page.goto(`/#/${route}?${scopeQuery(workspace)}`)
      await expect(page.locator('#app-page-title')).toHaveText(titleText)
      await expect(page.locator('.page-loading')).toHaveCount(0)
      if (route === 'command-center') await expect(page.getByRole('heading', {name: 'No active runs', exact: true})).toBeVisible()
      await page.waitForLoadState('networkidle')
      if (['scripts', 'tests', 'results', 'repository'].includes(route)) await expect(page.locator('.ux-library-toolbar')).toHaveCount(1)
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await expect(page.locator('.main-topbar')).toBeVisible()
      const title = await page.locator('#app-page-title').boundingBox()
      expect(title!.x + title!.width).toBeLessThanOrEqual(width)
      for (const toolbar of await page.locator('.ux-library-toolbar').all()) {
        const search = toolbar.locator('input[type="search"], input').first()
        if (await search.count()) await expect(search).toBeVisible()
        const filters = toolbar.locator('details').first()
        if (await filters.count()) {
          await filters.locator('summary').click()
          await expect(filters).toHaveAttribute('open', '')
          await filters.locator('summary').click()
          await expect(filters).not.toHaveAttribute('open', '')
        }
      }
      await page.screenshot({ path: testInfo.outputPath(`${theme}-${width}-${route}.png`), fullPage: false, animations: 'disabled' })
    }
  })
}
