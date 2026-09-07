import { expect, test } from './support/liveRun'

// The old five-tile hero was replaced by the run control shelf. Verify the
// shipped summary, real nonzero readings and readable labels at both widths.
test('[U12 U20] live run summary shows readable averaged throughput and current/target load', async ({ page, seedLiveRun }) => {
  test.setTimeout(150_000)
  const run = await seedLiveRun({ label: 'command-center-hero-metrics', readyTimeoutMs: 90_000 })
  await page.goto(`/#/command-center/${run.testId}`)
  const shelf = page.getByRole('region', { name: 'Command center run controls' })
  await expect(shelf).toBeVisible()
  const summary = shelf.locator('[aria-label="Run summary"]')
  await expect(summary.locator('.command-metric-pill > span')).toHaveText([
    'Current / Target Load', 'Remaining', 'TPS (10s avg)', 'Overall Error Rate',
  ])
  const tps = summary.locator('.command-metric-pill').filter({ hasText: 'TPS (10s avg)' }).locator('strong')
  await expect(tps).toHaveText(/^[\d,]+\.\d$/)
  await expect.poll(async () => Number((await tps.innerText()).replace(/,/g, ''))).toBeGreaterThan(0)
  const load = summary.locator('.command-metric-pill').filter({ hasText: 'Current / Target Load' }).locator('strong')
  await expect(load).toHaveText('2 / 2 VU')
  for (const width of [1281, 1280, 932]) {
    await page.setViewportSize({ width, height: 900 })
    await expect.poll(() => summary.locator('.command-metric-pill > span').evaluateAll(labels =>
      labels.every(label => label.scrollWidth <= label.clientWidth && label.scrollHeight <= label.clientHeight)),
    { message: `Run summary labels remain readable at ${width}px` }).toBe(true)
    for (const name of ['Pause', 'Stop', 'Abort']) await expect(shelf.getByRole('button', { name, exact: true })).toBeVisible()
  }
})
