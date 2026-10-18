import { pathToFileURL } from 'node:url'
import { assertNodeVersion } from './check-node-version.mjs'

// Launch the default headless browser, rather than checking executablePath():
// Playwright uses a separate headless shell and also needs its system libraries.
export async function checkE2EBrowser(loadChromium = async () => (await import('@playwright/test')).chromium) {
  let chromium
  try {
    chromium = await loadChromium()
  } catch (cause) {
    throw new Error('Playwright is unavailable. Install dashboard dependencies: (cd dashboard && npm ci)', { cause })
  }
  try {
    const browser = await chromium.launch({ headless: true, timeout: 15_000 })
    await browser.close()
  } catch (cause) {
    throw new Error('The pinned Playwright Chromium browser could not start. Install it with: (cd dashboard && npx playwright install chromium). On Linux, install system dependencies with npx playwright install --with-deps chromium.', { cause })
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    assertNodeVersion()
    await checkE2EBrowser()
  } catch (error) {
    console.error(`ERROR: ${error.message}`)
    if (error.cause) console.error(error.cause.message)
    process.exitCode = 1
  }
}
