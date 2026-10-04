import { test, expect } from './support/fixtures'
import { studio, startRecording, stopRecording, preview, verifyOutputs, saveRecording, editor, editSource, pairAgent, type Script } from './support/journey'
import type { Locator, Page } from '@playwright/test'

interface EnvironmentReceipt {
  version: number
  observation_source: string
  engine: string
  device_profile?: string
  profile_browser_family?: string
  emulated: boolean
  applied_context?: { viewport_width?: number; viewport_height?: number; device_scale_factor?: number; mobile?: boolean; has_touch?: boolean }
  physical_device_qualification: string
}

const presets = [
  { key: 'ipad_air', label: 'ipad air (ipados)', family: 'webkit', width: 820, height: 1180, scale: 2 },
  { key: 'pixel_8', label: 'pixel 8 (android)', family: 'chromium', width: 412, height: 915, scale: 2.625 },
] as const

function receipt(script: Script): EnvironmentReceipt {
  return script.metadata?.import?.preserved?.recording_environment as EnvironmentReceipt
}

async function assertPanel(panel: Locator, configured?: string) {
  await expect(panel).toBeVisible()
  await expect(panel).toContainText('Recorded engine')
  await expect(panel).toContainText('Chromium')
  await expect(panel).toContainText('Applied mobile mode')
  await expect(panel).toContainText('Applied touch mode')
  await expect(panel).toContainText('Physical Chrome/iPad qualification')
  await expect(panel).toContainText('Unverified')
  if (configured) await expect(panel).toContainText(configured)
}

async function capturePanel(page: Page, panel: Locator, path: string) {
  await panel.getByRole('heading').first().evaluate(element => {
    element.scrollIntoView({ block: 'center', inline: 'nearest' })
    for (let parent = element.parentElement; parent; parent = parent.parentElement) parent.scrollLeft = 0
    window.scrollTo({ left: 0, top: window.scrollY })
  })
  const bounds = await panel.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.x).toBeGreaterThanOrEqual(0)
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1)
  expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path })
}

