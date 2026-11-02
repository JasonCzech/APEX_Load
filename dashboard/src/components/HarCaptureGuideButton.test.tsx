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
})
