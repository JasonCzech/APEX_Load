// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import Logo from './Logo'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('Logo', () => {
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

  function svg(): SVGSVGElement {
    const el = container.querySelector('svg')
    if (!el) {
      throw new Error('expected an svg to render')
    }
    return el as SVGSVGElement
  }

  it('renders an svg sized to the requested dimensions', () => {
    act(() => root.render(<Logo size={40} />))
    expect(svg().getAttribute('width')).toBe('40')
    expect(svg().getAttribute('height')).toBe('40')
  })

  it('defaults to a 28px mark', () => {
    act(() => root.render(<Logo />))
    expect(svg().getAttribute('width')).toBe('28')
  })

  it('drives its fills from theme accent variables so it adapts to the active theme', () => {
    act(() => root.render(<Logo />))
    // Two peaks plus the baseline rule are accent-driven, so the mark recolors
    // automatically when the --accent custom property changes per theme.
    expect(container.querySelectorAll('[fill="var(--accent)"]').length).toBe(3)
  })

  it('is decorative (aria-hidden) by default', () => {
    act(() => root.render(<Logo />))
    expect(svg().getAttribute('aria-hidden')).toBe('true')
    expect(svg().getAttribute('role')).toBeNull()
  })

  it('exposes an accessible name when a title is provided', () => {
    act(() => root.render(<Logo title="APEX Load" />))
    expect(svg().getAttribute('role')).toBe('img')
    expect(svg().getAttribute('aria-label')).toBe('APEX Load')
    expect(svg().getAttribute('aria-hidden')).toBeNull()
    expect(container.querySelector('title')?.textContent).toBe('APEX Load')
  })
})
