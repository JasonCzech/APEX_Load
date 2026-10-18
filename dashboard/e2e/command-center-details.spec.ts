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
  // The run shelf keeps throughput in view, full or folded into its one-row form.
  await expect(cockpit.getByRole('region', { name: 'Command center run controls' })).toContainText('Requests/s')
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

test('live run keeps view, group, and graph controls in the right sidebar at desktop widths', async ({ page, seedLiveRun }, testInfo) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-right-sidebar' })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/command-center/${run.testId}`)
  const cockpit = page.locator('.command-center-page')
  const opsSidebar = cockpit.getByRole('complementary', { name: 'Command center sidebar', exact: true })
  const graphSidebar = cockpit.locator('.command-grafana-side')
  const graphSelection = graphSidebar.getByRole('region', { name: 'Group selection', exact: true })
  const graphOptions = graphSidebar.getByRole('region', { name: 'Graph display options', exact: true })

  const expectSidebarPosition = async (view: 'ops' | 'graphs', stacked: boolean) => {
    const sidebar = view === 'ops' ? opsSidebar : graphSidebar
    await expect(sidebar).toBeVisible()
    const viewSwitch = sidebar.getByRole('region', { name: 'Command center view mode', exact: true })
    await expect(viewSwitch.getByRole('button', { name: 'OPS View', exact: true })).toBeVisible()
    await expect(viewSwitch.getByRole('button', { name: 'Graph Wall', exact: true })).toBeVisible()
    await expect.poll(() => cockpit.evaluate((element, { view, stacked }) => {
      const layout = element.querySelector(view === 'ops' ? '.command-layout-ops' : '.command-grafana-grid')
      const primary = layout?.querySelector(view === 'ops' ? '.command-primary-column' : '.command-runtime-graphs')
      const rail = layout?.querySelector(view === 'ops' ? '.command-side-rail' : '.command-grafana-side')
      if (!layout || !primary || !rail) return null
      const mainBox = primary.getBoundingClientRect()
      const railBox = rail.getBoundingClientRect()
      const layoutBox = layout.getBoundingClientRect()
      return {
        positioned: stacked ? railBox.top >= mainBox.bottom : railBox.left >= mainBox.right,
        aligned: Math.abs(stacked ? railBox.left - mainBox.left : railBox.top - mainBox.top) <= 1,
        contained: railBox.right <= layoutBox.right + 1 && mainBox.width > 0 && railBox.width > 0,
      }
    }, { view, stacked }), { message: `${view} sidebar stays ${stacked ? 'below' : 'to the right of'} the main pane` }).toEqual({
      positioned: true,
      aligned: true,
      contained: true,
    })
    await expect.poll(() => page.locator('.main-content').evaluate(element => ({
      mainFits: element.scrollWidth <= element.clientWidth + 1,
      pageFits: document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    })), { message: `${view} workspace fits without page-level horizontal scrolling` }).toEqual({ mainFits: true, pageFits: true })
  }

  for (const width of [1440, 1280, 1024, 900]) {
    await test.step(`right sidebar at ${width}px`, async () => {
      await page.setViewportSize({ width, height: 1000 })
      await cockpit.getByRole('button', { name: 'OPS View', exact: true }).click()
      await expectSidebarPosition('ops', false)
      await opsSidebar.getByRole('button', { name: 'Graph Wall', exact: true }).click()
      await expectSidebarPosition('graphs', false)
      await expect(graphSelection).toContainText('1 / 1 groups visible')
      const groupVisibility = graphSelection.getByRole('button', { name: 'smoke graph visibility', exact: true })
      await groupVisibility.click()
      await expect(groupVisibility).toHaveAttribute('aria-pressed', 'false')
      await expect(graphSelection).toContainText('0 / 1 groups visible')
      await expect(cockpit.locator('.command-runtime-graphs')).toHaveAttribute('data-visible-groups', '')
      await groupVisibility.click()
      await expect(groupVisibility).toHaveAttribute('aria-pressed', 'true')
      await expect(cockpit.locator('.command-runtime-graphs')).toHaveAttribute('data-visible-groups', /\S/)
      await expect(graphOptions.getByRole('slider', { name: 'Graph display window', exact: true })).toBeVisible()
      await graphOptions.getByRole('button', { name: 'Seconds', exact: true }).click()
      await expect(graphOptions.getByRole('button', { name: 'Seconds', exact: true })).toHaveAttribute('aria-pressed', 'true')
      await graphOptions.getByRole('button', { name: 'Milliseconds', exact: true }).click()
      await expect(graphOptions.getByRole('button', { name: 'Milliseconds', exact: true })).toHaveAttribute('aria-pressed', 'true')
      if (width === 1280 || width === 900) {
        await page.locator('.main-content').evaluate(element => { element.scrollTop = 0 })
        await page.screenshot({ path: testInfo.outputPath(`graph-wall-right-sidebar-${width}.png`), fullPage: true })
      }
    })
  }

  for (const width of [820, 760]) {
    await test.step(`stacked sidebar at ${width}px`, async () => {
      await page.setViewportSize({ width, height: 1000 })
      await graphSidebar.getByRole('button', { name: 'OPS View', exact: true }).click()
      await expectSidebarPosition('ops', true)
      await opsSidebar.getByRole('button', { name: 'Graph Wall', exact: true }).click()
      await expectSidebarPosition('graphs', true)
      await expect(graphSelection).toContainText('1 / 1 groups visible')
      await expect(graphOptions.getByRole('slider', { name: 'Graph display window', exact: true })).toBeVisible()
    })
  }
})

