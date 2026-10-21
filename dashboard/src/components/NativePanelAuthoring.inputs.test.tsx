// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
import { NativePanelAuthoring } from './NativeAnalyticsPanel'
const { freeze } = vi.hoisted(() => ({ freeze: vi.fn() }))
vi.mock('./browserPanelInputs', () => ({ freezeBrowserPanelDefinition: freeze }))
let container: HTMLDivElement
let root: Root | undefined
beforeEach(() => { freeze.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { if (root) await act(async () => root!.unmount()); container.remove() })
async function selectBrowserAndFreeze() {
 const select = container.querySelector<HTMLSelectElement>('select[aria-label="Evidence population"]')!
 await act(async () => { select.value = 'browser_document'; select.dispatchEvent(new Event('change', { bubbles: true })) })
 for (const text of ['Show separate browser cohorts', 'Freeze archive inputs for this panel']) {
  const label = [...container.querySelectorAll('label')].find(item => item.textContent?.includes(text))!
  await act(async () => label.querySelector<HTMLInputElement>('input')!.click())
 }
}
it('evaluates frozen inputs before saving the authored panel', async () => {
 const onSave = vi.fn().mockResolvedValue(undefined)
 freeze.mockImplementation(async (_run, definition) => definition)
 await act(async () => root!.render(<NativePanelAuthoring runId="current" onSave={onSave} />))
 await selectBrowserAndFreeze()
 await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
 expect(freeze).toHaveBeenCalledWith('current', expect.any(Object), expect.any(AbortSignal))
 expect(onSave.mock.calls[0][0].tabs[0].panels[0].query).toMatchObject({ query_version: 4, browser_tuple_mode: 'separate' })
})
it('does not save a late preview after leaving the run', async () => {
 const onSave = vi.fn()
 let resolve!: (definition: Pick<DashboardDefinition, 'name' | 'tabs'>) => void
 freeze.mockReturnValue(new Promise(done => { resolve = done }))
 await act(async () => root!.render(<NativePanelAuthoring runId="current" onSave={onSave} />))
 await selectBrowserAndFreeze()
 await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
 const signal = freeze.mock.calls[0][2] as AbortSignal
 await act(async () => { root!.unmount(); root = undefined })
 expect(signal.aborted).toBe(true)
 await act(async () => resolve(freeze.mock.calls[0][1]))
 expect(onSave).not.toHaveBeenCalled()
})
