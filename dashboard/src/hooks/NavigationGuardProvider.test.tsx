// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  useNavigationGuard,
  useNavigationGuardRunner,
  useUnsavedWorkGuard,
  type NavigationGuard,
} from './NavigationGuardProvider'
import { NavigationGuardProvider } from './NavigationGuardRoot'

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

  it('retains the parent guard across an internal return, including child resolution', async () => {
    const returned = vi.fn()
    const departed = vi.fn()
    const parentDiscard = vi.fn()
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={returned} retainedGuardIds={['parent']} />
      <Publisher guard={makeGuard({id:'parent', label:'Scenario', dirty:true, discard:parentDiscard})} dirty />
      <Publisher guard={makeGuard({id:'child', label:'Script', dirty:true})} dirty />
    </NavigationGuardProvider>))
    await click('Navigate')
    expect(container.textContent).toContain('Script has unsaved changes')
    await click('Discard changes')
    expect(returned).toHaveBeenCalledOnce()
    expect(parentDiscard).not.toHaveBeenCalled()
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={departed} />
      <Publisher guard={makeGuard({id:'parent', label:'Scenario', dirty:true, discard:parentDiscard})} dirty />
    </NavigationGuardProvider>))
    await click('Navigate')
    expect(container.textContent).toContain('Scenario has unsaved changes')
    expect(departed).not.toHaveBeenCalled()
    await click('Keep editing')
    expect(parentDiscard).not.toHaveBeenCalled()
  })

  it('asks only to discard or stay for work that cannot be saved from the prompt', async () => {
    const proceed = vi.fn()
    const discard = vi.fn()
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={proceed} />
      <UnsavedWork dirty discard={discard} />
    </NavigationGuardProvider>))
    await click('Navigate')
    expect(container.textContent).toContain('Import review has work that is not saved yet')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent?.includes('Save changes'))).toBe(false)
    await click('Discard changes')
    expect(discard).toHaveBeenCalledOnce()
    expect(proceed).toHaveBeenCalledOnce()
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

  it('goes ahead without saving when the work was saved or closed while the prompt was open', async () => {
    for (const settle of ['saved', 'closed'] as const) {
      const proceed = vi.fn()
      const save = vi.fn().mockResolvedValue(true)
      const guard = makeGuard({ dirty: true, save })
      await act(async () => root.render(<NavigationGuardProvider>
        <Runner proceed={proceed} />
        <Publisher guard={guard} dirty />
      </NavigationGuardProvider>))
      await click('Navigate')
      expect(container.textContent).toContain('Test Builder has unsaved changes')
      // The save that was already in flight lands, or the editor closes.
      await act(async () => root.render(<NavigationGuardProvider>
        <Runner proceed={proceed} />
        {settle === 'saved' && <Publisher guard={makeGuard({ dirty: false, save })} dirty={false} />}
      </NavigationGuardProvider>))
      await click('Save changes')
      expect(save, settle).not.toHaveBeenCalled()
      expect(proceed, settle).toHaveBeenCalledOnce()
      expect(container.textContent).not.toContain('Unsaved Changes')
      await act(async () => root.render(<></>))
    }
  })

  it('still saves when the guard reads dirty though its registration does not', async () => {
    const proceed = vi.fn()
    const save = vi.fn().mockResolvedValue(true)
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={proceed} />
      <Publisher guard={makeGuard({ dirty: true, save })} dirty />
    </NavigationGuardProvider>))
    await click('Navigate')
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={proceed} />
      <Publisher guard={makeGuard({ dirty: true, save })} dirty={false} />
    </NavigationGuardProvider>))
    await click('Save changes')
    expect(save).toHaveBeenCalledOnce()
    expect(proceed).toHaveBeenCalledOnce()
  })

  it('lets an unsaved-work guard read its live state', async () => {
    const proceed = vi.fn()
    let live = true
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={proceed} />
      <LiveUnsavedWork dirty={false} isDirty={() => live} discard={vi.fn()} />
    </NavigationGuardProvider>))
    // Rendered clean, but the live state is dirty: the prompt appears.
    await click('Navigate')
    expect(container.textContent).toContain('Import review has work that is not saved yet')
    await click('Keep editing')
    // Rendered dirty, live state clean and the guard marked clean: no prompt.
    live = false
    await act(async () => root.render(<NavigationGuardProvider>
      <Runner proceed={proceed} />
      <LiveUnsavedWork dirty isDirty={() => live} discard={vi.fn()} withMarkCleanButton />
    </NavigationGuardProvider>))
    await click('Mark clean')
    await click('Navigate')
    expect(proceed).toHaveBeenCalledOnce()
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

  it('allows a resolved navigation to pass through a nested guarded owner', async () => {
    const proceed = vi.fn()
    const discard = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <NestedRunner proceed={proceed} />
          <Publisher guard={makeGuard({ dirty: true, discard })} dirty />
        </NavigationGuardProvider>,
      )
    })

    await click('Navigate nested')
    expect(container.textContent).toContain('Unsaved Changes')

    await click('Discard changes')

    expect(discard).toHaveBeenCalledTimes(1)
    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('prompts for the most recently registered dirty guard', async () => {
    const proceed = vi.fn()
    const pageDiscard = vi.fn()
    const modalDiscard = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <Runner proceed={proceed} />
          <Publisher guard={makeGuard({ dirty: true, label: 'Page Builder', discard: pageDiscard })} dirty />
          <Publisher guard={makeGuard({ id: 'modal-guard', dirty: true, label: 'Inline Editor', discard: modalDiscard })} dirty />
        </NavigationGuardProvider>,
      )
    })

    await click('Navigate')

    expect(container.textContent).toContain('Inline Editor has unsaved changes')
    expect(container.textContent).not.toContain('Page Builder has unsaved changes')

    await click('Discard changes')

    expect(modalDiscard).toHaveBeenCalledTimes(1)
    expect(pageDiscard).not.toHaveBeenCalled()
    expect(proceed).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Page Builder has unsaved changes')

    await click('Discard changes')

    expect(pageDiscard).toHaveBeenCalledTimes(1)
    expect(proceed).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
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

  // A native quit in the desktop app asks through the same prompt: Keep
  // editing stays, Discard quits, and a quit pressed while a leave is being
  // asked about takes over that prompt.
  it('asks before a desktop quit and quits only once the work is resolved', async () => {
    const { listeners, confirmClose, uninstall } = installDesktopShell()
    try {
      const proceed = vi.fn()
      const discard = vi.fn()
      await act(async () => root.render(<NavigationGuardProvider>
        <Runner proceed={proceed} />
        <UnsavedWork dirty discard={discard} />
      </NavigationGuardProvider>))
      await act(async () => { await Promise.resolve() })

      await act(async () => { listeners.get('desktop:close-requested')?.('1') })
      expect(container.textContent).toContain('Import review has work that is not saved yet')
      await click('Keep editing')
      expect(confirmClose).not.toHaveBeenCalled()

      await click('Navigate')
      await act(async () => { listeners.get('desktop:close-requested')?.('2') })
      await click('Discard changes')
      expect(discard).toHaveBeenCalledOnce()
      expect(proceed).not.toHaveBeenCalled()
      expect(confirmClose).toHaveBeenCalledWith('guard-token', '2')
    } finally {
      uninstall()
    }
  })

  // A quit pressed while the prompt's save is still running quits once the
  // save lands, instead of making the leave first asked about.
  it('quits after a save that a desktop quit took over', async () => {
    const { listeners, confirmClose, uninstall } = installDesktopShell()
    try {
      let finishSave: (saved: boolean) => void = () => undefined
      const save = vi.fn(() => new Promise<boolean>(resolve => { finishSave = resolve }))
      const proceed = vi.fn()
      await renderHarness({ dirty: true, proceed, save })
      await act(async () => { await Promise.resolve() })

      await click('Navigate')
      await click('Save changes')
      await act(async () => { listeners.get('desktop:close-requested')?.('1') })
      await act(async () => { finishSave(true) })
      expect(save).toHaveBeenCalledOnce()
      expect(proceed).not.toHaveBeenCalled()
      expect(confirmClose).toHaveBeenCalledWith('guard-token', '1')
    } finally {
      uninstall()
    }
  })

  // A dialog's own provider leaves quitting to the app's: it registers no
  // close guard of its own, so closing the dialog cannot remove the app's.
  it('answers a desktop quit only from the outermost provider', async () => {
    const { bridge, listeners, uninstall } = installDesktopShell()
    try {
      await act(async () => root.render(<NavigationGuardProvider>
        <UnsavedWork dirty discard={vi.fn()} />
        <NavigationGuardProvider><span>Shared assets</span></NavigationGuardProvider>
      </NavigationGuardProvider>))
      await act(async () => { await Promise.resolve() })
      await act(async () => root.render(<NavigationGuardProvider>
        <UnsavedWork dirty discard={vi.fn()} />
      </NavigationGuardProvider>))
      expect(bridge.EnableCloseGuard).toHaveBeenCalledOnce()
      expect(bridge.DisableCloseGuard).not.toHaveBeenCalled()

      await act(async () => { listeners.get('desktop:close-requested')?.('1') })
      expect(container.textContent).toContain('Import review has work that is not saved yet')
    } finally {
      uninstall()
    }
  })

  function installDesktopShell() {
    const listeners = new Map<string, (...data: unknown[]) => void>()
    const confirmClose = vi.fn().mockResolvedValue(undefined)
    const bridge = {
      EnableCloseGuard: vi.fn().mockResolvedValue('guard-token'),
      DisableCloseGuard: vi.fn().mockResolvedValue(undefined),
      AcknowledgeCloseRequest: vi.fn().mockResolvedValue(undefined),
      ConfirmClose: confirmClose,
    }
    const desktopWindow = window as typeof window & { go?: unknown; runtime?: unknown }
    desktopWindow.go = { main: { App: bridge } }
    desktopWindow.runtime = {
      EventsOn: (name: string, callback: (...data: unknown[]) => void) => {
        listeners.set(name, callback)
        return () => listeners.delete(name)
      },
    }
    const uninstall = () => {
      delete desktopWindow.go
      delete desktopWindow.runtime
    }
    return { bridge, listeners, confirmClose, uninstall }
  }

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
    id = 'test-guard',
    label = 'Test Builder',
    save = vi.fn().mockResolvedValue(true),
  }: {
    dirty: boolean
    discard?: () => void
    id?: string
    label?: string
    save?: () => Promise<boolean>
  }): NavigationGuard {
    return {
      id,
      label,
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

function Runner({ proceed, retainedGuardIds }: { proceed: () => void; retainedGuardIds?: readonly string[] }) {
  const { runGuarded } = useNavigationGuardRunner()
  return (
    <button type="button" onClick={() => runGuarded(proceed, retainedGuardIds)}>
      Navigate
    </button>
  )
}

function NestedRunner({ proceed }: { proceed: () => void }) {
  const { runGuarded } = useNavigationGuardRunner()
  return (
    <button type="button" onClick={() => runGuarded(() => runGuarded(proceed))}>
      Navigate nested
    </button>
  )
}

function UnsavedWork({ dirty, discard }: { dirty: boolean; discard: () => void }) {
  useUnsavedWorkGuard('import-review', 'Import review', dirty, discard)
  return null
}

function LiveUnsavedWork({ dirty, isDirty, discard, withMarkCleanButton }: { dirty: boolean; isDirty: () => boolean; discard: () => void; withMarkCleanButton?: boolean }) {
  useUnsavedWorkGuard('import-review', 'Import review', dirty, discard, isDirty)
  const { markClean } = useNavigationGuardRunner()
  return withMarkCleanButton ? <button type="button" onClick={() => markClean('import-review')}>Mark clean</button> : null
}
