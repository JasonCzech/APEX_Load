// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { parseAppViewFromHash } from '../appRouting'
import { scenarioDataStepHref, scenarioLibraryHref, scriptEditorHref } from './assetLinks'
import SharedAssetUsedBy from './SharedAssetUsedBy'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('SharedAssetUsedBy', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const links = () => [...container.querySelectorAll('a')].map(link => [link.textContent, link.getAttribute('href')])

  it('links each scenario to its Data step and names the alias it binds', async () => {
    await act(async () => root.render(<SharedAssetUsedBy kind="scenario" projectId="p1" none="Unused" uses={[{ id: 's1', name: 'Checkout run', alias: 'customers' }, { id: 's2', name: 'Old run', alias: 'users', archived: true }]} />))
    expect(container.querySelector('ul')?.getAttribute('aria-label')).toBe('Used by 2 scenarios')
    // An archived scenario cannot be edited, so it points to the library, where it is restored.
    expect(links()).toEqual([['Checkout run', scenarioDataStepHref('s1', 'p1')], ['Scenario library', scenarioLibraryHref('p1')]])
    expect(container.textContent).toContain('(as customers)')
    expect(container.textContent).toContain('Old run(as users, archived)')
    expect(container.textContent).toContain('Restore an archived scenario from the Scenario library to change it.')
  })

  it('says nothing about restoring when no scenario is archived', async () => {
    await act(async () => root.render(<SharedAssetUsedBy kind="scenario" projectId="p1" none="Unused" uses={[{ id: 's1', name: 'Checkout run', alias: 'customers' }]} />))
    expect(container.textContent).not.toContain('Restore')
  })

  it('links each script to its editor', async () => {
    await act(async () => root.render(<SharedAssetUsedBy kind="script" label="Applied to" projectId="p1" none="Unused" uses={[{ id: 'a', name: 'Login' }]} />))
    expect(container.textContent).toContain('Applied to 1 script')
    expect(links()).toEqual([['Login', scriptEditorHref('a', 'p1')]])
  })

  it('says when nothing uses the asset', async () => {
    await act(async () => root.render(<SharedAssetUsedBy kind="script" projectId="p1" none="No script points at these objects yet." uses={[]} />))
    expect(container.textContent).toBe('No script points at these objects yet.')
    expect(container.querySelector('a')).toBeNull()
  })

  it('shows five at first and the rest on request', async () => {
    const uses = Array.from({ length: 7 }, (_, index) => ({ id: `s${index}`, name: `Script ${index}` }))
    await act(async () => root.render(<SharedAssetUsedBy kind="script" projectId="p1" none="" uses={uses} />))
    expect(links()).toHaveLength(5)
    const more = container.querySelector('button')!
    expect(more.textContent).toBe('Show all 7')
    await act(async () => more.click())
    expect(links()).toHaveLength(7)
    expect(more.getAttribute('aria-expanded')).toBe('true')
  })

  it('builds links the router opens: the builder at the Data step, and the script editor', () => {
    expect(parseAppViewFromHash(scenarioDataStepHref('s1', 'p1'))).toEqual({ page: 'tests', scenarioView: 'builder', scenarioId: 's1', scenarioStep: 'data', projectId: 'p1' })
    expect(parseAppViewFromHash(scriptEditorHref('a', 'p1'))).toMatchObject({ page: 'scripts', mode: 'edit', scriptId: 'a', projectId: 'p1' })
  })
})
