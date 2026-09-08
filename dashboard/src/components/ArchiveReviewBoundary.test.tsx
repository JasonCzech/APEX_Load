// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import ArchiveReviewBoundary from './ArchiveReviewBoundary'

it('contains a failed review and preserves recovery and the surrounding queue', () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const retry = vi.fn()
  const logging = vi.spyOn(console, 'error').mockImplementation(() => {})
  function Broken(): never { throw new Error('Invalid review data') }
  const suppressExpected = (event: ErrorEvent) => { if (event.error?.message === 'Invalid review data') event.preventDefault() }
  window.addEventListener('error', suppressExpected)
  try {
    act(() => root.render(<><button>Other archive</button><ArchiveReviewBoundary key="first" archiveName="broken.zip" onRetry={retry}><Broken /></ArchiveReviewBoundary></>))
    expect(host.textContent).toContain('Other archive')
    expect(host.querySelector('[role="alert"]')?.textContent).toContain('broken.zip')
    expect(host.textContent).not.toContain('application may have been updated')
    act(() => Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Validate this archive again')?.click())
    expect(retry).toHaveBeenCalledOnce()
    act(() => root.render(<><button>Other archive</button><ArchiveReviewBoundary key="first" archiveName="broken.zip" retryDisabled onRetry={retry}><Broken /></ArchiveReviewBoundary></>))
    const retryButton = Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Validate this archive again')!
    expect(retryButton.disabled).toBe(true)
    act(() => retryButton.click())
    expect(retry).toHaveBeenCalledOnce()
    act(() => root.render(<ArchiveReviewBoundary key="second" archiveName="good.zip" onRetry={retry}><p>Valid review</p></ArchiveReviewBoundary>))
    expect(host.textContent).toBe('Valid review')
  } finally { act(() => root.unmount()); logging.mockRestore(); window.removeEventListener('error', suppressExpected) }
})
