import { describe, expect, it } from 'vitest'

import { loadCssSources } from '../scripts/load-css-sources.mjs'

const cssFiles = await loadCssSources(new URL('.', import.meta.url))
const indexCss = cssFiles['index.css']

if (!indexCss) {
  throw new Error('Missing index.css in dashboard CSS modules')
}

function themeBlock(selector: string): string {
  const start = indexCss.indexOf(`${selector} {`)
  if (start < 0) throw new Error(`Missing theme selector ${selector}`)
  const end = indexCss.indexOf('\n}', start)
  if (end < 0) throw new Error(`Unclosed theme selector ${selector}`)
  return indexCss.slice(start, end)
}

function token(block: string, name: string): string {
  const match = block.match(new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})`))
  if (!match) throw new Error(`Missing hexadecimal --${name} token`)
  return match[1]
}

function relativeLuminance(hex: string): number {
  const channels = hex.slice(1).match(/.{2}/g)?.map(value => Number.parseInt(value, 16) / 255) || []
  const [red, green, blue] = channels.map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4)
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue
}

function contrast(left: string, right: string): number {
  const leftLuminance = relativeLuminance(left)
  const rightLuminance = relativeLuminance(right)
  return (Math.max(leftLuminance, rightLuminance) + 0.05) / (Math.min(leftLuminance, rightLuminance) + 0.05)
}

function hardcodedWhiteOnAccent(css: string, file: string): Array<{ file: string; selector: string }> {
  const findings: Array<{ file: string; selector: string }> = []
  const accentBackground =
    /(?:^|;)\s*background(?:-color)?\s*:\s*var\(\s*--accent(?:\s*,[^)]*)?\s*\)/i
  const hardcodedWhite =
    /(?:^|;)\s*color\s*:\s*(?:white|#fff(?:fff)?)(?=\s*(?:!important\s*)?(?:;|$))/i

  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const declarations = match[2]
    if (!accentBackground.test(declarations) || !hardcodedWhite.test(declarations)) continue
    const selectorLines = match[1].trim().split('\n')
    const selector = selectorLines[selectorLines.length - 1]?.trim() || '<unknown>'
    findings.push({ file, selector })
  }
  return findings
}

describe('dashboard theme tokens', () => {
  it('defines every CSS custom property that is used without a fallback', () => {
    const definitions = new Set<string>()
    const uses: Array<{ name: string; file: string }> = []

    Object.entries(cssFiles).forEach(([file, css]) => {
      for (const match of css.matchAll(/--([\w-]+)\s*:/g)) definitions.add(match[1])
      for (const match of css.matchAll(/var\(--([\w-]+)([^)]*)\)/g)) {
        if (!match[2].includes(',')) uses.push({ name: match[1], file })
      }
    })

    expect(uses.filter(use => !definitions.has(use.name))).toEqual([])
  })

  it('does not hard-code white text on accent-token backgrounds', () => {
    const findings = Object.entries(cssFiles).flatMap(([file, css]) =>
      hardcodedWhiteOnAccent(css, file),
    )

    expect(findings).toEqual([])
  })

  it('recognizes accent fallbacks when checking for hard-coded white text', () => {
    const sample = `
      .plain { background: var(--accent); color: white; }
      .fallback { background-color: var(--accent, #0ea5e9); color: #fff !important; }
      .allowed { background: var(--accent); color: var(--accent-contrast); }
    `

    expect(hardcodedWhiteOnAccent(sample, 'sample.css')).toEqual([
      { file: 'sample.css', selector: '.plain' },
      { file: 'sample.css', selector: '.fallback' },
    ])
  })

  it.each([
    [':root'],
    [":root[data-theme='light']"],
    [":root[data-theme='solarized-dark']"],
    [":root[data-theme='solarized-light']"],
    [":root[data-theme='monokai-dimmed']"],
  ])('%s keeps muted and accent text at normal-text contrast', (selector) => {
    const block = themeBlock(selector)
    const muted = token(block, 'text-muted')
    const primaryBackground = token(block, 'bg-primary')
    const secondaryBackground = token(block, 'bg-secondary')
    const accent = token(block, 'accent')
    const accentContrast = token(block, 'accent-contrast')

    expect(contrast(muted, primaryBackground)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(muted, secondaryBackground)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(accent, secondaryBackground)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(accentContrast, accent)).toBeGreaterThanOrEqual(4.5)
  })
})
