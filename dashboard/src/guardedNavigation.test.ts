import { describe, expect, it, vi } from 'vitest'

import { commitGuardedNavigation } from './guardedNavigation'

describe('commitGuardedNavigation', () => {
  it('defers navigation side effects until guarded navigation is accepted', () => {
    let pending: (() => void) | undefined
    const commit = vi.fn()
    const afterCommit = vi.fn()

    commitGuardedNavigation(proceed => { pending = proceed }, commit, afterCommit)

    expect(commit).not.toHaveBeenCalled()
    expect(afterCommit).not.toHaveBeenCalled()

    pending?.()

    expect(commit).toHaveBeenCalledTimes(1)
    expect(afterCommit).toHaveBeenCalledTimes(1)
    expect(commit.mock.invocationCallOrder[0]).toBeLessThan(afterCommit.mock.invocationCallOrder[0])
  })
})
