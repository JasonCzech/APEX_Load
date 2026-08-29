// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { TrafficProfile, TrafficProfileDeriveResponse } from '../types'
import TrafficProfileSourcePanel from './TrafficProfileSourcePanel'
import type { ScenarioUpsertPayload } from './scenarioPayloadUtils'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const point = { timestamp: '2026-08-26T00:00:00Z', value: 15 }
const analyzedSeries = { key: 'requests', name: 'requests', points: [point, { timestamp: '2026-08-26T00:00:15Z', value: 30 }] }
const defaultAnalysis = {
  source: { kind: 'prometheus' as const, prometheus: { query: 'sum(rate(requests_total[1m]))', range: { mode: 'relative' as const, lookback: '24h', step_seconds: 15 } } },
  series: [{ series: analyzedSeries, stats: { count: 2, start: point.timestamp, end: '2026-08-26T00:00:15Z', min: 15, max: 30, mean: 22.5, detected_bucket: 15_000_000_000, gaps: 0 } }],
}
const apiMock = {
  getTrafficProfileSources: vi.fn().mockResolvedValue({
    sources: [
      { kind: 'file', enabled: true, configured: true, formats: ['csv', 'xlsx'] },
      { kind: 'prometheus', enabled: true, configured: true },
      { kind: 'opensearch', enabled: false, configured: false },
    ],
    limits: { upload_bytes: 25, derive_bytes: 8, upstream_bytes: 16, points_per_series: 10_000, points_per_profile: 50_000 },
  }),
  analyzeTrafficProfile: vi.fn().mockResolvedValue(defaultAnalysis),
  deriveTrafficProfile: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))
vi.mock('recharts', () => {
  const Wrapper = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  return { CartesianGrid: Wrapper, ComposedChart: Wrapper, Line: () => null, ResponsiveContainer: Wrapper, Tooltip: Wrapper, XAxis: Wrapper, YAxis: Wrapper }
})

