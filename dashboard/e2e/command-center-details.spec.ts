import { expect, test as liveRunTest, startLiveRun, abortLiveRun, type LiveRun, type StartLiveRunOptions } from './support/liveRun'
import { login } from './support/workflow'

// Keep a private, fixture-owned API context usable during timeout teardown,
// while sharing the real local-auth session established through the dashboard.
const test = liveRunTest.extend({
  seedLiveRun: async ({ page, playwright, baseURL }, provide) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const context = await playwright.request.newContext({ baseURL, storageState: await page.context().storageState() })
    const seeded: LiveRun[] = []
    try {
      await provide(async (options: StartLiveRunOptions = {}) => {
        const run = await startLiveRun(context, baseURL, options)
        seeded.push(run)
        return run
      })
    } finally {
      try {
        for (const run of seeded.reverse()) await abortLiveRun(context, run)
      } finally {
        await context.dispose()
      }
    }
  },
})

test('advanced details returns to the same run without losing cockpit edits or graph mode', async ({ page, seedLiveRun }) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-details-return' })
  await page.goto(`/#/command-center/${run.testId}`)
  const cockpit = page.locator('.command-center-page')
  const target = cockpit.getByRole('spinbutton', { name: 'smoke target VUsers', exact: true })
  await target.fill('5')
  await cockpit.getByRole('searchbox', { name: 'Filter command center table rows' }).fill('smoke')
  await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/test/${run.testId}\\?`))
  await expect(page.getByRole('tab', { name: /^Run state/ })).toHaveAttribute('aria-selected', 'true')
  await expect(cockpit).toBeHidden()
  await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/command-center/${run.testId}`))
  await expect(target).toHaveValue('5')
  await expect(cockpit.getByRole('searchbox')).toHaveValue('smoke')
  await expect(cockpit.getByRole('button', { name: 'Advanced run details', exact: true })).toBeFocused()
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  await page.reload()
  await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/command-center/${run.testId}`))
  // A reload has no in-memory draft to restore, but must still return to this run.
  await expect(cockpit).toBeVisible()
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  await expect(cockpit.getByRole('button', { name: 'Graph Wall', exact: true })).toHaveAttribute('aria-pressed', 'true')
})

test('runtime workspace keeps essential metrics readable and settings in an opaque drawer', async ({ page, seedLiveRun }, testInfo) => {
  const run = await seedLiveRun({ label: 'command-clean-interface' })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/command-center/${run.testId}`)
  const cockpit = page.locator('.command-center-page')
  const scenario = cockpit.getByRole('tabpanel', { name: 'Groups' })
  await expect(scenario.getByRole('columnheader')).toHaveCount(6)
  await scenario.getByRole('checkbox', { name: 'Show all 15 metrics' }).check()
  await expect(scenario.getByRole('columnheader')).toHaveCount(15)
  await cockpit.getByRole('searchbox').fill('smoke')
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  await expect(cockpit.getByRole('button', { name: 'Adjust run', exact: true })).toBeVisible()
  await expect(cockpit.locator('.command-run-stats')).toContainText('Requests/s')
  await cockpit.getByRole('button', { name: 'Configure Graphs', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'Graph configuration', exact: true })
  await expect(drawer).toBeVisible()
  await expect(drawer).toContainText('Graph Cards')
  const graphDisplayOptions = cockpit.getByRole('region', { name: 'Graph display options', exact: true })
  await expect(graphDisplayOptions).toContainText('Display Window')
  await expect(graphDisplayOptions).toContainText('Reset Graphs')
  expect(await drawer.evaluate(element => getComputedStyle(element).backgroundColor)).toMatch(/^rgb\(/)
  await page.screenshot({ path: testInfo.outputPath('graph-configuration.png'), fullPage: true })
  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(cockpit.getByRole('button', { name: 'Configure Graphs', exact: true })).toBeFocused()
  await cockpit.getByRole('button', { name: 'OPS View', exact: true }).click()
  await expect(cockpit.getByRole('searchbox')).toHaveValue('smoke')
  await expect(scenario.getByRole('checkbox', { name: 'Show all 15 metrics' })).toBeChecked()
  await scenario.getByRole('checkbox', { name: 'Show all 15 metrics' }).uncheck()
  await cockpit.getByRole('tab', { name: 'Error Details', exact: true }).click()
  await expect(cockpit.getByRole('tabpanel', { name: 'Error details', exact: true })).toContainText('No errors recorded.')
  await cockpit.getByRole('tab', { name: 'Groups', exact: true }).click()
  await page.locator('.main-content').evaluate(element => { element.scrollTop = 400 })
  const header = await page.locator('.main-topbar').boundingBox()
  const shelf = await cockpit.locator('.command-control-shelf').boundingBox()
  expect(shelf!.y).toBeGreaterThanOrEqual(header!.y + header!.height - 1)
  await page.screenshot({ path: testInfo.outputPath('operations-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 900, height: 1000 })
  // ResizeObserver publishes the new scroll offsets after viewport resize
  // returns. Scrolling before that callback uses the desktop shelf height.
  await expect.poll(async () => cockpit.evaluate(element => {
    const scroller = element.closest<HTMLElement>('.main-content')
    const header = scroller?.querySelector<HTMLElement>('.main-topbar')
    const shelf = element.querySelector<HTMLElement>('.command-control-shelf')
    if (!scroller || !header || !shelf) return null
    const style = getComputedStyle(element)
    return {
      header: parseFloat(style.getPropertyValue('--command-header-offset')) - header.offsetHeight,
      shelf: parseFloat(style.getPropertyValue('--command-shelf-height')) - shelf.offsetHeight,
      inset: parseFloat(style.getPropertyValue('--command-scroll-inset')) - parseFloat(getComputedStyle(scroller).paddingTop),
    }
  }), { timeout: 15_000, message: 'Command Center scroll offsets match the narrow viewport layout' }).toEqual({ header: 0, shelf: 0, inset: 0 })
  const currentLoad = cockpit.locator('.command-group-row [data-label="Current VUsers"]').first()
  expect(await currentLoad.evaluate(element => getComputedStyle(element, '::before').content)).toBe('"Current VUsers"')
  const groupActions = cockpit.getByRole('button', { name: 'smoke group actions', exact: true })
  await groupActions.evaluate(element => element.scrollIntoView({ block: 'start' }))
  await groupActions.focus()
  await expect.poll(async () => groupActions.evaluate(element => {
    const shelf = element.closest('.command-center-page')?.querySelector('.command-control-shelf')
    if (!shelf) throw new Error('Command Center run controls are missing')
    const narrowShelf = shelf.getBoundingClientRect()
    const actionBox = element.getBoundingClientRect()
    return actionBox.y - (narrowShelf.y + narrowShelf.height - 1)
  }), { timeout: 15_000, message: 'Narrow group actions remain below the sticky run controls' }).toBeGreaterThanOrEqual(0)
  await page.screenshot({ path: testInfo.outputPath('operations-narrow.png'), fullPage: true })
})
