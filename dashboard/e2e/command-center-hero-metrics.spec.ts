import type { Locator } from '@playwright/test'

import { expect, test } from './support/liveRun'

// This spec used to page.setContent() a hand-written copy of the hero strip
// markup plus the component stylesheet. That fixture passed even when the
// dashboard was not served at all, so it could not detect a regression in the
// component, the route, or the bundle. It now drives the real application:
// a real script and run are created through the controller API, and the
// assertions read the Command Center that the shipped bundle renders for that
// live run.
//
// The subject is the averaged top-line tiles: "TPS" and "ART" carry a
// "10s avg" hint, and `.command-hero-metric.has-hint span` is nowrap +
// ellipsis, so a layout regression silently truncates the label instead of
// breaking. The narrowest five-column desktop width is the tight case.

const NARROWEST_FIVE_COLUMN_WIDTH = 1281
const DEFAULT_STACKED_WIDTH = 1280

// Every wait in this spec is capped explicitly and the cap is part of the test
// timeout below, so the budget cannot silently exceed it. That matters: an
// over-budget spec times out mid-assertion, and a timed-out spec used to leak
// its seeded run onto the shared controller (see support/liveRun.ts).
const READY_TIMEOUT_MS = 90_000
const NAVIGATION_TIMEOUT_MS = 30_000
const AVERAGED_HINT_TIMEOUT_MS = 60_000
// Nine further waiting assertions at Playwright's 5s default expect timeout
// (playwright.config.ts sets no `expect.timeout`), plus margin.
const TAIL_ASSERTION_BUDGET_MS = 60_000
const TEST_TIMEOUT_MS =
  READY_TIMEOUT_MS + NAVIGATION_TIMEOUT_MS + AVERAGED_HINT_TIMEOUT_MS + TAIL_ASSERTION_BUDGET_MS

/**
 * Reads a rendered hero tile as a number, tolerating thousands separators.
 * The tiles are rendered with `toLocaleString(undefined, …)`, i.e. the browser
 * locale, so this (and the en-US format guards below) is only well defined
 * because playwright.config.ts pins `locale: 'en-US'`. Without that pin a
 * healthy de-DE runner renders 1234.5 as "1.234,5", the format guards fail, and
 * comma-stripping would parse it as 1.2345.
 */
async function heroReading(tile: Locator): Promise<number> {
  return Number((await tile.innerText()).replace(/,/g, ''))
}

/** Counts the strip's rendered grid tracks (computed style is resolved px). */
function heroColumnCount(strip: Locator): Promise<number> {
  return strip.evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length)
}

test('shows the complete averaged metric titles for a live run', async ({ page, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)

  // Teardown lives in the `seedLiveRun` fixture, not in a `finally` here: a
  // `finally` does not survive a test timeout (the request context is already
  // closed by then), which used to leave the seeded run executing.
  const run = await seedLiveRun({
    label: 'command-center-hero-metrics',
    readyTimeoutMs: READY_TIMEOUT_MS,
  })

  await page.goto(`/#/command-center/${run.testId}`, { timeout: NAVIGATION_TIMEOUT_MS })

  const strip = page.locator('.command-hero-strip')
  await expect(strip).toBeVisible()

  // The averaged hints appear as soon as the live metrics history holds a
  // usable sample, so this is the first assertion that has to wait on the
  // running backend: with no stream at all no hint tile ever renders.
  const averagedTitles = strip.locator('.command-hero-metric.has-hint > span')
  await expect(averagedTitles).toHaveCount(2, { timeout: AVERAGED_HINT_TIMEOUT_MS })

  await expect(strip.locator('.command-hero-metric > span')).toHaveText([
    'TPS10s avg',
    'ART10s avg',
    'Error Rate',
    'Active VUsers',
    'Target VUsers',
  ])
  // The run really is executing, so the tiles have to carry non-zero readings,
  // not merely well-formed ones. `formatNumber` renders 0 — and every
  // non-finite value — as "0"/"0.0", so a shape-only regex stays green on a
  // dead feed; the value assertions below close that. The regexes remain as
  // format guards (no unit suffix, no placeholder glyph, non-empty).
  //
  // These prove the readings are non-zero, not that they are fresh.
  // `recentMetricAverages` anchors its 10s window on the newest sample's own
  // timestamp rather than wall-clock, so a feed that delivers one non-zero
  // sample and then freezes keeps rendering that value forever and still
  // satisfies both polls. Asserting the rendered value changes would catch
  // that, but under steady smoke load the one-decimal reading is near-constant,
  // so it would be flaky; freshness is deliberately not covered here.
  const tps = strip.locator('.command-hero-metric.accent strong')
  const activeVUsers = strip.locator('.command-hero-metric.info strong')
  await expect(tps).toHaveText(/^[\d,]+\.\d$/)
  await expect(activeVUsers).toHaveText(/^[\d,]+$/)
  await expect
    .poll(() => heroReading(tps), { message: 'TPS tile must report live throughput, not 0' })
    .toBeGreaterThan(0)
  await expect
    .poll(() => heroReading(activeVUsers), { message: 'Active VUsers tile must report live vusers, not 0' })
    .toBeGreaterThan(0)

  // Only the narrowest five-column width is a tight case for the averaged
  // labels. At the stacking breakpoint the strip drops to two columns and the
  // ops side rail collapses, which leaves each label roughly five times the
  // width it has at 1281px — a clipping poll there cannot fail, so it would
  // read as coverage it does not provide. Assert the collapse itself at that
  // width instead, and keep the clipping check where a regression shows up.
  await page.setViewportSize({ width: NARROWEST_FIVE_COLUMN_WIDTH, height: 900 })
  await expect
    .poll(() => heroColumnCount(strip), {
      message: `hero strip must stay five-up at ${NARROWEST_FIVE_COLUMN_WIDTH}px`,
    })
    .toBe(5)
  await expect
    .poll(async () => averagedTitles.evaluateAll(titles => titles.map(title => ({
      horizontal: title.scrollWidth > title.clientWidth,
      vertical: title.scrollHeight > title.clientHeight,
    }))), { message: `averaged hero titles must not clip at ${NARROWEST_FIVE_COLUMN_WIDTH}px` })
    .toEqual([
      { horizontal: false, vertical: false },
      { horizontal: false, vertical: false },
    ])

  await page.setViewportSize({ width: DEFAULT_STACKED_WIDTH, height: 900 })
  await expect
    .poll(() => heroColumnCount(strip), {
      message: `hero strip must collapse to two columns at ${DEFAULT_STACKED_WIDTH}px`,
    })
    .toBe(2)
})
