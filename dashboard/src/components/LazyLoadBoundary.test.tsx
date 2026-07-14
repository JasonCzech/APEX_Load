// @vitest-environment jsdom

import { Component, type ReactNode } from 'react'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LazyLoadBoundary from './LazyLoadBoundary'

class ThrowingChild extends Component<{ fail: boolean; children?: ReactNode }> {
  render() {
    if (this.props.fail) throw new Error('chunk unavailable')
    return this.props.children || <span>Loaded</span>
  }
}

describe('LazyLoadBoundary', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.restoreAllMocks()
  })

  it('keeps the surrounding application mounted and offers a retry action', async () => {
    const retry = vi.fn()
    await act(async () => {
      root.render(
        <div>
          <span>Application shell</span>
          <LazyLoadBoundary title="Authoring tools could not be loaded." onRetry={retry}>
            <ThrowingChild fail />
          </LazyLoadBoundary>
        </div>,
      )
    })

    expect(container.textContent).toContain('Application shell')
    expect(container.textContent).toContain('Authoring tools could not be loaded.')
    const button = container.querySelector<HTMLButtonElement>('button')
    await act(async () => button?.click())
    expect(retry).toHaveBeenCalledTimes(1)
  })
})
