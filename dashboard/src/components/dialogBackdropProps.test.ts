import { describe, expect, it, vi } from 'vitest'

import { dialogBackdropProps } from './dialogBackdropProps'

describe('dialogBackdropProps', () => {
  it('dismisses only direct backdrop clicks', () => {
    const dismiss = vi.fn()
    const props = dialogBackdropProps(dismiss)
    const backdrop = {}
    props.onClick({ target: backdrop, currentTarget: backdrop } as never)
    props.onClick({ target: {}, currentTarget: backdrop } as never)
    expect(dismiss).toHaveBeenCalledTimes(1)
  })

  it('supports Enter and Space keyboard dismissal', () => {
    const dismiss = vi.fn()
    const preventDefault = vi.fn()
    const props = dialogBackdropProps(dismiss)
    const backdrop = {}
    props.onKeyDown({ key: 'Enter', target: backdrop, currentTarget: backdrop, preventDefault } as never)
    props.onKeyDown({ key: 'Escape', target: backdrop, currentTarget: backdrop, preventDefault } as never)
    expect(dismiss).toHaveBeenCalledTimes(1)
    expect(preventDefault).toHaveBeenCalledTimes(1)
  })
})