describe('TrafficProfileSourcePanel', () => {
  beforeEach(() => {
    apiMock.analyzeTrafficProfile.mockReset().mockResolvedValue(defaultAnalysis)
    apiMock.deriveTrafficProfile.mockReset()
  })

  it('analyzes, maps, previews, and applies a Prometheus profile without creating stages', async () => {
    const profile: TrafficProfileDeriveResponse = {
      traffic_profile: {
        version: 1,
        source: { kind: 'prometheus', prometheus: { query: 'sum(rate(requests_total[1m]))', range: { mode: 'relative', lookback: '24h', step_seconds: 15 } } },
        value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '15s', target_duration: '15s', gap_policy: 'hold',
        mappings: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
        refreshed_at: '2026-08-27T00:00:00Z',
      },
      preview: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
      scenario_draft: { name: 'Replay', duration: '15s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, stages: [], initialization: 'at_runtime' }] },
    }
    apiMock.deriveTrafficProfile.mockResolvedValue(profile)
    const onApply = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' }] } as ScenarioUpsertPayload

    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={onApply} />) })
    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const query = container.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set?.call(query, 'sum(rate(requests_total[1m]))')
      query.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.analyzeTrafficProfile).toHaveBeenCalled()
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.deriveTrafficProfile).toHaveBeenCalledWith(expect.objectContaining({ mappings: [{ series_key: 'requests', group_name: 'Checkout' }] }))
    const apply = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply production profile')
    await act(async () => { apply?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onApply).toHaveBeenCalledWith(profile)
    await act(async () => {
      root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} existingProfile={profile.traffic_profile} onApply={onApply} />)
    })
    const applied = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Production profile applied')
    expect(applied?.disabled).toBe(true)
    expect(container.textContent).toContain('Applied: this candidate is the current scenario profile.')

    await act(async () => root.unmount())
    container.remove()
  })

  it('clears worksheet and column mappings when a different file is selected', async () => {
    const existingProfile: TrafficProfile = {
      version: 1,
      source: { kind: 'file', file: { filename: 'old.csv', sheet: 'CSV', timestamp_column: 'old_time', value_columns: ['old_value'], series_column: 'old_series', timezone: 'UTC' } },
      value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '15s', target_duration: '15s', gap_policy: 'hold',
      mappings: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
      refreshed_at: '2026-08-27T00:00:00Z',
    }
    const draft = { name: 'Replay', duration: '15s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} existingProfile={existingProfile} onApply={vi.fn()} />) })

    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { configurable: true, value: [new File(['timestamp,transactions\n'], 'new.xlsx')] })
    await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const uploadCalls = apiMock.analyzeTrafficProfile.mock.calls
    const upload = uploadCalls[uploadCalls.length - 1]?.[0] as FormData
    expect(upload).toBeInstanceOf(FormData)
    expect(upload.get('sheet')).toBeNull()
    expect(upload.get('timestamp_column')).toBeNull()
    expect(upload.getAll('value_column')).toEqual([])
    expect(upload.get('series_column')).toBeNull()

    await act(async () => root.unmount())
    container.remove()
  })

  it('fans one selected workload series out to every scenario group', async () => {
    const derived: TrafficProfileDeriveResponse = {
      traffic_profile: {
        version: 1, source: defaultAnalysis.source, value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30s', target_duration: '30s', gap_policy: 'hold', refreshed_at: '2026-08-27T00:00:00Z',
        mappings: ['Checkout', 'Search'].map(group_name => ({ series_key: 'requests', series_name: 'requests', group_name, peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] })),
      },
      preview: [],
      scenario_draft: { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [] },
    }
    apiMock.deriveTrafficProfile.mockResolvedValue(derived)
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'checkout', vusers: 10 }, { name: 'Search', script_id: 'search', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout', 'Search']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect((container.querySelector('[aria-label="Apply requests to"]') as HTMLSelectElement).value).toBe('__all_groups__')
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.deriveTrafficProfile).toHaveBeenCalledWith(expect.objectContaining({
      mappings: [
        { series_key: 'requests', group_name: 'Checkout' },
        { series_key: 'requests', group_name: 'Search' },
      ],
    }))

    await act(async () => root.unmount())
    container.remove()
  })

  it('builds a composite profile from an independent file for each group', async () => {
    apiMock.analyzeTrafficProfile.mockImplementation(async (request: FormData | unknown) => {
      const filename = request instanceof FormData ? (request.get('file') as File)?.name : ''
      const group = filename.startsWith('checkout') ? 'checkout' : 'search'
      return {
        source: { kind: 'file' as const, file: { filename, sheet: 'CSV', timestamp_column: 'timestamp', value_columns: ['transactions'], timezone: 'UTC' } },
        series: [{ series: { ...analyzedSeries, key: `${group}-requests`, name: `${group} requests` }, stats: { count: 2, start: point.timestamp, end: '2026-08-26T00:00:15Z', min: 15, max: 30, mean: 22.5, detected_bucket: 15_000_000_000, gaps: 0 } }],
      }
    })
    apiMock.deriveTrafficProfile.mockResolvedValue({
      traffic_profile: { version: 1, source: { kind: 'multiple', sources: [] }, value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30s', target_duration: '30s', mappings: [], refreshed_at: '2026-08-27T00:00:00Z' },
      preview: [], scenario_draft: { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [] },
    } satisfies TrafficProfileDeriveResponse)
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'checkout', vusers: 10 }, { name: 'Search', script_id: 'search', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout', 'Search']} scenarioDraft={draft} onApply={vi.fn()} />) })
    const separate = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Separate file per group')
    await act(async () => { separate?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const inputs = Array.from(container.querySelectorAll('input[type="file"]')) as HTMLInputElement[]
    Object.defineProperty(inputs[0], 'files', { configurable: true, value: [new File(['data'], 'checkout.csv')] })
    Object.defineProperty(inputs[1], 'files', { configurable: true, value: [new File(['data'], 'search.xlsx')] })
    await act(async () => {
      inputs[0].dispatchEvent(new Event('change', { bubbles: true }))
      inputs[1].dispatchEvent(new Event('change', { bubbles: true }))
    })
    const checkoutAnalyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze Checkout')
    const searchAnalyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze Search')
    await act(async () => {
      checkoutAnalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      searchAnalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(Array.from(container.querySelectorAll('.status-badge.success')).map(item => item.textContent)).toEqual(['Ready', 'Ready'])
    const seriesSelects = Array.from(container.querySelectorAll('[aria-label^="Workload series for"]')) as HTMLSelectElement[]
    const firstSeries = seriesSelects[0].value
    await act(async () => {
      seriesSelects[0].value = ''
      seriesSelects[0].dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.querySelectorAll('.status-badge.success')).toHaveLength(1)
    await act(async () => {
      seriesSelects[0].value = firstSeries
      seriesSelects[0].dispatchEvent(new Event('change', { bubbles: true }))
    })
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const request = apiMock.deriveTrafficProfile.mock.calls[0]?.[0] as { source: TrafficProfile['source']; series: Array<{ key: string }>; mappings: Array<{ group_name: string; source?: TrafficProfile['source'] }> }
    expect(request.source.kind).toBe('multiple')
    expect(request.source.sources?.map(source => source.file?.filename)).toEqual(['checkout.csv', 'search.xlsx'])
    expect(new Set(request.series.map(series => series.key)).size).toBe(2)
    expect(request.mappings.map(mapping => [mapping.group_name, mapping.source?.file?.filename])).toEqual([['Checkout', 'checkout.csv'], ['Search', 'search.xlsx']])

    await act(async () => root.unmount())
    container.remove()
  })
})
