// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import HarCaptureGuideButton from './HarCaptureGuideButton'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('HarCaptureGuideButton', () => {
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

  it('opens an accessible modal dialog with browser DevTools settings, size-reduction tips, and copyable jq command', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    act(() => {
      root.render(<HarCaptureGuideButton />)
    })

    const trigger = container.querySelector('button')
    expect(trigger?.textContent).toBe('HAR capture guide')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()

    act(() => {
      trigger?.click()
    })

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).toBeTruthy()
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.textContent).toContain('Preserve log')
    expect(dialog?.textContent).toContain('Allow to generate HAR (with sensitive data)')
    expect(dialog?.textContent).toContain('Disable cache')
    expect(dialog?.textContent).toContain('Clear network log')
    expect(dialog?.textContent).toContain('Copy all listed as HAR')
    expect(dialog?.textContent).toContain('.zip')

    const copyBtn = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent === 'Copy jq command')
    expect(copyBtn).toBeTruthy()
    await act(async () => {
      copyBtn?.click()
      await Promise.resolve()
    })
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('_resourceType == "script"'))
    expect(copyBtn?.textContent).toBe('Copied command')

    const closeBtn = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent === 'Close')
    act(() => {
      closeBtn?.click()
    })
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('says the copy failed when the page has no clipboard access', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    act(() => { root.render(<HarCaptureGuideButton />) })
    act(() => { container.querySelector('button')?.click() })
    const copyBtn = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent === 'Copy jq command')!
    await act(async () => {
      copyBtn.click()
      await Promise.resolve()
    })
    expect(copyBtn.textContent).toBe('Copy jq command')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('The browser did not allow copying')
  })

  it('announces a copy failure through a live region that was already mounted, and again on a repeat failure', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    act(() => { root.render(<HarCaptureGuideButton />) })
    act(() => { container.querySelector('button')?.click() })
    const dialog = document.body.querySelector('[role="dialog"]')!
    const liveRegion = () => Array.from(dialog.querySelectorAll('[role="status"]')).find(node => node.closest('.har-capture-guide'))
    const region = liveRegion()
    expect(region).toBeDefined()
    expect(region?.textContent).toBe('')
    const copyBtn = Array.from(document.body.querySelectorAll('button')).find(btn => btn.textContent === 'Copy jq command')!
    await act(async () => { copyBtn.click(); await Promise.resolve() })
    expect(liveRegion()).toBe(region)
    const firstMessage = region?.firstElementChild
    expect(firstMessage?.textContent).toContain('The browser did not allow copying')
    await act(async () => { copyBtn.click(); await Promise.resolve() })
    expect(liveRegion()).toBe(region)
    expect(region?.firstElementChild).not.toBe(firstMessage)
    expect(region?.textContent).toContain('The browser did not allow copying')
  })

  it('does not promise automatic credential migration', () => {
    act(() => { root.render(<HarCaptureGuideButton />) })
    act(() => { container.querySelector('button')?.click() })
    const text = document.body.querySelector('[role="dialog"]')?.textContent || ''
    expect(text).not.toContain('automatically scrubs and binds')
    expect(text).toContain('Allow source secret migration')
  })
})
