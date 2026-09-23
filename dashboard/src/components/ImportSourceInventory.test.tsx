// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import ImportSourceInventory from './ImportSourceInventory'

it('keeps archive metadata in a closed disclosure and preserves its contents', async () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportSourceInventory script={{ name: 'Imported', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', source_script_name: 'Original archive' } } }} />))
    const details = container.querySelector('details')!
    expect(details.open).toBe(false)
    expect(details.querySelector('summary')?.textContent).toBe('Archive details')
    await act(async () => { details.querySelector('summary')!.click() })
    expect(details.open).toBe(true)
    expect(details.textContent).toContain('Original archive')
    expect(details.querySelector('pre')?.textContent).toContain('enterprise_script_archive')
  } finally {
    await act(async () => root.unmount())
  }
})

it.each([true, false])('copies the displayed metadata and reports clipboard success=%s', async (succeeds) => {
  const writeText = vi.fn().mockImplementation(() => succeeds ? Promise.resolve() : Promise.reject(new Error('Denied')))
  const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportSourceInventory script={{ name: 'Imported', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', source_script_name: 'Original archive' } } }} />))
    const button = container.querySelector('button')!
    expect(button.textContent).toBe('Copy JSON')
    await act(async () => { button.click() })
    expect(writeText).toHaveBeenCalledWith(container.querySelector('pre')!.textContent)
    expect(container.querySelector('[role="status"]')!.textContent).toBe(succeeds ? 'Copied to clipboard.' : 'Unable to copy. Select and copy the JSON below.')
  } finally {
    await act(async () => root.unmount())
    if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
    else Reflect.deleteProperty(navigator, 'clipboard')
  }
})
