// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import UnsavedChangesModal from './UnsavedChangesModal'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('UnsavedChangesModal', () => {
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

  it('renders all choices and delegates actions', async () => {
    const onSave = vi.fn()
    const onDiscard = vi.fn()
    const onCancel = vi.fn()

    await act(async () => {
      root.render(
        <UnsavedChangesModal
          label="Scenario builder"
          onSave={onSave}
          onDiscard={onDiscard}
          onCancel={onCancel}
        />,
      )
    })

    expect(container.textContent).toContain('Scenario builder has unsaved changes')

    await click('Keep editing')
    await click('Discard changes')
    await click('Save changes')

    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onDiscard).toHaveBeenCalledTimes(1)
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('disables actions while saving and renders errors', async () => {
    await act(async () => {
      root.render(
        <UnsavedChangesModal
          label="Script Editor"
          saving
          error="Save did not complete"
          onSave={() => {}}
          onDiscard={() => {}}
          onCancel={() => {}}
        />,
      )
    })

    expect(container.textContent).toContain('Save did not complete')
    expect(button('Keep editing').disabled).toBe(true)
    expect(button('Discard changes').disabled).toBe(true)
    expect(button('Saving…').disabled).toBe(true)
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('Save did not complete')
  })

  it('provides a labelled focus-trapped dialog and Escape cancellation', async () => {
    const onCancel = vi.fn()
    await act(async () => {
      root.render(
        <UnsavedChangesModal
          label="Scenario builder"
          onSave={() => {}}
          onDiscard={() => {}}
          onCancel={onCancel}
        />,
      )
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Unsaved Changes')
    expect(document.activeElement).toBe(button('Keep editing'))

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onCancel).toHaveBeenCalledOnce()
  })

  function button(label: string) {
    const found = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(found).toBeDefined()
    return found as HTMLButtonElement
  }

  async function click(label: string) {
    await act(async () => {
      button(label).dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})