// Actual desktop Chromium recording via the controller and local agent. These
// tests qualify applied emulation and retained receipts, not a physical iPad.
for (const launch of ['controller', 'agent'] as const) {
  test.describe(launch, () => {
    test.use({ agentMode: launch === 'agent' })
    for (const preset of presets) {
      test(`${launch}: ${preset.key} recording engine receipt survives dual-output save and replay review`, async ({ page, harness: h }, info) => {
        test.setTimeout(300_000)
        await studio(page, h)
        if (launch === 'agent') await pairAgent(page, h, info)
        await expect(page.getByLabel('Emulated device preset', { exact: true })).toBeVisible()
        const rec = await startRecording(page, h, launch === 'agent', { mobile: preset.label })
        try {
          const live = rec.session.recording_environment as EnvironmentReceipt
          expect(live.observation_source).toBe('local_recorder')
          expect(live).toMatchObject({ version: 1, engine: 'chromium', device_profile: preset.key, profile_browser_family: preset.family, emulated: true, physical_device_qualification: 'unverified', applied_context: { viewport_width: preset.width, viewport_height: preset.height, device_scale_factor: preset.scale, mobile: true, has_touch: true } })
          // CDP does not retain the Go-owned Playwright viewport option. With
          // no separate Screen option, the applied viewport determines the
          // observed screen size; mobile CSS layout width may differ.
          expect(await rec.target.evaluate(() => ({ width: screen.width, height: screen.height }))).toEqual({ width: preset.width, height: preset.height })
          expect(await rec.target.evaluate(() => window.devicePixelRatio)).toBe(preset.scale)
          expect(await rec.target.evaluate(() => navigator.maxTouchPoints)).toBeGreaterThan(0)
          const livePanel = page.getByTestId('browser-recorder-panel').getByRole('region', { name: 'Recording environment', exact: true })
          await assertPanel(livePanel)
          await expect(livePanel).toContainText(launch === 'agent' ? 'Recording agent reported' : 'Local recorder reported')
          if (launch === 'agent') await expect(livePanel).toContainText('The controller did not independently observe the agent browser runtime.')
          await rec.target.getByLabel('Customer', { exact: true }).fill('environment-seed')
          await rec.target.getByRole('button', { name: 'Record event', exact: true }).click()
          await expect.poll(() => h.target.events.length).toBe(1)
          await stopRecording(page)
          await page.getByLabel('Also create a protocol-level HTTP script from the recorded HAR', { exact: true }).check()
          const converted = await preview(page, info)
          expect(converted.scripts).toHaveLength(2)
          const scripts = converted.scripts!
          expect(scripts.map(script => script.protocol)).toEqual(['browser', 'http'])
          for (const script of scripts) {
            // Preview is served by the recording process itself, including
            // the agent; the review UI qualifies that channel separately.
            expect(receipt(script)).toEqual(live)
          }
          expect(scripts[0].config.browser.engine).toBe(preset.family)
          const output = page.getByRole('region', { name: `Output 1: ${scripts[0].name}`, exact: true })
          const reviewed = output.getByRole('region', { name: 'Recording environment', exact: true })
          await assertPanel(reviewed, preset.family === 'webkit' ? 'WebKit' : 'Chromium')
          await expect(reviewed).toContainText(launch === 'agent' ? 'Recording agent reported' : 'Local recorder reported')
          if (preset.family === 'webkit') await expect(reviewed).toContainText('differs')
          // The matching Chromium control is actually replayed. A successful
          // draft verification must not upgrade physical-device qualification.
          if (preset.family === 'chromium') {
            await verifyOutputs(page, 2)
            await expect(reviewed).toContainText('Unverified')
          }
          await capturePanel(page, reviewed, info.outputPath('recording-environment-desktop.png'))
          await page.setViewportSize({ width: 760, height: 1000 })
          await capturePanel(page, reviewed, info.outputPath('recording-environment-narrow.png'))
          await page.setViewportSize({ width: 1600, height: 1050 })
          const ids = await saveRecording(page, preset.family === 'chromium')
          expect(ids).toHaveLength(2)
          const savedObservationSource = launch === 'agent' ? 'agent_reported' : 'local_recorder'
          const savedReceipt = (script: Script) => ({ ...receipt(script), observation_source: savedObservationSource })
          let storedBrowser: Script | undefined
          for (const id of ids) {
            const response = await h.request.get(`/api/v1/scripts/${id}`)
            expect(response.ok(), await response.text()).toBeTruthy()
            const script = (await response.json()).script as Script
            if (script.protocol === 'browser') storedBrowser = script
            // The controller normalizes agent claims at the handoff before
            // attestation/save; every observed context value is retained.
            expect(receipt(script)).toEqual(savedReceipt(scripts.find(item => item.protocol === script.protocol)!))
            await editor(page, h, id)
            await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
            const savedPanel = page.getByRole('region', { name: 'Recording environment', exact: true })
            await assertPanel(savedPanel)
            await expect(savedPanel).toContainText(launch === 'agent' ? 'Recording agent reported' : 'Local recorder reported')
          }
          // Editing configured replay intent cannot rewrite the recording
          // receipt. Remove the preset before selecting the other engine.
          await editor(page, h, ids[0])
          expect(storedBrowser).toBeDefined()
          const edited = structuredClone(storedBrowser!)
          edited.config.browser.engine = preset.family === 'webkit' ? 'chromium' : 'webkit'
          edited.config.browser.device_profile = ''
          await editSource(page, edited)
          await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
          const storedReview = page.getByRole('region', { name: 'Recording environment', exact: true })
          await assertPanel(storedReview, edited.config.browser.engine === 'webkit' ? 'WebKit' : 'Chromium')
          await page.reload()
          await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
          await assertPanel(page.getByRole('region', { name: 'Recording environment', exact: true }))
          const refreshed = await h.request.get(`/api/v1/scripts/${ids[0]}`)
          expect(refreshed.ok(), await refreshed.text()).toBeTruthy()
          expect(receipt((await refreshed.json()).script)).toEqual(savedReceipt(scripts[0]))
        } finally { await rec.browser.close().catch(() => {}) }
      })
    }
  })
}
