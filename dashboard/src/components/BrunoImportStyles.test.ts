import { describe, expect, it } from 'vitest'

async function readStylesheet(path: URL) {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  return readFileSync(path, 'utf8') as string
}

function cssRule(css: string, selector: string) {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const matches = Array.from(css.matchAll(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, 'g')))
  return matches.map(match => match[1]).join('\n')
}

describe('Bruno import responsive layout', () => {
  it('fills the main content area and keeps the native table overflow local', async () => {
    const css = await readStylesheet(new URL('./BrunoImport.css', import.meta.url))
    const importer = cssRule(css, '.bruno-import')
    const tableWrap = cssRule(css, '.bruno-native-table-wrap')

    expect(importer).toContain('width: 100%;')
    expect(importer).toContain('min-width: 0;')
    expect(importer).toContain('max-width: none;')
    expect(importer).toContain('container-name: bruno-import;')
    expect(importer).toContain('container-type: inline-size;')
    expect(css).toContain('@container bruno-import (max-width: 820px)')
    expect(tableWrap).toContain('max-width: 100%;')
    expect(tableWrap).toContain('overflow-x: auto;')
  })

  it('allows imported credential names and controls to shrink inside their rows', async () => {
    const css = await readStylesheet(new URL('./ScriptImportReview.css', import.meta.url))
    const review = cssRule(css, '.secret-binding-review')
    const item = cssRule(css, '.secret-binding-review-item')
    const label = cssRule(css, '.secret-binding-review-label-row > strong')
    const choice = cssRule(css, '.secret-binding-review-choice')
    const reference = cssRule(css, '.secret-binding-review-reference')
    const pathScrollFocus = cssRule(css, '.credentials-location-scroll-region:focus-visible')

    expect(review).toContain('max-width: 100%;')
    expect(review).toContain('container-name: secret-binding-review;')
    expect(item).toContain('min-width: 0;')
    expect(item).toContain('max-width: 100%;')
    expect(label).toContain('overflow-wrap: anywhere;')
    expect(choice).toContain('width: 100%;')
    expect(choice).toContain('min-width: 0;')
    expect(choice).toContain('max-width: 100%;')
    expect(reference).toContain('overflow-wrap: anywhere;')
    expect(pathScrollFocus).toContain('outline: 2px solid var(--accent);')
    expect(css).toContain('@container secret-binding-review (max-width: 720px)')
  })

  it('wraps long secret identifiers inside the native Review dialog', async () => {
    const css = await readStylesheet(new URL('./BrunoImport.css', import.meta.url))
    const identifiers = cssRule(css, [
      '.bruno-native-review-section li,',
      '.bruno-native-review-section li strong,',
      '.bruno-native-review-section li span,',
      '.bruno-native-review-section li code',
    ].join('\n'))

    expect(identifiers).toContain('min-width: 0;')
    expect(identifiers).toContain('overflow-wrap: anywhere;')
  })

  it('uses the full source dialog body for the syntax-highlighted viewer', async () => {
    const css = await readStylesheet(new URL('./BrunoImport.css', import.meta.url))
    const body = cssRule(css, '.bruno-native-source-dialog-body')
    const section = cssRule(css, '.bruno-native-source-section')
    const viewer = cssRule(css, '.bruno-native-source-viewer')

    expect(body).toContain('display: flex;')
    expect(body).toContain('overflow: hidden;')
    expect(section).toContain('flex: 1 1 auto;')
    expect(section).toContain('min-height: 0;')
    expect(viewer).toContain('flex: 1 1 auto;')
    expect(viewer).toContain('min-height: 0;')
  })
})
