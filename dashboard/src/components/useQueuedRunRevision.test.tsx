// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TestSummary } from '../types'
import { queuedRunRevisionScope, useQueuedRunRevision } from './useQueuedRunRevision'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const source = (): TestSummary => ({ id: 'queued-a', project_id: 'project-a', created_at: '2026-10-05T00:00:00Z', status: 'QUEUED', name: 'Queued A', duration: '1m', total_vusers: 1, runtime_settings: { results: { property_analytics: [{ name: 'category', extraction: 'category', values: ['original'] }] } }, groups: [{ name: 'orders', script_id: 'script', script_name: 'Script', protocol: 'http', vusers: 1 }] })
const admission = (run: TestSummary | null = source()) => ({ run, routeId: run?.id, projectId: 'project-a', applicationId: 'application-a', releaseId: 'release-a', resolvedStatus: 'QUEUED', canManageTests: true, canSelectRun: true, active: true, prototypeMode: false })

describe('queued run revision scope', () => {
  it('admits only exact canonical unstarted QUEUED ownership with verified permission', () => {
    expect(queuedRunRevisionScope(admission())).not.toBe('')
    for (const delta of [{ canManageTests: false }, { canSelectRun: false }, { active: false }, { prototypeMode: true }, { routeId: 'other-run' }, { projectId: 'other-project' }, { projectId: undefined }, { resolvedStatus: 'PENDING' }, { resolvedStatus: 'RUNNING' }, { run: null }, { run: { ...source(), status: 'PENDING' as const } }, { run: { ...source(), started_at: '2026-10-05T00:00:01Z' } }, { run: { ...source(), ended_at: '2026-10-05T00:00:02Z' } }]) expect(queuedRunRevisionScope({ ...admission(), ...delta })).toBe('')
    expect(queuedRunRevisionScope({ ...admission(), applicationId: 'other-application' })).not.toBe(queuedRunRevisionScope(admission()))
    expect(queuedRunRevisionScope({ ...admission(), releaseId: 'other-release' })).not.toBe(queuedRunRevisionScope(admission()))
  })
})

describe('queued run revision owner', () => {
  let container: HTMLDivElement
  let root: Root
  let read: ReturnType<typeof vi.fn<(id: string) => Promise<TestSummary>>>
  let selected: ReturnType<typeof vi.fn<(id: string) => void>>
  let current: ReturnType<typeof useQueuedRunRevision>
  function Harness({ allowed = true, run = source(), project = 'project-a', resolved = 'QUEUED' }) {
    current = useQueuedRunRevision(queuedRunRevisionScope({ ...admission(run), projectId: project, resolvedStatus: resolved, canManageTests: allowed }), run, read, selected)
    return <><button onClick={() => { void current.open() }}>Open</button><p>{current.error}</p>{current.snapshot && <pre>{JSON.stringify(current.snapshot)}</pre>}</>
  }
  beforeEach(() => {
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
    read = vi.fn().mockResolvedValue(source()); selected = vi.fn()
  })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })
  const render = async (props: Parameters<typeof Harness>[0] = {}) => { await act(async () => root.render(<Harness {...props} />)) }
  const open = async () => { await act(async () => container.querySelector('button')!.click()) }

  it('opens under the actual application StrictMode effect replay', async () => {
    await act(async () => root.render(<StrictMode><Harness /></StrictMode>))
    await open()
    expect(read).toHaveBeenCalledExactlyOnceWith('queued-a')
    expect(current.snapshot?.id).toBe('queued-a')
  })
  it('re-reads canonical source and owns its complete snapshot without aliases', async () => {
    const canonical = source(); canonical.name = 'Fresh canonical'; read.mockResolvedValue(canonical)
    await render(); await open()
    expect(read).toHaveBeenCalledExactlyOnceWith('queued-a')
    expect(current.snapshot?.name).toBe('Fresh canonical')
    canonical.runtime_settings!.results!.property_analytics![0].values[0] = 'later external mutation'
    expect(current.snapshot?.runtime_settings?.results?.property_analytics?.[0].values).toEqual(['original'])
    await act(async () => current.created('new-copy'))
    expect(selected).toHaveBeenCalledExactlyOnceWith('new-copy'); expect(current.snapshot).toBeNull()
  })
  it.each(['id', 'project', 'incarnation', 'started', 'pending', 'status'])('refuses a fresh read whose %s changed', async kind => {
    const fresh = source()
    if (kind === 'id') fresh.id = 'other-run'
    if (kind === 'project') fresh.project_id = 'other-project'
    if (kind === 'incarnation') fresh.created_at = '2026-10-06T00:00:00Z'
    if (kind === 'started') fresh.started_at = '2026-10-05T00:00:01Z'
    if (kind === 'pending') fresh.status = 'PENDING'
    if (kind === 'status') fresh.status = 'RUNNING'
    read.mockResolvedValue(fresh); await render(); await open()
    expect(current.snapshot).toBeNull(); expect(current.error).toContain('source changed or has started'); expect(selected).not.toHaveBeenCalled()
  })
  it('does not adopt a late read after an A→B→A scope switch', async () => {
    let resolve!: (run: TestSummary) => void
    read.mockReturnValue(new Promise<TestSummary>(done => { resolve = done }))
    await render(); await open(); expect(current.loading).toBe(true)
    await render({ project: 'project-b' }); await render()
    await act(async () => resolve(source()))
    expect(current.snapshot).toBeNull(); expect(current.loading).toBe(false); expect(selected).not.toHaveBeenCalled()
  })
  it('permission revocation and restoration clears the old owner and late navigation', async () => {
    await render(); await open(); const lateCreated = current.created
    await render({ allowed: false }); await render()
    await act(async () => lateCreated('late-copy'))
    expect(current.snapshot).toBeNull(); expect(selected).not.toHaveBeenCalled()
  })
  it('an old modal close cannot dismiss a newer owner for the same run', async () => {
    await render(); await open(); const lateClose = current.close; const firstIncarnation = current.incarnation
    await act(async () => current.close()); await open()
    await act(async () => lateClose())
    expect(current.snapshot?.id).toBe('queued-a')
    expect(current.incarnation).not.toBe(firstIncarnation)
    await act(async () => current.created('current-copy'))
    expect(selected).toHaveBeenCalledExactlyOnceWith('current-copy')
  })
  it('closes on a source status change and rejects a stale modal response', async () => {
    await render(); await open(); const lateCreated = current.created
    await render({ run: { ...source(), status: 'RUNNING', started_at: '2026-10-05T00:00:01Z' }, resolved: 'RUNNING' })
    await act(async () => lateCreated('late-copy'))
    expect(current.snapshot).toBeNull(); expect(selected).not.toHaveBeenCalled()
  })
  it('close and unmount fence an outstanding read', async () => {
    let resolve!: (run: TestSummary) => void
    read.mockReturnValue(new Promise<TestSummary>(done => { resolve = done }))
    await render(); await open(); await act(async () => current.close()); await act(async () => resolve(source()))
    expect(current.snapshot).toBeNull()
    read.mockReturnValue(new Promise<TestSummary>(done => { resolve = done }))
    await open(); await act(async () => root.unmount()); await act(async () => resolve(source()))
    await act(async () => current.open())
    expect(read).toHaveBeenCalledTimes(2)
    expect(selected).not.toHaveBeenCalled()
    root = createRoot(container)
  })
})
