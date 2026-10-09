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

  it('drives its colors from theme tokens so it changes tone with the active theme', () => {
    act(() => root.render(<Logo />))
    expect(svg().querySelector(':scope > rect')?.getAttribute('fill')).toBe('var(--accent)')
    expect(container.querySelector('polygon')?.getAttribute('fill')).toBe('var(--accent-contrast)')
    expect(container.querySelector('path[stroke]')?.getAttribute('stroke')).toBe('var(--logo-wave, var(--brand-highlight))')
    for (const stop of Array.from(container.querySelectorAll('stop'))) {
      expect(stop.getAttribute('style')).toContain('var(--logo-wave, var(--brand-highlight))')
    }
  })

  it('switches to the bolder small cut at 32px and below', () => {
    act(() => root.render(<Logo size={32} />))
    expect(svg().getAttribute('data-logo-cut')).toBe('small')
    expect(container.querySelector('path[stroke]')?.getAttribute('stroke-width')).toBe('7')

    act(() => root.render(<Logo size={64} />))
    expect(svg().getAttribute('data-logo-cut')).toBe('master')
    expect(container.querySelector('path[stroke]')?.getAttribute('stroke-width')).toBe('5')
  })

  it('gives each instance its own gradient and clip ids', () => {
    act(() => root.render(<><Logo /><Logo /></>))
    const ids = Array.from(container.querySelectorAll('[id]'), (el) => el.id)
    expect(ids).toHaveLength(4)
    expect(new Set(ids).size).toBe(4)
    for (const logo of Array.from(container.querySelectorAll('svg'))) {
      const fade = logo.querySelector('linearGradient')?.id
      expect(logo.querySelector(`[fill="url(#${fade})"]`)).not.toBeNull()
    }
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
