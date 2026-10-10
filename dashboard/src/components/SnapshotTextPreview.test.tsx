// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SnapshotTextPreview from './SnapshotTextPreview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('SnapshotTextPreview', () => {
  let container: HTMLDivElement
  let root: Root
  const writeText = vi.fn()

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    writeText.mockReset()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('opens long header text in a full dialog', async () => {
    const text = Array.from({ length: 14 }, (_, index) => `X-Debug-${index}: value-${index}`).join('\n')

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} />)
    })

    expect(container.textContent).toContain('Open full text')
    expect(container.textContent).not.toContain('X-Debug-13')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Open full text'))
    expect(button).toBeTruthy()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const dialog = document.body.querySelector('.snapshot-full-dialog[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(container.textContent).not.toContain('X-Debug-13')
    expect(dialog?.textContent).toContain('X-Debug-13')

    const copyButton = Array.from(dialog?.querySelectorAll('button') ?? []).find(node => node.textContent?.includes('Copy'))
    expect(copyButton).toBeTruthy()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(text)
    expect(dialog?.textContent).toContain('Copied')

    const closeButton = Array.from(dialog?.querySelectorAll('button') ?? []).find(node => node.textContent?.includes('Close'))
    expect(closeButton).toBeTruthy()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(document.body.querySelector('.snapshot-full-dialog[role="dialog"]')).toBeNull()
  })

  it('renders short text with copy and full-text actions', async () => {
    await act(async () => {
      root.render(<SnapshotTextPreview text={'Content-Type: application/json'} />)
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    expect(buttons.some(button => button.textContent?.includes('Open full text'))).toBe(true)
    expect(buttons.some(button => button.textContent?.includes('Copy'))).toBe(true)
    expect(container.textContent).toContain('Content-Type: application/json')
  })

  it('copies the full header text', async () => {
    const text = 'Content-Type: application/json\nX-Trace: abc123'

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} />)
    })

    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy'))
    expect(copyButton).toBeTruthy()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(text)
    expect(container.textContent).toContain('Copied')
  })

  it('honors controlled expansion state', async () => {
    const text = Array.from({ length: 14 }, (_, index) => `X-Debug-${index}: value-${index}`).join('\n')
    const onExpandedChange = vi.fn()

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} expanded={false} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).not.toContain('X-Debug-13')
    expect(container.textContent).toContain('Open full text')
    expect(onExpandedChange).not.toHaveBeenCalled()

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} expanded={true} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).toContain('X-Debug-13')
  })
})
