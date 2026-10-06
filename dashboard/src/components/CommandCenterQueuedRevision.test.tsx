// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CommandCenterPrototype from './CommandCenterPrototype'
import type { TestSummary, TestGroupRuntimeSettingsUpdateResult } from '../types'

const loader = vi.hoisted(() => ({ release: undefined as (() => void) | undefined, requested: false, mounts: 0, close: undefined as (() => void) | undefined, plannedClose: undefined as (() => void) | undefined, plannedSaved: undefined as ((result: TestGroupRuntimeSettingsUpdateResult) => void) | undefined }))
vi.mock('./TestExecutionScenarioEditor', async () => {
  loader.requested = true
  await new Promise<void>(resolve => { loader.release = resolve })
  const { useState } = await import('react')
  const QueuedEditorFixture = ({ test, onClose, onSelectTest }: { test: TestSummary; onClose: () => void; onSelectTest: (id: string) => void }) => {
    const [draftName] = useState(() => { loader.mounts++; return test.name })
    loader.close = onClose
    return <section data-testid="queued-editor"><span>{draftName}</span><button onClick={onClose}>Close owned copy</button><button onClick={() => onSelectTest('new-copy')}>Complete owned copy</button></section>
  }
  return { default: QueuedEditorFixture }
})
vi.mock('./GroupRuntimeSettingsDialog', () => ({ default: ({ testId, groupName, onClose, onSaved }: { testId: string; groupName: string; onClose: () => void; onSaved: (result: TestGroupRuntimeSettingsUpdateResult) => void }) => {
  loader.plannedClose = onClose
  loader.plannedSaved = onSaved
  return <section data-testid="planned-settings">{testId}:{groupName}</section>
} }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const fixture = (): TestSummary => ({ id: 'queued', name: 'First canonical source', project_id: 'project', created_at: '2026-10-05T00:00:00Z', status: 'QUEUED', total_vusers: 1, duration: '1m', groups: [{ name: 'orders', script_id: 'script', script_name: 'HTTP script', protocol: 'http', vusers: 1 }] })

describe('Command Center queued-copy lazy owner', () => {
  let root: Root
  let container: HTMLDivElement
  let canonical: TestSummary
  let selected: ReturnType<typeof vi.fn<(id: string) => void>>
  beforeEach(() => {
    canonical = fixture(); selected = vi.fn(); loader.mounts = 0
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
      const path = new URL(url, 'http://localhost').pathname
      const body = path === '/api/v1/tests/queued' ? canonical : path === '/api/v1/tests' ? { tests: [canonical], count: 1 } : {}
      return Promise.resolve(new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } }))
    })
  })
  afterEach(async () => { loader.release?.(); await act(async () => root.unmount()); container.remove(); vi.restoreAllMocks() })
  const render = async (allowed = true) => { await act(async () => root.render(<CommandCenterPrototype testId="queued" projectId="project" initialTest={canonical} canManageTests={allowed} onSelectRun={selected} />)) }
  const button = (text: string) => { const found = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes(text)); expect(found, text).toBeDefined(); return found! }
  const open = async () => { await act(async () => button('Create revised queued copy').click()) }
  it('does not mount a late lazy editor after permission retirement', async () => {
    await render(); await open(); expect(loader.requested).toBe(true)
    expect(container.textContent).toContain('Loading queued-run editor')
    await render(false)
    await act(async () => loader.release?.())
    expect(container.querySelector('[data-testid="queued-editor"]')).toBeNull()
    expect(loader.mounts).toBe(0)
    expect(selected).not.toHaveBeenCalled()
  })
  it('remounts the captured editor incarnation on rapid close/reopen with an immediately resolved read', async () => {
    await render(); await open()
    expect(container.querySelector('[data-testid="queued-editor"]')?.textContent).toContain('First canonical source')
    const oldClose = loader.close!
    canonical = { ...canonical, name: 'Second canonical source' }
    await act(async () => { oldClose(); button('Create revised queued copy').click() })
    expect(container.querySelector('[data-testid="queued-editor"]')?.textContent).toContain('Second canonical source')
    expect(loader.mounts).toBe(2)
    await act(async () => oldClose())
    expect(container.querySelector('[data-testid="queued-editor"]')).not.toBeNull()
    await act(async () => button('Complete owned copy').click())
    expect(selected).toHaveBeenCalledExactlyOnceWith('new-copy')
    expect(container.querySelector('[data-testid="queued-editor"]')).toBeNull()
  })
  it.each(['QUEUED', 'RESERVED'] as const)('opens configured group settings for a canonical %s plan without fabricated load metrics', async status => {
    canonical = { ...fixture(), status }
    await render()
    const plan = container.querySelector('[aria-label="Planned run"]')!
    expect(plan.textContent).toContain('Run not started')
    expect(plan.textContent).not.toContain('0.0 TPS')
    await act(async () => button('orders · Runtime Settings').click())
    expect(container.querySelector('[data-testid="planned-settings"]')?.textContent).toBe('queued:orders')
    await render(false)
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
    await render()
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
  })
  it('retires planned settings across project ABA and fences late callbacks from a former modal', async () => {
    const renderProject = async (project: string) => { await act(async () => root.render(<CommandCenterPrototype testId="queued" projectId={project} initialTest={canonical} canManageTests onSelectRun={selected} />)) }
    await renderProject('project')
    await act(async () => button('orders · Runtime Settings').click())
    const oldClose = loader.plannedClose!
    const oldSaved = loader.plannedSaved!
    await renderProject('other-project')
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
    await renderProject('project')
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
    await act(async () => button('orders · Runtime Settings').click())
    expect(container.querySelector('[data-testid="planned-settings"]')).not.toBeNull()
    await act(async () => { oldClose(); oldSaved({ test_id: 'queued', group: 'former-group', fields: ['log'], revision: 'old-revision', no_changes: false }) })
    expect(container.querySelector('[data-testid="planned-settings"]')).not.toBeNull()
    expect(container.textContent).not.toContain('former-group')
    await act(async () => loader.plannedClose!())
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
  })

  it.each(['viewer', 'foreign project', 'started', 'historical', 'prototype', 'inactive'] as const)('refuses planned group settings for %s ownership', async reason => {
    if (reason === 'started') canonical = { ...fixture(), started_at: '2026-10-05T00:01:00Z' }
    if (reason === 'historical') canonical = { ...fixture(), status: 'COMPLETE' }
    await act(async () => root.render(<CommandCenterPrototype testId="queued" projectId={reason === 'foreign project' ? 'other-project' : 'project'} initialTest={canonical} canManageTests={reason !== 'viewer'} prototypeMode={reason === 'prototype'} active={reason !== 'inactive'} onSelectRun={selected} />))
    expect(container.querySelector('[aria-label="Configured group runtime settings"]')).toBeNull()
    expect(container.querySelector('[data-testid="planned-settings"]')).toBeNull()
  })

})
