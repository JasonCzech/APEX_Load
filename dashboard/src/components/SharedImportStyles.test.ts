import { describe, expect, it } from 'vitest'

async function componentCSS(name: string): Promise<string> {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  return readFileSync(new URL(name, import.meta.url), 'utf8') as string
}

describe('shared import responsive CSS contracts', () => {
  it('sizes the protocol import from its content container and wraps long generated names', async () => {
    const css = await componentCSS('./SampleScriptImport.css')

    expect(css).toMatch(/\.sample-import\s*\{[^}]*width:\s*100%/s)
    expect(css).toMatch(/\.sample-import\s*\{[^}]*min-width:\s*0/s)
    expect(css).toMatch(/container:\s*sample-import\s*\/\s*inline-size/)
    expect(css).toContain('@container sample-import (max-width: 980px)')
    expect(css).toContain('@container sample-import (max-width: 720px)')
    expect(css).toMatch(/\.sample-import-script-tab\s*\{[^}]*overflow-wrap:\s*anywhere/s)
    expect(css).toMatch(/\.sample-import-script-tab\s*\{[^}]*white-space:\s*normal/s)
  })

  it('uses distinct named containers for the outer review and credential cards', async () => {
    const css = await componentCSS('./ScriptImportReview.css')

    expect(css).toMatch(/container:\s*script-import-review\s*\/\s*inline-size/)
    expect(css).toContain('container-name: secret-binding-review')
    expect(css).toContain('@container script-import-review (max-width: 720px)')
    expect(css).toContain('@container secret-binding-review (max-width: 720px)')
    expect(css).toMatch(/\.script-import-review-header h3\s*\{[^}]*overflow-wrap:\s*anywhere/s)
    expect(css).toMatch(/\.secret-binding-review-script > strong\s*\{[^}]*overflow-wrap:\s*anywhere/s)
  })

  it('keeps the shared destination control container-responsive', async () => {
    const css = await componentCSS('./ImportDestinationFolder.css')

    expect(css).toMatch(/\.import-destination-folder\s*\{[^}]*width:\s*100%/s)
    expect(css).toMatch(/container:\s*import-destination-folder\s*\/\s*inline-size/)
    expect(css).toContain('@container import-destination-folder (max-width: 620px)')
  })
})
