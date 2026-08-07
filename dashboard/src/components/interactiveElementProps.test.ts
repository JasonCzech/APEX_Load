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

  it('leaves keyboard activation to nested controls the event bubbled from', () => {
    const activate = vi.fn()
    const preventDefault = vi.fn()
    const props = interactiveElementProps(activate, 'Schedule a run on this day')
    const wrapper = { tagName: 'DIV' }
    const nestedButton = { tagName: 'BUTTON' }

    props.onKeyDown({ key: 'Enter', preventDefault, target: nestedButton, currentTarget: wrapper } as never)
    props.onKeyDown({ key: ' ', preventDefault, target: nestedButton, currentTarget: wrapper } as never)

    // preventDefault would cancel the browser's synthesized click on the button.
    expect(preventDefault).not.toHaveBeenCalled()
    expect(activate).not.toHaveBeenCalled()

    props.onKeyDown({ key: 'Enter', preventDefault, target: wrapper, currentTarget: wrapper } as never)
    expect(activate).toHaveBeenCalledTimes(1)
  })
})
