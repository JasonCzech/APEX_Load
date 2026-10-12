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

// The chevron points the way the panel will move: a start-side panel collapses
// toward the start and expands back out; an end-side panel is the mirror.
it.each([
  ['start', true, 'start'],
  ['start', false, 'end'],
  ['end', true, 'end'],
  ['end', false, 'start'],
] as const)('a %s-side panel that is expanded=%s points its chevron toward the %s', async (side, expanded, direction) => {
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => root.render(<PanelToggle side={side} expanded={expanded} panelName="Run details" controls="details" onToggle={() => undefined} />))
  const svg = container.querySelector('svg')!
  expect(svg.style.transform).toBe(direction === 'start' ? '' : 'rotate(180deg)')
  await act(async () => root.unmount()); container.remove()
})

it('draws only a chevron when icon-only, keeping the action in its accessible name', async () => {
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => root.render(<PanelToggle iconOnly side="end" expanded panelName="right sidebar" controls="details" onToggle={() => undefined} />))
  const button = container.querySelector('button')!
  expect(button.textContent).toBe('')
  expect(button.getAttribute('aria-label')).toBe('Collapse right sidebar')
  expect(button.classList.contains('ux-panel-toggle-icon')).toBe(true)
  await act(async () => root.unmount()); container.remove()
})
