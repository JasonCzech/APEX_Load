// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import ArchiveResourceInspectorDisclosure from './ArchiveResourceInspectorDisclosure'
import LazyLoadBoundary from './LazyLoadBoundary'
import type { RunArchiveReport, TestSummary } from '../types'

const chunk = vi.hoisted(() => {
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release, started: 0, rejected: false }
})
vi.mock('./BrowserResourceInspector', async () => {
  chunk.started++
  await chunk.ready
  chunk.rejected = true
  throw new Error('inspector chunk unavailable')
})

it('keeps the archived report mounted when the optional inspector chunk fails', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  const load = vi.fn()
  const expectedError = (event: ErrorEvent) => {
    if (event.error instanceof Error && event.error.message === 'inspector chunk unavailable') event.preventDefault()
  }
  window.addEventListener('error', expectedError)
  const diagnostic = vi.spyOn(console, 'error').mockImplementation(() => undefined)
  try {
    await act(async () => root.render(
      <LazyLoadBoundary title="Execution route failed.">
        <article><h2>Archived run report</h2><ArchiveResourceInspectorDisclosure hasArchive
          test={{ id: 'run-one', project_id: 'project-one', started_at: '2026-10-05T12:00:00Z' } as TestSummary}
          report={{ manifest: { test_id: 'run-one', created_at: '2026-10-05T12:00:01Z', provenance: { project_id: 'project-one' }, transactions_file: 'transactions.jsonl' } } as RunArchiveReport}
          loadPage={load} /></article>
      </LazyLoadBoundary>,
    ))
    expect(chunk.started).toBe(0)
    await act(async () => node.querySelector('button')!.click())
    expect(node.textContent).toContain('Loading retained resource inspector…')
    await act(async () => {
      chunk.release()
      await vi.waitFor(() => expect(chunk.rejected).toBe(true))
    })
    expect(node.querySelector('h2')!.textContent).toBe('Archived run report')
    expect(node.querySelector('[role="alert"]')!.textContent).toContain('Retained resource inspector could not be loaded.')
    expect(node.textContent).not.toContain('Execution route failed.')
    expect(Array.from(node.querySelectorAll('button')).some(button => button.textContent === 'Reload and retry')).toBe(true)
    expect(load).not.toHaveBeenCalled()
    const close = Array.from(node.querySelectorAll('button')).find(button => button.textContent === 'Close retained resource inspector')!
    await act(async () => close.click())
    expect(node.querySelector('[role="alert"]')).toBeNull()
    expect(node.querySelector('h2')!.textContent).toBe('Archived run report')
    expect(load).not.toHaveBeenCalled()
  } finally {
    await act(async () => {
      chunk.release()
      if (chunk.started) await vi.waitFor(() => expect(chunk.rejected).toBe(true))
    })
    await act(async () => root.unmount())
    window.removeEventListener('error', expectedError)
    diagnostic.mockRestore()
  }
})
