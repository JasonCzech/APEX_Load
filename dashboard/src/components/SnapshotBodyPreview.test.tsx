// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SnapshotBodyPreview from './SnapshotBodyPreview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('SnapshotBodyPreview', () => {
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
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: undefined,
    })
    container.remove()
  })

  it('opens long formatted XML bodies in a full dialog', async () => {
    const longXML = Array.from({ length: 18 }, (_, index) => `<line-${index}>value-${index}</line-${index}>`).join('')

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} />)
    })

    expect(container.textContent).toContain('Open full body')
    expect(container.textContent).not.toContain('line-17')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Open full body'))
    expect(button).not.toBeNull()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const dialog = document.body.querySelector('.snapshot-full-dialog[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(container.textContent).not.toContain('line-17')
    expect(dialog?.textContent).toContain('line-17')

    const copyButton = Array.from(dialog?.querySelectorAll('button') ?? []).find(node => node.textContent?.includes('Copy'))
    expect(copyButton).not.toBeNull()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('line-17'))
    expect(dialog?.textContent).toContain('Copied')

    const closeButton = Array.from(dialog?.querySelectorAll('button') ?? []).find(node => node.textContent?.includes('Close'))
    expect(closeButton).not.toBeNull()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(document.body.querySelector('.snapshot-full-dialog[role="dialog"]')).toBeNull()
  })

  it('renders short bodies with copy and full-body actions', async () => {
    await act(async () => {
      root.render(<SnapshotBodyPreview body="<root><value>ok</value></root>" headers={{ 'Content-Type': 'application/xml' }} />)
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    expect(buttons.some(button => button.textContent?.includes('Open full body'))).toBe(true)
    expect(buttons.some(button => button.textContent?.includes('Copy'))).toBe(true)
    expect(container.textContent).toContain('ok')
  })

  it('copies the full formatted body', async () => {
    await act(async () => {
      root.render(<SnapshotBodyPreview body="<root><value>ok</value></root>" headers={{ 'Content-Type': 'application/xml' }} />)
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

    expect(writeText).toHaveBeenCalledWith('<root>\n  <value>ok</value>\n</root>')
    expect(container.textContent).toContain('Copied')
  })

  it('uses the textarea copy fallback when available', async () => {
    const setData = vi.fn()
    const execCommand = vi.fn(() => {
      const copyEvent = new Event('copy', { bubbles: true, cancelable: true }) as ClipboardEvent
      Object.defineProperty(copyEvent, 'clipboardData', {
        configurable: true,
        value: { setData },
      })
      document.dispatchEvent(copyEvent)
      return true
    })
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommand,
    })

    await act(async () => {
      root.render(<SnapshotBodyPreview body="<root><value>ok</value></root>" headers={{ 'Content-Type': 'application/xml' }} />)
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

    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(setData).toHaveBeenCalledWith('text/plain', '<root>\n  <value>ok</value>\n</root>')
    expect(writeText).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Copied')
  })

  it('opens a selected manual copy dialog when automatic copy is blocked', async () => {
    writeText.mockRejectedValue(new Error('clipboard blocked'))

    await act(async () => {
      root.render(<SnapshotBodyPreview body="<root><value>ok</value></root>" headers={{ 'Content-Type': 'application/xml' }} />)
    })

    const copyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy'))
    expect(copyButton).not.toBeNull()
    if (!copyButton) {
      return
    }

    await act(async () => {
      copyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = document.body.querySelector('.snapshot-full-dialog[role="dialog"]')
    const manualCopyText = dialog?.querySelector('textarea')
    expect(dialog?.textContent).toContain('Text selected')
    expect(manualCopyText).not.toBeNull()
    expect(manualCopyText?.value).toBe('<root>\n  <value>ok</value>\n</root>')
    expect(document.activeElement).toBe(manualCopyText)
  })

  it('renders highlighted tokens for structured payloads', async () => {
    await act(async () => {
      root.render(<SnapshotBodyPreview body={JSON.stringify({ status: true })} headers={{ 'Content-Type': 'application/json' }} />)
    })

    expect(container.querySelector('.snapshot-code-token-key')).not.toBeNull()
    expect(container.querySelector('.snapshot-code-token-boolean')).not.toBeNull()
  })

  it('honors controlled expansion state', async () => {
    const longXML = Array.from({ length: 18 }, (_, index) => `<line-${index}>value-${index}</line-${index}>`).join('')
    const onExpandedChange = vi.fn()

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} expanded={false} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).not.toContain('line-17')
    expect(container.textContent).toContain('Open full body')
    expect(onExpandedChange).not.toHaveBeenCalled()

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} expanded={true} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).toContain('line-17')
  })
})
