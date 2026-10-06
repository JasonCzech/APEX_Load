// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { resourcePageFixture } from './browserResourcePage.testFixtures'
import type { RunArchiveReport, TestSummary } from '../types'

// Gate only the module boundary. After release, exercise the real inspector,
// decoder and read hook so a retired disclosure cannot resurrect a read.
const chunk = vi.hoisted(() => {
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release, started: 0, completed: false }
})
vi.mock('./BrowserResourceInspector', async () => {
  chunk.started++
  await chunk.ready
  const module = await vi.importActual('./BrowserResourceInspector')
  chunk.completed = true
  return module
})

function scope(run = 'run-one') {
  return {
    hasArchive: true,
    test: { id: run, project_id: 'project-one', started_at: '2026-10-05T12:00:00Z' } as TestSummary,
    report: { manifest: { test_id: run, created_at: '2026-10-05T12:00:01Z', provenance: { project_id: 'project-one' }, transactions_file: 'transactions.jsonl' } } as RunArchiveReport,
  }
}

it('loads the inspector only on disclosure and retires a pending chunk across close and run changes', async () => {
  let disclosure: typeof import('./ArchiveResourceInspectorDisclosure') | undefined
  const imported = import('./ArchiveResourceInspectorDisclosure').then(module => { disclosure = module; return module })
  const node = document.createElement('div')
  const root = createRoot(node)
  const load = vi.fn(async (testID: string, projectID: string) => ({ ...resourcePageFixture(), test_id: testID, project_id: projectID }))
  try {
    // An eager module import would wait on chunk.ready and fail this assertion.
    await vi.waitFor(() => expect(disclosure).toBeDefined())
    expect(chunk.started).toBe(0)
    const Disclosure = disclosure!.default
    await act(async () => root.render(<Disclosure {...scope()} loadPage={load} />))
    expect(chunk.started).toBe(0)
    expect(load).not.toHaveBeenCalled()

    await act(async () => node.querySelector('button')!.click())
    await vi.waitFor(() => expect(chunk.started).toBe(1))
    expect(node.textContent).toContain('Loading retained resource inspector…')
    expect(node.querySelector('button')!.getAttribute('aria-expanded')).toBe('true')
    expect(load).not.toHaveBeenCalled()
    await act(async () => node.querySelector('button')!.click())
    expect(node.textContent).not.toContain('Loading retained resource inspector…')
    await act(async () => node.querySelector('button')!.click())
    await act(async () => root.render(<Disclosure {...scope('run-two')} loadPage={load} />))
    expect(node.querySelector('button')!.getAttribute('aria-expanded')).toBe('false')

    await act(async () => {
      chunk.release()
      await vi.waitFor(() => expect(chunk.completed).toBe(true))
    })
    expect(load).not.toHaveBeenCalled()
    expect(node.querySelector('[aria-label="Retained archive resource inspector"]')).toBeNull()
    await act(async () => node.querySelector('button')!.click())
    await vi.waitFor(() => expect(load).toHaveBeenCalledTimes(1))
    expect(load.mock.calls[0].slice(0, 2)).toEqual(['run-two', 'project-one'])
    expect(node.textContent).toContain('80 retained entry observations')
  } finally {
    chunk.release()
    await imported
    await act(async () => root.unmount())
    if (chunk.started) await vi.waitFor(() => expect(chunk.completed).toBe(true))
  }
})