test('graph layout paginates selected graphs and preserves its choice through responsive reflow', async ({ page, seedLiveRun }, testInfo) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-graph-layout' })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/command-center/${run.testId}`)
  const cockpit = page.locator('.command-center-page')
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  const graphOptions = cockpit.locator('.command-grafana-side').getByRole('region', { name: 'Graph display options', exact: true })
  const layout = graphOptions.getByRole('group', { name: 'Graph layout', exact: true })
  const grid = cockpit.locator('.command-runtime-graphs .charts-grid')
  const cards = grid.locator(':scope > [data-chart-id]')
  const pagination = cockpit.getByRole('navigation', { name: 'Graph pages', exact: true })
  const previousPage = pagination.getByRole('button', { name: 'Previous graph page', exact: true })
  const nextPage = pagination.getByRole('button', { name: 'Next graph page', exact: true })
  const readGraphIds = () => cards.evaluateAll(elements => elements.map(element => element.getAttribute('data-chart-id')))
  await layout.getByRole('button', { name: '3x3', exact: true }).click()
  await expect(cards).toHaveCount(7)
  const graphIds = await readGraphIds()
  expect(new Set(graphIds).size).toBe(7)
  const expectColumns = async (count: number) => {
    // Measure the rendered first row, not just the selected button or CSS class.
    await expect.poll(() => cards.evaluateAll(elements => {
      const boxes = elements.map(element => element.getBoundingClientRect())
      const firstRowTop = boxes[0]?.top
      return firstRowTop === undefined ? 0 : boxes.filter(box => Math.abs(box.top - firstRowTop) <= 1).length
    })).toBe(count)
  }

  for (const width of [1440, 1280]) {
    await test.step(`four or nine graphs per page at ${width}px`, async () => {
      await page.setViewportSize({ width, height: 1000 })
      await layout.getByRole('button', { name: '2x2', exact: true }).click()
      await expect(cards).toHaveCount(4)
      await expect(pagination).toContainText('Graphs 1–4 of 7 · Page 1 of 2')
      await expect(previousPage).toBeDisabled()
      await expect(nextPage).toBeEnabled()
      await expectColumns(2)
      const firstPageIds = await readGraphIds()
      expect(firstPageIds).toEqual(graphIds.slice(0, 4))
      if (width === 1280) {
        await grid.scrollIntoViewIfNeeded()
        await page.screenshot({ path: testInfo.outputPath('graph-wall-2x2-first-page-1280.png'), fullPage: true })
        await graphOptions.screenshot({ path: testInfo.outputPath('graph-options-2x2-1280.png') })
      }
      await nextPage.click()
      await expect(cards).toHaveCount(3)
      await expect(pagination).toContainText('Graphs 5–7 of 7 · Page 2 of 2')
      await expect(previousPage).toBeEnabled()
      await expect(nextPage).toBeDisabled()
      await expectColumns(2)
      const secondPageIds = await readGraphIds()
      expect(secondPageIds).toEqual(graphIds.slice(4))
      expect([...firstPageIds, ...secondPageIds]).toEqual(graphIds)
      expect(new Set([...firstPageIds, ...secondPageIds]).size).toBe(7)
      await previousPage.click()
      await expect(cards).toHaveCount(4)
      expect(await readGraphIds()).toEqual(firstPageIds)
      // Changing the layout from the second page must still reveal every graph.
      await nextPage.click()
      await expect(cards).toHaveCount(3)
      await layout.getByRole('button', { name: '3x3', exact: true }).click()
      await expect(cards).toHaveCount(7)
      await expect(pagination).toContainText('Graphs 1–7 of 7 · Page 1 of 1')
      await expect(previousPage).toBeDisabled()
      await expect(nextPage).toBeDisabled()
      expect(await readGraphIds()).toEqual(graphIds)
      await expectColumns(3)
    })
  }
  await grid.scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('graph-wall-3x3-1280.png'), fullPage: true })
  await graphOptions.screenshot({ path: testInfo.outputPath('graph-options-3x3-1280.png') })
  await page.reload()
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  await expect(layout.getByRole('button', { name: '3x3', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(cards).toHaveCount(7)
  expect(await readGraphIds()).toEqual(graphIds)
  await expectColumns(3)

  // Reflow columns within the graph pane without changing the chosen page size.
  for (const [width, columns] of [[1024, 2], [900, 1], [760, 2]]) {
    await page.setViewportSize({ width, height: 1000 })
    await expectColumns(columns)
    await expect(layout.getByRole('button', { name: '3x3', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await expect(cards).toHaveCount(7)
    expect(await readGraphIds()).toEqual(graphIds)
    await layout.getByRole('button', { name: '2x2', exact: true }).click()
    await expect(cards).toHaveCount(4)
    await expectColumns(Math.min(2, columns))
    await expect(pagination).toContainText('Graphs 1–4 of 7 · Page 1 of 2')
    expect(await readGraphIds()).toEqual(graphIds.slice(0, 4))
    await layout.getByRole('button', { name: '3x3', exact: true }).click()
    await expect(cards).toHaveCount(7)
  }
  await page.setViewportSize({ width: 1280, height: 1000 })
  await expectColumns(3)
  expect(await readGraphIds()).toEqual(graphIds)

  await test.step('keyboard reordering follows the moved graph across pages and retains focus', async () => {
    await layout.getByRole('button', { name: '2x2', exact: true }).click()
    await expect(cards).toHaveCount(4)
    const originalCardId = graphIds[0]
    const movedCard = grid.locator(`[data-chart-id="${originalCardId}"]`)
    const dragHandle = movedCard.getByRole('button', { name: /^Rearrange / })
    await dragHandle.focus()
    await page.keyboard.press('End')
    await expect(pagination).toContainText('Graphs 5–7 of 7 · Page 2 of 2')
    await expect(cards).toHaveCount(3)
    await expect(cards.last()).toHaveAttribute('data-chart-id', originalCardId!)
    await expect(dragHandle).toBeFocused()
    expect(await readGraphIds()).toEqual([...graphIds.slice(1), originalCardId].slice(4))
    await page.keyboard.press('Home')
    await expect(pagination).toContainText('Graphs 1–4 of 7 · Page 1 of 2')
    await expect(cards).toHaveCount(4)
    await expect(cards.first()).toHaveAttribute('data-chart-id', originalCardId!)
    await expect(dragHandle).toBeFocused()
    expect(await readGraphIds()).toEqual(graphIds.slice(0, 4))
    await layout.getByRole('button', { name: '3x3', exact: true }).click()
    await expect(cards).toHaveCount(7)
    expect(await readGraphIds()).toEqual(graphIds)
  })
})

test('monitoring graph tabs reflect confirmed configuration and stay stable during refresh failures', async ({ page, seedLiveRun }) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-monitoring-tab-availability' })
  const now = Date.now()
  const generatedAt = new Date(now).toISOString()
  const values = [1, 2].map((value, index) => ({
    timestamp: new Date(now - (1 - index) * 1000).toISOString(),
    timestamp_ms: now - (1 - index) * 1000,
    value,
  }))
  const configuredResponses = {
    prometheus: {
      enabled: true,
      configured: true,
      generated_at: generatedAt,
      graphs: [{ id: 'fixture_prometheus_cpu', title: 'Fixture Prometheus CPU', unit: '%', series: [{ key: 'cpu', label: 'CPU', values }] }],
    },
    dashboards: {
      enabled: true,
      configured: true,
      generated_at: generatedAt,
      dashboards: [{ dashboard_id: 'fixture-dashboard', dashboard_title: 'Fixture dashboard', panels: [{ panel_id: 1, title: 'Fixture dashboard CPU', kind: 'line', unit: '%', series: [{ key: 'cpu', label: 'CPU', values }] }] }],
    },
  }
  const disabledResponses = {
    prometheus: { enabled: false, configured: false, generated_at: generatedAt, graphs: [] },
    dashboards: { enabled: false, configured: false, generated_at: generatedAt, dashboards: [] },
  }
  type ResponseMode = 'disabled' | 'configured' | 'error'
  const phases: Array<{ release: () => void }> = []
  const createPhase = (mode: ResponseMode) => {
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    const phase = { mode, gate, release, requested: new Set<string>() }
    phases.push(phase)
    return phase
  }
  let currentPhase = createPhase('disabled')
  const sources = ['dashboards', 'prometheus'] as const
  await page.clock.install({ time: new Date(now) })
  await page.route(/\/api\/v1\/monitoring\/(prometheus|dashboards)\/graphs(?:\?.*)?$/, async route => {
    const source = new URL(route.request().url()).pathname.includes('/prometheus/') ? 'prometheus' : 'dashboards'
    const phase = currentPhase
    phase.requested.add(source)
    await phase.gate
    await route.fulfill(phase.mode === 'error'
      ? { status: 503, json: { error: `${source} graph refresh unavailable` } }
      : { json: phase.mode === 'configured' ? configuredResponses[source] : disabledResponses[source] })
  })
  const waitForPendingRequests = async () => {
    await expect.poll(() => [...currentPhase.requested].sort()).toEqual(sources)
    // Flush the hooks' loading updates while both source responses remain gated.
    await page.clock.runFor(100)
  }
  const finishPhase = async () => {
    const responses = sources.map(source => page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/monitoring/${source}/graphs`))
    currentPhase.release()
    await Promise.all((await Promise.all(responses)).map(response => response.finished()))
    await page.clock.runFor(100)
  }
  const beginRefresh = async (mode: ResponseMode) => {
    currentPhase = createPhase(mode)
    await page.clock.fastForward(10_001)
    await waitForPendingRequests()
  }
  try {
    await page.goto(`/#/command-center/${run.testId}`)
    const cockpit = page.locator('.command-center-page')
    await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
    const tabs = cockpit.getByRole('tablist', { name: 'Graph groups', exact: true })
    const prometheusTab = tabs.getByRole('tab', { name: /^Prometheus/ })
    const dashboardsTab = tabs.getByRole('tab', { name: /^Dashboards/ })
    const expectMonitoringTabsAbsent = async () => {
      await expect(prometheusTab).toHaveCount(0)
      await expect(dashboardsTab).toHaveCount(0)
    }
    const expectMonitoringTabsPresent = async () => {
      await expect(prometheusTab).toBeVisible()
      await expect(dashboardsTab).toBeVisible()
    }

    await waitForPendingRequests()
    await expectMonitoringTabsAbsent()
    await finishPhase()
    await expectMonitoringTabsAbsent()
    await beginRefresh('disabled')
    await expectMonitoringTabsAbsent()
    await finishPhase()
    await expectMonitoringTabsAbsent()
    await beginRefresh('error')
    await expectMonitoringTabsAbsent()
    await finishPhase()
    await expectMonitoringTabsAbsent()

    await beginRefresh('configured')
    await expectMonitoringTabsAbsent()
    await finishPhase()
    await expectMonitoringTabsPresent()
    await prometheusTab.click()
    await expect(cockpit.getByRole('heading', { name: 'Fixture Prometheus CPU', exact: true })).toBeVisible()
    await dashboardsTab.click()
    await expect(cockpit.getByRole('heading', { name: 'Fixture dashboard · Fixture dashboard CPU', exact: true })).toBeVisible()

    await beginRefresh('configured')
    await expectMonitoringTabsPresent()
    await expect(dashboardsTab).toHaveAttribute('aria-selected', 'true')
    await finishPhase()
    await expectMonitoringTabsPresent()
    await beginRefresh('error')
    await expectMonitoringTabsPresent()
    await finishPhase()
    await expectMonitoringTabsPresent()
    await expect(dashboardsTab).toHaveAttribute('aria-selected', 'true')
    await expect(cockpit.getByText('Imported dashboards unavailable', { exact: true })).toBeVisible()

    await beginRefresh('disabled')
    await expectMonitoringTabsPresent()
    await finishPhase()
    await expectMonitoringTabsAbsent()
    await expect(tabs.getByRole('tab', { name: /^Live Metrics/ })).toHaveAttribute('aria-selected', 'true')
  } finally {
    phases.forEach(phase => phase.release())
    await page.unrouteAll({ behavior: 'wait' })
  }
})

test('live network phase graphs connect sparse samples without extending into empty edges', async ({ page, seedLiveRun }, testInfo) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-continuous-network-phases' })
  const sampleStart = Date.now() - 6000
  const history = Array.from({ length: 7 }, (_, index) => ({
    timestamp: new Date(sampleStart + index * 1000).toISOString(),
    active_vusers: 2,
    tps: 2,
    request_rate: 2,
    total_transactions: (index + 1) * 2,
    total_errors: 0,
    avg_response_ms: 50,
    error_pct: 0,
    p50_ms: 40,
    p90_ms: 55,
    p95_ms: 60,
    p99_ms: 65,
    // Include both whole-stack gaps and a missing DNS observation while the
    // other phases are present. Neither kind of absence is a measured zero.
    ...(index === 1 || index === 3 || index === 5 ? {
      ...(index === 3 ? {} : { avg_dns_ms: 20 }),
      avg_connect_ms: 30,
      avg_tls_ms: 40,
      avg_ttfb_ms: 50,
      avg_wait_ms: 60,
    } : {}),
  }))
  await page.route(url => url.pathname === `/api/v1/tests/${run.testId}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    await route.fulfill({ response, json: { ...payload, history, live_metrics: history[history.length - 1] } })
  })
  // Keep the real run alive, while holding this page's metric stream open so
  // later engine samples cannot replace the deliberately sparse API history.
  await page.routeWebSocket(url => url.pathname === `/api/v1/tests/${run.testId}/stream`, socket => {
    socket.onMessage(() => undefined)
  })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/#/command-center/${run.testId}`)
  const cockpit = page.locator('.command-center-page')
  await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  await cockpit.getByRole('tab', { name: /^Infrastructure/ }).click()
  const graphOptions = cockpit.getByRole('region', { name: 'Graph display options', exact: true })
  await graphOptions.getByRole('button', { name: '3x3', exact: true }).click()
  await graphOptions.getByRole('button', { name: 'Milliseconds', exact: true }).click()
  await graphOptions.getByRole('checkbox', { name: 'Show table rows beneath graphs', exact: true }).check()
  const chart = cockpit.locator('[data-chart-id="tx_network_phases"]')
  const phaseCurves = chart.locator('.recharts-area-curve')
  await expect(phaseCurves).toHaveCount(5)
  const expectConnectedPaths = async (selector: string, count: number) => {
    await expect.poll(() => chart.evaluate((element, { selector, count }) => {
      const grid = element.querySelector<SVGGraphicsElement>('.recharts-cartesian-grid')
      const paths = Array.from(element.querySelectorAll<SVGPathElement>(selector))
      if (!grid || paths.length !== count) return false
      const plot = grid.getBBox()
      return plot.width > 0 && paths.every(path => {
        const bounds = path.getBBox()
        const commands = path.getAttribute('d') || ''
        return (commands.match(/M/gi) || []).length === 1
          && path.getTotalLength() > 0
          && bounds.width > plot.width * 0.2
          && bounds.x > plot.x + plot.width * 0.05
          && bounds.x + bounds.width < plot.x + plot.width * 0.95
      })
    }, { selector, count }), { message: 'Measured samples form one continuous path with unmeasured edges left empty' }).toBe(true)
  }
  await expectConnectedPaths('.recharts-area-curve', 5)
  await expectConnectedPaths('.recharts-area-area', 5)
  // Each phase is constant where observed. Missing DNS must neither dip its
  // own stroke to zero nor lower the cumulative baseline of the other phases.
  await expect.poll(() => phaseCurves.evaluateAll(paths => paths.length === 5
    && paths.every(path => (path as SVGPathElement).getBBox().height <= 1)),
  { message: 'All phase strokes stay level across the missing DNS observation' }).toBe(true)
  // Drawing across a missing interval must not add zero-valued measurements.
  const dnsRow = chart.locator('.chart-metrics-table tbody tr').filter({ hasText: 'DNS' })
  await expect(dnsRow.locator('td').nth(3)).toHaveText('20ms')
  await expect(dnsRow.locator('td').nth(4)).toHaveText('20ms')
  const highlight = chart.getByRole('button', { name: 'DNS', exact: true })
  await highlight.click()
  await expect(highlight).toHaveAttribute('aria-pressed', 'true')
  await expectConnectedPaths('.recharts-line-curve[stroke-width="4"]', 1)
  await chart.screenshot({ path: testInfo.outputPath('continuous-network-phases.png') })
})
