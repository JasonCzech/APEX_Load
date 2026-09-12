import { test, expect } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

for (const width of [390, 1440]) {
  test(`peak capacity remains distinct from live utilization at ${width}px`, async ({ page, request }, testInfo) => {
    const workspace = await createWorkspace(request)
    await page.setViewportSize({ width, height: 1000 })
    await page.route('**/api/v1/system/inventory', async route => {
      const response = await route.fetch()
      const inventory = await response.json()
      const budget = { cpu_cores: 6.4, memory_mb: 12800, network_mbps: 800, vusers: 8000, protocols: { soap: 4000 } }
      const half = { cpu_cores: 3.2, memory_mb: 6400, network_mbps: 400, vusers: 4000, protocols: { soap: 2000 } }
      inventory.generators = [{
        target: 'generator-0:9090', generator_id: 'generator-0', status: 'connected',
        available_capacity: 9900, active_vusers: 100, cpu_percent: 12, memory_bytes: 100 * 1024 * 1024, goroutine_count: 150,
        cpu_limit_cores: 8, memory_limit_bytes: 16000 * 1024 * 1024,
        pod_capacity: { state: 'ready', model_version: 'peak_capacity_v1', incarnation: 'browser-fixture',
          headroom_pct: 20, schedulable: budget, reserved: half, remaining: half,
          reserved_pct: 50, remaining_pct: 50, confidence: 'estimated', dedicated: false },
      }]
      await route.fulfill({ response, json: inventory })
    })
    await page.goto(`/#/monitoring?category=fleet&${scopeQuery(workspace)}`)
    await page.getByRole('tab', { name: 'Fleet', exact: true }).click()
    await page.getByRole('button', { name: 'Open generator inventory', exact: true }).click()
    await expect(page.getByText('50% reserved / 50% remaining', { exact: true })).toBeVisible()
    await expect(page.getByText('20% headroom excluded', { exact: true })).toBeVisible()
    await expect(page.getByRole('progressbar', { name: 'Reserved peak capacity' })).toHaveAttribute('value', '50')
    await expect(page.getByRole('progressbar', { name: 'Reserved peak capacity' })).toBeInViewport()
    await expect(page.getByText('12.0%', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`pod-capacity-${width}.png`), fullPage: true, animations: 'disabled' })
  })
}
