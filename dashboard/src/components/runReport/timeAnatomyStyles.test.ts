import { beforeAll, describe, expect, it } from 'vitest'

// jsdom does no layout or cascade, so the measurable parts of TimeAnatomy.css
// are checked against the source: the phase palette per theme and the rules
// that keep long names inside the panel at phone width.
let anatomyCss = ''
let themeCss = ''
beforeAll(async () => {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  anatomyCss = readFileSync(new URL('./TimeAnatomy.css', import.meta.url), 'utf8') as string
  themeCss = readFileSync(new URL('../../index.css', import.meta.url), 'utf8') as string
})

// Card surfaces as themeChartPalette.test.ts measures them.
const themes: Record<string, { selector: string; surface: string }> = {
  dark: { selector: ':root {', surface: '#0b0c12' },
  light: { selector: ":root[data-theme='light'] {", surface: '#ffffff' },
  'solarized-dark': { selector: ":root[data-theme='solarized-dark'] {", surface: '#06353f' },
  'solarized-light': { selector: ":root[data-theme='solarized-light'] {", surface: '#f0eadb' },
  'monokai-dimmed': { selector: ":root[data-theme='monokai-dimmed'] {", surface: '#252621' },
}
const TIMED_PHASES = ['dns', 'connect', 'tls', 'wait', 'receive'] as const

function block(css: string, selector: string): string {
  const start = css.indexOf(selector)
  expect(start, selector).toBeGreaterThanOrEqual(0)
  return css.slice(start, css.indexOf('}', start))
}

function luminance(hex: string) {
  const [r, g, b] = [1, 3, 5].map(index => {
    const value = parseInt(hex.slice(index, index + 2), 16) / 255
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
const contrast = (a: string, b: string) => {
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (high + 0.05) / (low + 0.05)
}

describe('time anatomy phase palette', () => {
  const slotFor = (phase: string) => {
    const base = block(anatomyCss, '.run-report-time-anatomy,\n.run-report-anatomy-drawer {')
    return new RegExp(`--rr-phase-${phase}:\\s*var\\(--chart-([1-6])\\b`).exec(base)?.[1] || ''
  }

  it('takes each timed phase from its own validated theme series colour', () => {
    const slots = TIMED_PHASES.map(slotFor)
    expect(slots.every(Boolean), slots.join(',')).toBe(true)
    expect(new Set(slots).size).toBe(TIMED_PHASES.length)
    // --chart-4 is red in every theme: a phase in it would read as an error.
    expect(slots).not.toContain('4')
    // No theme override may swap a timed phase back to a hand-picked colour.
    for (const phase of TIMED_PHASES) expect(anatomyCss.match(new RegExp(`--rr-phase-${phase}:`, 'g'))).toHaveLength(1)
  })

  it.each(Object.entries(themes))('%s: phases are distinct and readable on the card', (_, { selector, surface }) => {
    const theme = block(themeCss, selector)
    const colours = TIMED_PHASES.map(phase => new RegExp(`--chart-${slotFor(phase)}:\\s*(#[0-9a-f]{6});`, 'i').exec(theme)?.[1] || '')
    expect(colours.every(Boolean), colours.join(',')).toBe(true)
    expect(new Set(colours.map(colour => colour.toLowerCase())).size).toBe(TIMED_PHASES.length)
    for (const colour of colours) expect(contrast(colour, surface), colour).toBeGreaterThanOrEqual(3)
  })
})

describe('time anatomy narrow layout', () => {
  it('lets contributor and chip buttons wrap long request names', () => {
    const rule = block(anatomyCss, '.rr-anatomy-contributor,\n.rr-anatomy-chip {')
    expect(rule).toContain('max-width: 100%')
    expect(rule).toContain('min-width: 0')
    expect(rule).toContain('overflow-wrap: anywhere')
    expect(rule).toContain('white-space: normal')
  })

  it('never sizes the fact labels to an unbreakable max-content column', () => {
    const facts = block(anatomyCss, '.rr-anatomy-facts {')
    expect(facts).not.toMatch(/grid-template-columns:\s*max-content/)
    expect(block(anatomyCss, '.rr-anatomy-facts dt {')).toContain('overflow-wrap: anywhere')
    const narrow = anatomyCss.slice(anatomyCss.indexOf('@media (max-width: 640px)'))
    expect(narrow).toMatch(/\.rr-anatomy-facts \{ grid-template-columns: minmax\(0, 1fr\)/)
  })
})
