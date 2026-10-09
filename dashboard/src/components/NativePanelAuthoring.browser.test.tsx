// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { NativePanelAuthoring } from './NativeAnalyticsPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

async function select(label: string, value: string) {
  const element = container.querySelector<HTMLSelectElement>(`select[aria-label="${label}"]`)!
  await act(async () => { element.value = value; element.dispatchEvent(new Event('change', { bubbles: true })) })
}
async function type(label: string, value: string) {
  const element = container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

it('saves a browser document panel for one measurement tuple (BI-4)', async () => {
  const onSave = vi.fn().mockResolvedValue(undefined)
  await act(async () => root.render(<NativePanelAuthoring onSave={onSave} />))
  await select('Evidence population', 'browser_document')
  // The action and property selections do not apply to documents.
  expect(container.textContent).not.toContain('Exact action or transaction name')
  await type('Engine filter', 'webkit')
  await type('Device profile filter', 'iphone_15')
  await type('Group filter', 'checkout')
  await select('Measurement', 'p95')
  await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
  const query = onSave.mock.calls[0][0].tabs[0].panels[0].query
  expect(query).toMatchObject({
    population: 'browser_document',
    metric: 'document_lcp',
    aggregation: 'p95',
    filters: { engine: 'webkit', device_profile: 'iphone_15', group: 'checkout', frame_scope: 'main_frame', lifecycle_phase: 'navigation' },
    max_series: 1,
  })
  expect(query.filters).not.toHaveProperty('action')
  expect(query).not.toHaveProperty('evidence_mode')
})

it('offers browser documents only for archive panels, not evidence workflows', async () => {
  await act(async () => root.render(<NativePanelAuthoring onSave={vi.fn()} />))
  await select('Evidence workflow', 'live')
  expect(container.querySelector('option[value="browser_document"]')).toBeNull()
})
