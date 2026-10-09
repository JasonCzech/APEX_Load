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

  it('is presentation only: not focusable and not announced as a button', () => {
    const props = dialogBackdropProps(vi.fn()) as Record<string, unknown>
    expect(props.role).toBe('presentation')
    expect(props.tabIndex).toBeUndefined()
    expect(props['aria-label']).toBeUndefined()
  })
})
