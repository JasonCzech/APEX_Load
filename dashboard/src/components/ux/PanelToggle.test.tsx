// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import PanelToggle from './PanelToggle'
it('preserves the panel name and moves focus to its replacement trigger', async () => {
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { callback(0); return 1 })
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  function Example() {
    const [expanded, setExpanded] = useState(true)
    return <><PanelToggle expanded={expanded} panelName="User details" controls="details" onToggle={() => setExpanded(!expanded)} /><section id="details" hidden={!expanded}><input defaultValue="preserved draft" /></section></>
  }
  await act(async () => root.render(<Example />))
  const button = container.querySelector('button')!
  await act(async () => button.click())
  expect(button.getAttribute('aria-expanded')).toBe('false')
  expect(button.textContent).toContain('User details')
  expect(container.querySelector('input')!.value).toBe('preserved draft')
  await act(async () => button.click())
  expect(container.querySelector('section')!.hidden).toBe(false)
  await act(async () => root.unmount()); container.remove(); vi.unstubAllGlobals()
})
