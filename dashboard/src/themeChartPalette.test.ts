import { beforeAll, expect, it } from 'vitest'

// The six series colours each theme defines were tuned with the dataviz palette
// validator (lightness band, chroma, CVD and normal-vision separation, 3:1 on the
// card surface). This guards the measurable part a later edit could break.
let css = ''
beforeAll(async () => {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  css = readFileSync(new URL('./index.css', import.meta.url), 'utf8') as string
})
const themes: Record<string, { selector: string; surface: string }> = {
  dark: { selector: ':root {', surface: '#0b0c12' },
  light: { selector: ":root[data-theme='light'] {", surface: '#ffffff' },
  'solarized-dark': { selector: ":root[data-theme='solarized-dark'] {", surface: '#06353f' },
  'solarized-light': { selector: ":root[data-theme='solarized-light'] {", surface: '#f0eadb' },
  'monokai-dimmed': { selector: ":root[data-theme='monokai-dimmed'] {", surface: '#252621' },
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

function seriesFor(selector: string): string[] {
  const start = css.indexOf(selector)
  const block = css.slice(start, css.indexOf('\n}', start))
  return [1, 2, 3, 4, 5, 6].map(slot => new RegExp(`--chart-${slot}:\\s*(#[0-9a-f]{6});`, 'i').exec(block)?.[1] || '')
}

it.each(Object.entries(themes))('%s chart series stay distinct and readable on the card surface', (_, { selector, surface }) => {
  const series = seriesFor(selector)
  expect(series.every(Boolean), series.join(',')).toBe(true)
  expect(new Set(series).size).toBe(6)
  for (const colour of series) expect(contrast(colour, surface), colour).toBeGreaterThanOrEqual(3)
})
