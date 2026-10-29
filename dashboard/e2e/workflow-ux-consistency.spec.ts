import { test, expect, seedScript } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('narrow dev sidebar returns keyboard focus to the brand when collapsed', async ({ page }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'dev-admin', 'requires an unauthenticated dev sidebar')
  await page.setViewportSize({ width: 760, height: 1000 })
  await page.goto('/#/dashboard')
  const sidebar = page.locator('aside.sidebar')
  await expect(sidebar.locator('.sidebar-rail-avatar')).toHaveCount(0)
  const expand = page.getByRole('button', { name: 'Expand sidebar', exact: true })
  await expect(expand).toBeVisible()
  await expand.focus()
  await page.keyboard.press('Enter')
  const preview = page.getByRole('button', { name: /^Preview permissions/ })
  await expect(preview).toBeVisible()
  await preview.focus()
  await page.keyboard.press('Enter')
  const panel = page.locator('#sidebar-dev-view-panel')
  await expect(panel).toBeVisible()
  const admin = panel.getByRole('button', { name: 'Admin', exact: true })
  const editor = panel.getByRole('button', { name: 'Editor', exact: true })
  const [panelBox, adminBox, editorBox] = await Promise.all([panel.boundingBox(), admin.boundingBox(), editor.boundingBox()])
  expect(panelBox).not.toBeNull()
  expect(adminBox).not.toBeNull()
  expect(editorBox).not.toBeNull()
  if (!panelBox || !adminBox || !editorBox) throw new Error('Permission menu is not laid out')
  expect(panelBox.width).toBeGreaterThanOrEqual(160)
  expect(panelBox.x).toBeGreaterThanOrEqual(0)
  expect(panelBox.x + panelBox.width).toBeLessThanOrEqual(760)
  expect(adminBox.width).toBeGreaterThanOrEqual(60)
  expect(editorBox.width).toBeGreaterThanOrEqual(60)
  expect(adminBox.x + adminBox.width).toBeLessThanOrEqual(editorBox.x)
  await editor.focus()
  await page.keyboard.press('Enter')
  await expect(editor).toHaveAttribute('aria-pressed', 'true')
  await admin.focus()
  await page.keyboard.press('Enter')
  await expect(admin).toHaveAttribute('aria-pressed', 'true')
  await preview.focus()
  await page.keyboard.press('Enter')
  await expect(panel).toBeHidden()
  await expect(preview).toBeFocused()
  const collapse = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
  await expect(collapse).toBeVisible()
  await collapse.focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'APEX Load dashboard', exact: true })).toBeFocused()
})

test('narrow rail preserves focus when delayed preview identity replaces its controls', async ({ page }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'dev-admin', 'requires an unauthenticated dev sidebar')
  await page.setViewportSize({ width: 760, height: 1000 })
  await page.goto('/#/dashboard')
  let releaseEditorAuth: () => void = () => {}
  const editorAuthGate = new Promise<void>(resolve => { releaseEditorAuth = resolve })
  await page.route('**/api/v1/auth/me', async route => {
    if (route.request().headers()['x-apexload-dev-view'] === 'user') await editorAuthGate
    await route.continue()
  })
  try {
    const expand = page.getByRole('button', { name: 'Expand sidebar', exact: true })
    await expand.focus()
    await page.keyboard.press('Enter')
    const preview = page.getByRole('button', { name: /^Preview permissions/ })
    await preview.focus()
    await page.keyboard.press('Enter')
    const editorRequest = page.waitForRequest(request => request.url().includes('/api/v1/auth/me') && request.headers()['x-apexload-dev-view'] === 'user')
    await page.locator('#sidebar-dev-view-panel').getByRole('button', { name: 'Editor', exact: true }).click()
    await editorRequest
    const collapse = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
    await collapse.focus()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('button', { name: 'APEX Load dashboard', exact: true })).toBeFocused()
    await expand.focus()
    await expect(expand).toBeFocused()
    releaseEditorAuth()
    const avatar = page.getByRole('button', { name: /^Open account controls for / })
    await expect(avatar).toBeFocused()
    await page.evaluate(() => {
      localStorage.setItem('apexload.devViewMode', 'admin')
      window.dispatchEvent(new Event('apexload:dev-view-mode-changed'))
    })
    await expect(expand).toBeFocused()
  } finally {
    releaseEditorAuth()
    await page.unroute('**/api/v1/auth/me')
  }
})

test('desktop preview identity removal restores focus from the rail and account card', async ({ page }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'dev-admin', 'requires dev preview mode')
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.addInitScript(() => localStorage.setItem('apexload.devViewMode', 'user'))
  await page.goto('/#/dashboard')
  const avatar = page.getByRole('button', { name: /^Open account controls for / })
  await expect(avatar).toBeVisible()
  await avatar.focus()
  const setPreview = async (mode: 'admin' | 'user') => page.evaluate(value => {
    localStorage.setItem('apexload.devViewMode', value)
    window.dispatchEvent(new Event('apexload:dev-view-mode-changed'))
  }, mode)
  await setPreview('admin')
  const expand = page.getByRole('button', { name: 'Expand sidebar', exact: true })
  await expect(expand).toBeFocused()
  await setPreview('user')
  await expect(avatar).toBeVisible()
  await avatar.click()
  const account = page.locator('#sidebar-identity')
  await expect.poll(() => account.evaluate(element => element.contains(document.activeElement))).toBe(true)
  await setPreview('admin')
  await expect(page.getByRole('button', { name: 'Collapse sidebar', exact: true })).toBeFocused()
})

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
    // Each sidebar destination is titled with its sidebar name.
    const routes = [['dashboard', 'Dashboard'], ['scripts', 'Scripts'], ['tests', 'Scenarios'], ['new-test', 'New run'], ['results', 'Results'], ['repository', 'Repository'], ['monitoring', 'Monitoring'], ['docs', 'Help & documentation'], ['access', 'Access'], ['settings', 'Settings'], ['command-center', 'Command Center']]
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
