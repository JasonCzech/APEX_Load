import { readFile } from 'node:fs/promises'

import { expect, test } from '@playwright/test'

test('shows the complete averaged metric titles', async ({ page }) => {
  const styles = await readFile(new URL('../src/components/CommandCenterPrototype.css', import.meta.url), 'utf8')

  await page.setContent(`
    <style>
      * { box-sizing: border-box; }
      :root {
        --bg-card: #171b24;
        --border: #303744;
        --radius: 8px;
        --text-muted: #9ca3af;
        --text-primary: #f9fafb;
      }
      ${styles}
    </style>
    <main class="command-center-page">
      <div class="command-hero-strip" style="width: 570px">
        <div class="command-hero-metric accent has-hint">
          <span>TPS<small class="command-hero-hint">10s avg</small></span>
          <strong>74.1</strong>
        </div>
        <div class="command-hero-metric default has-hint">
          <span>ART<small class="command-hero-hint">10s avg</small></span>
          <strong>132 ms</strong>
        </div>
        <div class="command-hero-metric danger"><span>Error Rate</span><strong>0.50%</strong></div>
        <div class="command-hero-metric info"><span>Active VUsers</span><strong>42</strong></div>
        <div class="command-hero-metric success"><span>Target VUsers</span><strong>50</strong></div>
      </div>
    </main>
  `)

  const averagedTitles = page.locator('.command-hero-metric.has-hint > span')
  await expect(averagedTitles).toHaveCount(2)
  await expect(averagedTitles.nth(0)).toHaveText('TPS10s avg')
  await expect(averagedTitles.nth(1)).toHaveText('ART10s avg')

  const clipping = await averagedTitles.evaluateAll(titles => titles.map(title => ({
    horizontal: title.scrollWidth > title.clientWidth,
    vertical: title.scrollHeight > title.clientHeight,
  })))
  expect(clipping).toEqual([
    { horizontal: false, vertical: false },
    { horizontal: false, vertical: false },
  ])
})
