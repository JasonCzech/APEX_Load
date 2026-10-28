// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, it, expect, vi } from 'vitest'
import type { StoredScenario } from '../types'
import NewTestScheduleCatalog from './NewTestScheduleCatalog'
import { formatScheduleTimestampInZone } from './newTestScheduleFormat'
const api = { listScenarioSchedules: vi.fn() }
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLDivElement
let root: Root
const scenarios = [{ id: 'one', name: 'Same', ref_key: 'SCN-1' }, { id: 'two', name: 'Same', ref_key: 'SCN-2' }] as StoredScenario[]
beforeEach(() => { vi.resetAllMocks(); container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
it('loads every scenario, distinguishes duplicate names and renders next runs in the named zone', async () => {
  const next = '2026-09-14T09:00:00Z'
  api.listScenarioSchedules.mockImplementation(async id => ({ schedules: [{ id: `${id}-schedule`, scenario_id: id, name: 'Daily', next_run_at: next, timezone: 'Asia/Tokyo', enabled: true, analysis_policy_selections: id === 'one' ? { browser: { id: 'bp-1', revision: 2 }, journey: { id: 'jp-1', revision: 1 } } : undefined }] }))
  const select = vi.fn()
  await act(async () => root.render(<NewTestScheduleCatalog scenarios={scenarios} projectId="project" revision={0} onSelectScenario={select} />))
  expect(api.listScenarioSchedules.mock.calls.map(call => call[0])).toEqual(['one', 'two'])
  expect(container.textContent).toContain('Same · SCN-1')
  expect(container.textContent).toContain('Same · SCN-2')
  expect(container.textContent).toContain('Browser policy r2 · Journey policy r1')
  expect(container.textContent).toContain(formatScheduleTimestampInZone(next, 'Asia/Tokyo'))
  await act(async () => { container.querySelectorAll<HTMLButtonElement>('td button')[1].click() })
  expect(select).toHaveBeenCalledWith('two')
})
it('reports partial failures and hides prior-project schedules during a scope change', async () => {
  api.listScenarioSchedules.mockImplementation(async id => { if (id === 'two') throw new Error('Forbidden'); return { schedules: [{ id: 'schedule', scenario_id: id, name: 'Old project schedule', enabled: false }] } })
  await act(async () => root.render(<NewTestScheduleCatalog scenarios={scenarios} projectId="old" revision={0} onSelectScenario={vi.fn()} />))
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('Same · SCN-2')
  expect(container.textContent).toContain('Old project schedule')
  api.listScenarioSchedules.mockImplementation(() => new Promise(() => {}))
  await act(async () => root.render(<NewTestScheduleCatalog scenarios={scenarios} projectId="new" revision={0} onSelectScenario={vi.fn()} />))
  expect(container.textContent).not.toContain('Old project schedule')
  expect(container.textContent).toContain('Loading project schedules')
})
