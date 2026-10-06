// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import IssueDetailsDialog from './IssueDetailsDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true


function FocusHandoffHarness({ ownsDestinationFocus }: { ownsDestinationFocus: boolean }) {
  const [open, setOpen] = useState(false)
  return <><button data-opener onClick={() => setOpen(true)}>Open issues</button>{open && <IssueDetailsDialog title="Repair" issues={[{ id: 'one', severity: 'error', title: 'Repair target', ownsDestinationFocus, onSelect: () => undefined }]} onClose={() => setOpen(false)} />}</>
}

describe('IssueDetailsDialog', () => {
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

  it('renders issue context and closes with Escape', async () => {
    const onClose = vi.fn()
    act(() => {
      root.render(
        <IssueDetailsDialog
          title="Import blockers"
          issues={[{
            id: 'blocked-data',
            severity: 'blocker',
            title: 'Missing data source',
            description: 'Upload orders.csv before replay.',
            count: 2,
            source: 'Data source fit',
          }]}
          onClose={onClose}
        />,
      )
    })

    expect(document.body.querySelector('[role="dialog"]')?.getAttribute('aria-modal')).toBe('true')
    expect(document.body.textContent).toContain('Upload orders.csv before replay.')
    expect(document.body.textContent).toContain('Data source fit')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it.each([false, true])('hands focus to a destination only for an explicitly marked selection: %s', async ownsDestinationFocus => {
    await act(async () => { root.render(<FocusHandoffHarness ownsDestinationFocus={ownsDestinationFocus} />) })
    const opener = container.querySelector<HTMLButtonElement>('[data-opener]')!
    opener.focus(); await act(async () => { opener.click() })
    const repair = document.querySelector<HTMLButtonElement>('.issue-details-item.selectable')!
    repair.focus(); await act(async () => { repair.click() })
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(ownsDestinationFocus ? document.body : opener)
  })

  it.each(['Close', 'Escape'])('preserves normal return focus for %s even when an issue owns its selection destination', async exit => {
    await act(async () => { root.render(<FocusHandoffHarness ownsDestinationFocus />) })
    const opener = container.querySelector<HTMLButtonElement>('[data-opener]')!
    opener.focus(); await act(async () => { opener.click() })
    const close = document.querySelector<HTMLButtonElement>('[role="dialog"] .modal-header button')!
    close.focus()
    await act(async () => { if (exit === 'Escape') document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); else close.click() })
    expect(document.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(opener)
  })

  it('closes before selecting a navigable issue', () => {
    const calls: string[] = []
    act(() => {
      root.render(
        <IssueDetailsDialog
          title="Script errors"
          issues={[{
            id: 'action-error',
            severity: 'error',
            title: 'Target is required',
            onSelect: () => calls.push('select'),
          }]}
          onClose={() => calls.push('close')}
        />,
      )
    })

    act(() => {
      ;(document.body.querySelector('.issue-details-item.selectable') as HTMLButtonElement).click()
    })
    expect(calls).toEqual(['close', 'select'])
  })
})
