// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import IssueDetailsDialog from './IssueDetailsDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
