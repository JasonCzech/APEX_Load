// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import PageViewToggle from './PageViewToggle'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('PageViewToggle', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('marks the active view and switches inactive options', async () => {
    const onChange = vi.fn()

    await act(async () => {
      root.render(
        <PageViewToggle
          value="library"
          ariaLabel="Example view"
          onChange={onChange}
          options={[
            { value: 'library', label: 'Library' },
            { value: 'builder', label: 'Builder' },
          ]}
        />,
      )
    })

    const [library, builder] = Array.from(container.querySelectorAll('button'))
    expect(container.querySelector('[role="group"]')?.getAttribute('aria-label')).toBe('Example view')
    expect(library.getAttribute('aria-pressed')).toBe('true')
    expect(builder.getAttribute('aria-pressed')).toBe('false')

    await act(async () => {
      builder.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onChange).toHaveBeenCalledWith('builder')
  })

  it('does not fire changes for the active or disabled option', async () => {
    const onChange = vi.fn()

    await act(async () => {
      root.render(
        <PageViewToggle
          value="library"
          ariaLabel="Example view"
          onChange={onChange}
          options={[
            { value: 'library', label: 'Library' },
            { value: 'builder', label: 'Builder', disabled: true },
          ]}
        />,
      )
    })

    const [library, builder] = Array.from(container.querySelectorAll('button'))

    await act(async () => {
      library.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      builder.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(builder.disabled).toBe(true)
    expect(onChange).not.toHaveBeenCalled()
  })
})
