// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ImportExistingScript } from '../types/scriptImports'
import ImportExistingScripts from './ImportExistingScripts'
import { buildImportSaveChoices } from './importReimport'

const match = (overrides: Partial<ImportExistingScript> = {}): ImportExistingScript => ({
  script_id: 'script-1',
  name: 'Orders',
  ref_key: 'LOC-SCR-0001-v2',
  updated_at: '2026-10-15T10:00:00Z',
  matches: 1,
  same_source: true,
  edited_since_import: false,
  ...overrides,
})

describe('ImportExistingScripts', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('says what the save does to each script imported before and lets the operator change it', () => {
    const setChoice = vi.fn()
    const choices = buildImportSaveChoices([match({ edited_since_import: true }), null, match({ script_id: 'script-3', name: 'Browse', same_source: false })], {}, setChoice)
    act(() => root.render(<ImportExistingScripts choices={choices} labels={['Orders', 'Search', 'Browse']} />))

    expect(container.textContent).toContain('2 of 3 scripts were imported before.')
    expect(container.textContent).toContain('Imported before as Orders (LOC-SCR-0001-v2)')
    expect(container.textContent).toContain('Changed in APEX after it was imported.')
    expect(container.textContent).toContain('Matched by a file or generated name only')
    const selects = container.querySelectorAll('select')
    expect(selects).toHaveLength(2)
    expect((selects[0] as HTMLSelectElement).value).toBe('update')
    expect((selects[1] as HTMLSelectElement).value).toBe('copy')
    expect(container.textContent).toContain('1 is updated as a new version, keeping its name and folder.')
    expect(selects[0].closest('label')?.textContent).toContain('On save for Orders')
    expect(Array.from(selects[0].options).map(option => option.value)).toEqual(['update', 'copy'])

    act(() => {
      selects[0].value = 'copy'
      selects[0].dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(setChoice).toHaveBeenCalledWith(0, 'copy')
  })

  it('renders nothing when no script was imported before', () => {
    act(() => root.render(<ImportExistingScripts choices={buildImportSaveChoices([null], {}, vi.fn())} labels={['Orders']} />))
    expect(container.textContent).toBe('')
  })
})
