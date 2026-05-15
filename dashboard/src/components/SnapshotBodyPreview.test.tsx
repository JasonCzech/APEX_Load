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
    container.remove()
  })

  it('collapses long formatted XML bodies until expanded', async () => {
    const longXML = Array.from({ length: 18 }, (_, index) => `<line-${index}>value-${index}</line-${index}>`).join('')

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} />)
    })

    expect(container.textContent).toContain('Show full body')
    expect(container.textContent).not.toContain('line-17')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Show full body'))
    expect(button).not.toBeNull()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Show less')
    expect(container.textContent).toContain('line-17')
  })

  it('renders short bodies without a toggle', async () => {
    await act(async () => {
      root.render(<SnapshotBodyPreview body="<root><value>ok</value></root>" headers={{ 'Content-Type': 'application/xml' }} />)
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    expect(buttons.some(button => button.textContent?.includes('Show full body'))).toBe(false)
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

  it('renders highlighted tokens for structured payloads', async () => {
    await act(async () => {
      root.render(<SnapshotBodyPreview body={JSON.stringify({ status: true })} headers={{ 'Content-Type': 'application/json' }} />)
    })

    expect(container.querySelector('.snapshot-code-token-key')).not.toBeNull()
    expect(container.querySelector('.snapshot-code-token-boolean')).not.toBeNull()
  })

  it('supports controlled expansion', async () => {
    const longXML = Array.from({ length: 18 }, (_, index) => `<line-${index}>value-${index}</line-${index}>`).join('')
    const onExpandedChange = vi.fn()

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} expanded={false} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).not.toContain('line-17')

    const button = Array.from(container.querySelectorAll('button')).find(node => node.textContent?.includes('Show full body'))
    expect(button).not.toBeNull()
    if (!button) {
      return
    }

    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onExpandedChange).toHaveBeenCalledWith(true)
    expect(container.textContent).not.toContain('line-17')

    await act(async () => {
      root.render(<SnapshotBodyPreview body={`<root>${longXML}</root>`} headers={{ 'Content-Type': 'application/soap+xml' }} expanded={true} onExpandedChange={onExpandedChange} />)
    })

    expect(container.textContent).toContain('line-17')
  })
})