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
  const suppressExpectedRenderError = (event: ErrorEvent) => {
    if (event.error instanceof Error && event.error.message === 'chunk unavailable') {
      event.preventDefault()
    }
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.addEventListener('error', suppressExpectedRenderError)
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    window.removeEventListener('error', suppressExpectedRenderError)
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
