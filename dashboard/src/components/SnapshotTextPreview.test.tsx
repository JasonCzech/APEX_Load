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

  it('collapses long header text until expanded', async () => {
    const text = Array.from({ length: 14 }, (_, index) => `X-Debug-${index}: value-${index}`).join('\n')

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} />)
    })

    expect(container.textContent).toContain('Show full text')
    expect(container.textContent).not.toContain('X-Debug-13')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Show full text'))
    expect(button).not.toBeNull()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Show less')
    expect(container.textContent).toContain('X-Debug-13')
  })

  it('renders short text without a toggle', async () => {
    await act(async () => {
      root.render(<SnapshotTextPreview text={'Content-Type: application/json'} />)
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    expect(buttons.some(button => button.textContent?.includes('Show full text'))).toBe(false)
    expect(buttons.some(button => button.textContent?.includes('Copy'))).toBe(true)
    expect(container.textContent).toContain('Content-Type: application/json')
  })

  it('copies the full header text', async () => {
    const text = 'Content-Type: application/json\nX-Trace: abc123'

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} />)
    })

    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy'))
    expect(copyButton).not.toBeNull()
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

  it('supports controlled expansion', async () => {
    const text = Array.from({ length: 14 }, (_, index) => `X-Debug-${index}: value-${index}`).join('\n')
    const onExpandedChange = vi.fn()

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} expanded={false} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).not.toContain('X-Debug-13')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Show full text'))
    expect(button).not.toBeNull()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onExpandedChange).toHaveBeenCalledWith(true)
    expect(container.textContent).not.toContain('X-Debug-13')

    await act(async () => {
      root.render(<SnapshotTextPreview text={text} expanded={true} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).toContain('X-Debug-13')
  })
})