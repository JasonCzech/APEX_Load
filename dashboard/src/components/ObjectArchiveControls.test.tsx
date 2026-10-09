// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ObjectArchiveControls from './ObjectArchiveControls'
import { closeUndoNoticeForTests } from './ux/undoNotice'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))

describe('ObjectArchiveControls', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); act(() => closeUndoNoticeForTests()) })

  it('offers Undo after archiving, restoring with the archived version', async () => {
    request.mockResolvedValueOnce({ updated_at: '2026-10-08T10:00:01Z' }).mockResolvedValueOnce({})
    const onChanged = vi.fn()
    await act(async () => root.render(<ObjectArchiveControls resourceType="script" resourceId="s1" resourceName="Checkout" updatedAt="2026-10-08T10:00:00Z" canManage onChanged={onChanged} />))
    const archive = container.querySelector('button')!
    expect(archive.getAttribute('aria-label')).toBe('Archive Checkout')
    await act(async () => archive.click())
    const notice = document.body.querySelector('.ux-undo-notice')
    expect(notice?.getAttribute('role')).toBe('status')
    expect(notice?.textContent).toContain('Archived script “Checkout”.')
    await act(async () => (Array.from(notice!.querySelectorAll('button')).find(button => button.textContent === 'Undo') as HTMLButtonElement).click())
    expect(request).toHaveBeenLastCalledWith('/scripts/s1/restore', { method: 'POST', body: JSON.stringify({ updated_at: '2026-10-08T10:00:01Z' }) })
    expect(onChanged).toHaveBeenCalledTimes(2)
    expect(document.body.querySelector('.ux-undo-notice')).toBeNull()
  })

  it('in a row menu, renders only the button and reports a failure in a notice', async () => {
    request.mockRejectedValueOnce(new Error('version conflict'))
    const onChanged = vi.fn()
    await act(async () => root.render(<ObjectArchiveControls inMenu resourceType="scenario" resourceId="sc1" resourceName="Peak" archivedAt="2026-10-08T10:00:00Z" updatedAt="2026-10-08T10:00:00Z" canManage onChanged={onChanged} />))
    expect(container.querySelector('.object-archive-controls')).toBeNull()
    expect(container.textContent).toBe('Restore')
    await act(async () => container.querySelector('button')!.click())
    const notice = document.body.querySelector('.ux-undo-notice.error')
    expect(notice?.getAttribute('role')).toBe('alert')
    expect(notice?.textContent).toContain('Could not restore scenario “Peak”: version conflict')
    expect(notice?.textContent).not.toContain('Undo')
    expect(onChanged).not.toHaveBeenCalled()
  })

  it('in a row menu, renders nothing for readers', async () => {
    await act(async () => root.render(<ObjectArchiveControls inMenu resourceType="script" resourceId="s1" updatedAt="v" canManage={false} onChanged={vi.fn()} />))
    expect(container.innerHTML).toBe('')
  })
})
