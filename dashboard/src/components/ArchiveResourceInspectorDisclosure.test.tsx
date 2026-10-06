// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import ArchiveResourceInspectorDisclosure from './ArchiveResourceInspectorDisclosure'
import { archiveResourceInspectorOwner } from './archiveResourceInspectorOwner'
import { resourcePageFixture } from './browserResourcePage.testFixtures'
import type { RunArchiveReport, TestSummary } from '../types'
const props = () => ({ hasArchive: true, test: { id: 'run-one', project_id: 'project-one', started_at: '2026-10-05T12:00:00Z' } as TestSummary,
  report: { manifest: { test_id: 'run-one', created_at: '2026-10-05T12:00:01Z', provenance: { project_id: 'project-one' }, transactions_file: 'transactions.jsonl' } } as RunArchiveReport })

describe('archive resource inspector disclosure owner', () => {
  it.each(['missing archive', 'missing project', 'missing report', 'foreign report run', 'foreign report project', 'foreign summary archive', 'invalid project', 'unpaired surrogate', 'oversized project'])('refuses %s without a resource read', async caseName => {
    const value = props()
    if (caseName === 'missing archive') value.hasArchive = false
    if (caseName === 'missing project') delete value.test.project_id
    if (caseName === 'missing report') value.report = null as unknown as RunArchiveReport
    if (caseName === 'foreign report run') value.report.manifest.test_id = 'foreign'
    if (caseName === 'foreign report project') value.report.manifest.provenance!.project_id = 'foreign'
    if (caseName === 'foreign summary archive') value.test.archive = { test_id: 'foreign' } as never
    if (caseName === 'invalid project') value.test.project_id = 'project\0one'
    if (caseName === 'unpaired surrogate') value.test.project_id = 'project\ud800'
    if (caseName === 'oversized project') value.test.project_id = 'p'.repeat(129)
    expect(archiveResourceInspectorOwner(value)).toBeNull()
    const node = document.createElement('div'); const root = createRoot(node); const load = vi.fn(async () => resourcePageFixture())
    try {
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...value} loadPage={load} />))
      expect(node.querySelector('button')!.disabled).toBe(true); expect(load).not.toHaveBeenCalled()
      expect(node.textContent).toContain('matching archived run, project and report')
    } finally { await act(async () => root.unmount()) }
  })
  it('accepts paired historical project absence without granting source proof and performs reads only when opened', async () => {
    const value = props(); delete value.report.manifest.provenance!.project_id
    expect(archiveResourceInspectorOwner(value)).toMatchObject({ testID: 'run-one', projectID: 'project-one' })
    const node = document.createElement('div'); const root = createRoot(node); const load = vi.fn(async () => resourcePageFixture())
    try {
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...value} loadPage={load} />))
      expect(load).not.toHaveBeenCalled()
      await act(async () => node.querySelector('button')!.click())
      await act(async () => { await import('./BrowserResourceInspector') })
      expect(load).toHaveBeenCalledTimes(1); expect(node.textContent).toContain('80 retained entry observations')
      const close = Array.from(node.querySelectorAll('button')).find(button => button.textContent === 'Close retained resource inspector')!
      await act(async () => close.click()); expect(node.querySelector('[aria-label="Retained archive resource inspector"]')).toBeNull()
      expect(load).toHaveBeenCalledTimes(1)
    } finally { await act(async () => root.unmount()) }
  })
  it('retires disclosure/read ownership on source, run and project changes while preserving equivalent refreshes', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const load = vi.fn(async () => resourcePageFixture()); const value = props()
    try {
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...value} loadPage={load} />))
      await act(async () => node.querySelector('button')!.click())
      await act(async () => { await import('./BrowserResourceInspector') })
      expect(load).toHaveBeenCalledTimes(1)
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...structuredClone(value)} loadPage={load} />))
      expect(node.textContent).toContain('Close retained resource inspector'); expect(load).toHaveBeenCalledTimes(1)
      const changed = structuredClone(value); changed.report.manifest.created_at = '2026-10-05T12:00:02Z'
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...changed} loadPage={load} />))
      expect(node.textContent).toContain('Inspect retained archive resources'); expect(node.querySelector('[aria-label="Retained archive resource inspector"]')).toBeNull()
      await act(async () => node.querySelector('button')!.click())
      await act(async () => { await import('./BrowserResourceInspector') })
      expect(load).toHaveBeenCalledTimes(2)
      const other = props(); other.test.id = other.report.manifest.test_id = 'run-two'
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...other} loadPage={load} />))
      expect(node.querySelector('[aria-label="Retained archive resource inspector"]')).toBeNull(); expect(load).toHaveBeenCalledTimes(2)
      const moved = props(); moved.test.project_id = moved.report.manifest.provenance!.project_id = 'project-two'
      await act(async () => root.render(<ArchiveResourceInspectorDisclosure {...moved} loadPage={load} />))
      expect(node.querySelector('[aria-label="Retained archive resource inspector"]')).toBeNull(); expect(load).toHaveBeenCalledTimes(2)
    } finally { await act(async () => root.unmount()) }
  })
})
