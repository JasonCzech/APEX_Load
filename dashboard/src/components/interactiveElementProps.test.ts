import { describe, expect, it, vi } from 'vitest'

import { interactiveElementProps } from './interactiveElementProps'

describe('interactiveElementProps', () => {
  it('supports pointer and keyboard activation', () => {
    const activate = vi.fn()
    const preventDefault = vi.fn()
    const props = interactiveElementProps(activate, 'Open row')
    props.onClick({ defaultPrevented: false } as never)
    props.onKeyDown({ key: ' ', preventDefault } as never)
    expect(activate).toHaveBeenCalledTimes(2)
    expect(preventDefault).toHaveBeenCalledOnce()
  })
})
