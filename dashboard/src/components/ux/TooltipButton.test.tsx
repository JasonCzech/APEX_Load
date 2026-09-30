// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import TooltipButton from './TooltipButton'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('preserves descriptions and actions, shows keyboard help, and dismisses with Escape', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); const click = vi.fn()
  try {
    await act(async () => root.render(<TooltipButton title="Move action up" aria-describedby="existing-help" onClick={click}><svg /></TooltipButton>))
    const button = host.querySelector('button')!
    expect(button.hasAttribute('title')).toBe(false)
    expect(button.getAttribute('aria-label')).toBe('Move action up')
    await act(async () => button.focus())
    const tip = document.querySelector('[role="tooltip"]')!
    expect(tip.textContent).toBe('Move action up')
    expect(button.getAttribute('aria-describedby')).toBe(`existing-help ${tip.id}`)

    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })))
    expect(document.querySelector('[role="tooltip"]')).toBeNull()
    expect(button.getAttribute('aria-describedby')).toBe('existing-help')
    await act(async () => button.click())
    expect(click).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('makes disabled-control explanations available without enabling the action', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host); const click = vi.fn()
  try {
    await act(async () => root.render(<TooltipButton disabled title="Select an action first" onClick={click}>Move up</TooltipButton>))
    await act(async () => host.querySelector('span')!.focus())
    expect(document.querySelector('[role="tooltip"]')?.textContent).toBe('Select an action first')
    host.querySelector('button')!.click()
    expect(click).not.toHaveBeenCalled()
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('retains the same button when its availability and explanation change', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipButton disabled title="No changes to save">Save</TooltipButton>))
    const button = host.querySelector('button')!
    await act(async () => root.render(<TooltipButton title="Save changes">Save</TooltipButton>))
    expect(host.querySelector('button')).toBe(button)
    expect(button.disabled).toBe(false)
    expect(button.textContent).toBe('Save')
    expect(button.hasAttribute('aria-label')).toBe(false)
    await act(async () => button.focus())
    expect(document.querySelector('[role="tooltip"]')?.textContent).toBe('Save changes')
  } finally { await act(async () => root.unmount()); host.remove() }
})

it('removes the tooltip description when help text is removed while focused', async () => {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<TooltipButton title="Save changes" aria-describedby="persistent-help">Save</TooltipButton>))
    const button = host.querySelector('button')!
    await act(async () => button.focus())
    expect(document.querySelector('[role="tooltip"]')).not.toBeNull()
    await act(async () => root.render(<TooltipButton aria-describedby="persistent-help">Save</TooltipButton>))
    expect(document.querySelector('[role="tooltip"]')).toBeNull()
    expect(button.getAttribute('aria-describedby')).toBe('persistent-help')
  } finally { await act(async () => root.unmount()); host.remove() }
})
