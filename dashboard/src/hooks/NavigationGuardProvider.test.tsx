// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  NavigationGuardProvider,
  useNavigationGuard,
  useNavigationGuardRunner,
  type NavigationGuard,
} from './NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('NavigationGuardProvider', () => {
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
    vi.clearAllMocks()
  })

  it('proceeds immediately when no registered guard is dirty', async () => {
    const proceed = vi.fn()

    await renderHarness({ dirty: false, proceed })
    await click('Navigate')

    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('keeps editing without running the pending navigation', async () => {
    const proceed = vi.fn()

    await renderHarness({ dirty: true, proceed })
    await click('Navigate')

    expect(container.textContent).toContain('Unsaved Changes')
    expect(proceed).not.toHaveBeenCalled()

    await click('Keep editing')

    expect(container.textContent).not.toContain('Unsaved Changes')
    expect(proceed).not.toHaveBeenCalled()
  })

  it('discards and proceeds', async () => {
    const proceed = vi.fn()
    const discard = vi.fn()

    await renderHarness({ dirty: true, proceed, discard })
    await click('Navigate')
    await click('Discard changes')

    expect(discard).toHaveBeenCalledTimes(1)
    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('saves and proceeds only when the save succeeds', async () => {
    const proceed = vi.fn()
    const save = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true)

    await renderHarness({ dirty: true, proceed, save })
    await click('Navigate')
    await click('Save changes')

    expect(save).toHaveBeenCalledTimes(1)
    expect(proceed).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Save did not complete')

    await click('Save changes')

    expect(save).toHaveBeenCalledTimes(2)
    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('ignores a second guarded navigation while a prompt is open', async () => {
    const proceed = vi.fn()

    await renderHarness({ dirty: true, proceed })
    await click('Navigate')
    await click('Navigate')

    expect(container.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(proceed).not.toHaveBeenCalled()
  })

  it('does not prompt after the guard unregisters', async () => {
    const proceed = vi.fn()
    const guard = makeGuard({ dirty: true })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <Runner proceed={proceed} />
          <Publisher guard={guard} dirty />
        </NavigationGuardProvider>,
      )
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <Runner proceed={proceed} />
        </NavigationGuardProvider>,
      )
    })

    await click('Navigate')

    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('prevents native unload only while a registered guard is dirty', async () => {
    await renderHarness({ dirty: true })

    const dirtyUnload = new Event('beforeunload', { cancelable: true })
    const dirtyAllowed = window.dispatchEvent(dirtyUnload)

    expect(dirtyAllowed).toBe(false)
    expect(dirtyUnload.defaultPrevented).toBe(true)

    await renderHarness({ dirty: false })

    const cleanUnload = new Event('beforeunload', { cancelable: true })
    const cleanAllowed = window.dispatchEvent(cleanUnload)

    expect(cleanAllowed).toBe(true)
    expect(cleanUnload.defaultPrevented).toBe(false)
  })

  async function renderHarness({
    dirty,
    discard = vi.fn(),
    proceed = vi.fn(),
    save = vi.fn().mockResolvedValue(true),
  }: {
    dirty: boolean
    discard?: () => void
    proceed?: () => void
    save?: () => Promise<boolean>
  }) {
    const guard = makeGuard({ dirty, discard, save })
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <Runner proceed={proceed} />
          <Publisher guard={guard} dirty={dirty} />
        </NavigationGuardProvider>,
      )
    })
  }

  function makeGuard({
    dirty,
    discard = vi.fn(),
    save = vi.fn().mockResolvedValue(true),
  }: {
    dirty: boolean
    discard?: () => void
    save?: () => Promise<boolean>
  }): NavigationGuard {
    return {
      id: 'test-guard',
      label: 'Test Builder',
      isDirty: () => dirty,
      save,
      discard,
    }
  }

  async function click(label: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(button).toBeDefined()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => Promise.resolve())
  }
})

function Publisher({ guard, dirty }: { guard: NavigationGuard; dirty: boolean }) {
  useNavigationGuard(guard, dirty)
  return null
}

function Runner({ proceed }: { proceed: () => void }) {
  const { runGuarded } = useNavigationGuardRunner()
  return (
    <button type="button" onClick={() => runGuarded(proceed)}>
      Navigate
    </button>
  )
}
