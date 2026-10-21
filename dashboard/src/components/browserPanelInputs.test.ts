import { beforeEach, expect, it, vi } from 'vitest'
import type { DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
import { freezeBrowserPanelDefinition } from './browserPanelInputs'
const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request }))
const definition = (): Pick<DashboardDefinition, 'name' | 'tabs'> => ({ name: 'Pinned comparison', tabs: [{ id: 'main', title: 'Main', panels: [{ id: 'browser', title: 'Browser', kind: 'line', x: 0, y: 0, w: 24, h: 8, query: { query_version: 4, browser_tuple_mode: 'separate', population: 'browser_document', metric: 'document_lcp', aggregation: 'p75', comparison_run_id: 'baseline', end_seconds: 60, bucket_seconds: 10 } }] }] })
const pins = () => ({ version: 1 as const, query_sha256: 'a'.repeat(64), current: { run_id: 'current', manifest_sha256: 'b'.repeat(64), tuple_ids: ['c'.repeat(64)] }, baseline: { run_id: 'baseline', manifest_sha256: 'd'.repeat(64), tuple_ids: ['c'.repeat(64)] } })
beforeEach(() => { request.mockReset() })
it('retains evaluated pins without mutating the draft', async () => {
 const source = definition(); const returned = pins(); request.mockResolvedValue({ browser_inputs: returned })
 const result = await freezeBrowserPanelDefinition('current', source)
 expect(result.tabs[0].panels[0].query?.browser_inputs).toEqual(returned)
 expect(source.tabs[0].panels[0].query?.browser_inputs).toBeUndefined()
 expect(request).toHaveBeenCalledWith('/tests/current/archive/native-series', expect.objectContaining({ method: 'POST', timeoutMs: 35000 }))
 expect(JSON.parse(request.mock.calls[0][1].body)).not.toHaveProperty('browser_inputs')
})
it('keeps reusable queries without reading an archive', async () => {
 const source = definition(); source.tabs[0].panels[0].query!.query_version = 3
 expect(await freezeBrowserPanelDefinition(undefined, source)).toEqual(source)
 expect(request).not.toHaveBeenCalled()
})
it.each(['missing', 'wrong current', 'wrong baseline', 'future version'])('refuses %s returned pins', async scenario => {
 const returned = pins()
 if (scenario === 'wrong current') returned.current.run_id = 'other'
 if (scenario === 'wrong baseline') returned.baseline.run_id = 'other'
 request.mockResolvedValue({ browser_inputs: scenario === 'missing' ? undefined : scenario === 'future version' ? { ...returned, version: 2 } : returned })
 await expect(freezeBrowserPanelDefinition('current', definition())).rejects.toThrow('matching immutable browser inputs')
})
it('refuses missing current identity and oversized saves without requests', async () => {
 await expect(freezeBrowserPanelDefinition(undefined, definition())).rejects.toThrow('Select an archived run')
 const source = definition(); source.tabs[0].panels = Array.from({ length: 21 }, () => structuredClone(source.tabs[0].panels[0]))
 await expect(freezeBrowserPanelDefinition('current', source)).rejects.toThrow('at most 20')
 expect(request).not.toHaveBeenCalled()
})
it('keeps preview failures as failures', async () => {
 request.mockRejectedValue(new Error('Archive unavailable'))
 await expect(freezeBrowserPanelDefinition('current', definition())).rejects.toThrow('Archive unavailable')
})

it('refuses a late preview response after navigation cancellation', async () => {
 let resolve!: (value: { browser_inputs: ReturnType<typeof pins> }) => void
 request.mockReturnValue(new Promise(done => { resolve = done }))
 const abort = new AbortController()
 const pending = freezeBrowserPanelDefinition('current', definition(), abort.signal)
 abort.abort()
 resolve({ browser_inputs: pins() })
 await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
})
