import { describe, expect, it } from 'vitest'

async function readStylesheet(path: URL): Promise<string> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  return readFileSync(path, 'utf8')
}

function ruleBody(stylesheet: string, pattern: RegExp): string {
  return stylesheet.match(pattern)?.[1] || ''
}

describe('Run Calendar styles', () => {
  it('gives Solarized Dark distinct source colors for cron and ad-hoc runs', async () => {
    const css = await readStylesheet(new URL('../index.css', import.meta.url))
    const block = ruleBody(css, /:root\[data-theme='solarized-dark'\] \{([\s\S]*?)\n\}/)

    expect(block).toContain('--run-calendar-cron-bg: rgba(42, 161, 152, 0.18);')
    expect(block).toContain('--run-calendar-cron-text: #2aa198;')
    expect(block).toContain('--run-calendar-ad-hoc-bg: rgba(211, 54, 130, 0.18);')
    expect(block).toContain('--run-calendar-ad-hoc-text: #d33682;')
    expect(block).not.toContain('--run-calendar-ad-hoc-text: #2aa198;')
  })

  it('applies run source tokens to calendar events and detail labels', async () => {
    const css = await readStylesheet(new URL('./Pages.css', import.meta.url))
    const cronEvent = ruleBody(css, /\.run-calendar-event\.cron \{([\s\S]*?)\n\}/)
    const adHocEvent = ruleBody(css, /\.run-calendar-event\.ad-hoc \{([\s\S]*?)\n\}/)
    const cronSource = ruleBody(css, /\.run-calendar-source\.cron \{([\s\S]*?)\n\}/)
    const adHocSource = ruleBody(css, /\.run-calendar-source\.ad_hoc \{([\s\S]*?)\n\}/)

    expect(cronEvent).toContain('background: var(--run-calendar-cron-bg);')
    expect(cronEvent).toContain('box-shadow: inset 3px 0 0 var(--run-calendar-cron-text);')
    expect(adHocEvent).toContain('background: var(--run-calendar-ad-hoc-bg);')
    expect(adHocEvent).toContain('box-shadow: inset 3px 0 0 var(--run-calendar-ad-hoc-text);')
    expect(cronSource).toContain('color: var(--run-calendar-cron-text);')
    expect(adHocSource).toContain('color: var(--run-calendar-ad-hoc-text);')
  })
})
