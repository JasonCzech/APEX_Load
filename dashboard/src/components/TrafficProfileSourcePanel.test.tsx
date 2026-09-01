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
  const Chart = ({ children, data }: { children?: ReactNode; data?: unknown[] }) => <div data-testid="profile-preview-chart" data-chart={JSON.stringify(data || [])}>{children}</div>
  const MockLine = ({ dataKey, name }: { dataKey?: string; name?: string }) => <span data-testid="profile-preview-line" data-key={dataKey} data-name={name} />
  const MockReferenceArea = ({ x1, x2, label }: { x1?: number; x2?: number; label?: { value?: string } }) => <span data-testid="profile-preview-selection" data-x1={x1} data-x2={x2} data-name={label?.value} />
  const MockXAxis = ({ domain }: { domain?: [number, number] }) => <span data-testid="profile-preview-x-axis" data-domain={JSON.stringify(domain || [])} />
  return { CartesianGrid: Wrapper, ComposedChart: Chart, Legend: Wrapper, Line: MockLine, ReferenceArea: MockReferenceArea, ResponsiveContainer: Wrapper, Tooltip: Wrapper, XAxis: MockXAxis, YAxis: Wrapper }
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
        value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '15s', target_duration: '30s', gap_policy: 'hold',
        mappings: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
        refreshed_at: '2026-08-27T00:00:00Z',
      },
      preview: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
      scenario_draft: { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, stages: [], initialization: 'at_runtime' }] },
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
    expect(container.textContent).toContain('minimum 15 TPS')
    expect(container.textContent).toContain('average 22.5 TPS')
    expect(container.textContent).toContain('peak 30 TPS')
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

  it('adds per-group estimated ART, displays sizing, and confirms guardrail replacement', async () => {
    const mapping: TrafficProfile['mappings'][number] = {
      series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30,
      points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }],
      art: { basis: 'operator_peak', source_unit: 'seconds', points: [{ offset_ms: 0, art_ms: 1000 }], peak_art_ms: 1000, estimated_peak_vusers: 30 },
    }
    const response: TrafficProfileDeriveResponse = {
      traffic_profile: {
        version: 1, source: defaultAnalysis.source, value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30s', target_duration: '30s', gap_policy: 'hold', mappings: [mapping],
        sizing: { complete: true, profile_peak_vusers: 30, static_peak_vusers: 0, estimated_peak_vusers: 30, suggested_min_vusers: 1, suggested_max_vusers: 60, recommended_generators: 2 }, refreshed_at: '2026-08-27T00:00:00Z',
      },
      preview: [mapping],
      scenario_draft: { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', goal_config: { min_vusers: 1, max_vusers: 60 }, script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' }] },
      load_model_validation: { capacity_advisory: { warnings: ['Only one generator is currently available.'] } } as TrafficProfileDeriveResponse['load_model_validation'],
    }
    apiMock.deriveTrafficProfile.mockResolvedValue(response)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    const onApply = vi.fn()
    const existingProfile: TrafficProfile = {
      ...response.traffic_profile,
      refreshed_at: '2026-08-26T00:00:00Z',
      mappings: [{
        ...mapping,
        art: { basis: 'operator_peak', source_unit: 'milliseconds', points: [{ offset_ms: 0, art_ms: 500 }], peak_art_ms: 500, estimated_peak_vusers: 15 },
      }],
      sizing: { complete: true, profile_peak_vusers: 15, static_peak_vusers: 0, estimated_peak_vusers: 15, suggested_min_vusers: 1, suggested_max_vusers: 30, recommended_generators: 1 },
    }
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const draft = { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', goal_config: { min_vusers: 5, max_vusers: 10 }, script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} existingProfile={existingProfile} onApply={onApply} />) })
    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const basis = container.querySelector('[aria-label="ART basis for Checkout"]') as HTMLSelectElement
    await act(async () => { basis.value = 'operator_peak'; basis.dispatchEvent(new Event('change', { bubbles: true })) })
    const art = container.querySelector('[aria-label="Estimated peak ART for Checkout"]') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(art, '1')
      art.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const unit = container.querySelector('[aria-label="ART unit for Checkout"]') as HTMLSelectElement
    await act(async () => { unit.value = 'seconds'; unit.dispatchEvent(new Event('change', { bubbles: true })) })
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.deriveTrafficProfile).toHaveBeenCalledWith(expect.objectContaining({ mappings: [{ series_key: 'requests', group_name: 'Checkout', art: { basis: 'operator_peak', value: 1, unit: 'seconds' } }] }))
    expect(container.textContent).toContain('Estimated peak VUsers: 30')
    expect(container.textContent).toContain('Current guardrails: 5-10 VUsers · Suggested: 1-60 VUsers')
    expect(container.textContent).toContain('2 generator(s) recommended')
    expect(container.textContent).toContain('Capacity: Only one generator is currently available.')
    expect(container.textContent).toContain('Operator estimate · 500 ms · 15 VUsers')
    expect(container.textContent).toContain('estimated peak 15 VUsers, suggested bounds 1-30, 1 generator recommendation')
    const apply = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply profile and ART guardrails')
    await act(async () => { apply?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onApply).not.toHaveBeenCalled()
    await act(async () => { apply?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('5-10'))
    expect(onApply).toHaveBeenCalledWith(response)
    confirm.mockRestore()
    await act(async () => root.unmount())
    container.remove()
  })

  it('defaults manual ART and units from the saved group evidence', async () => {
    const existingProfile: TrafficProfile = {
      version: 1,
      source: defaultAnalysis.source,
      value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30s', target_duration: '30s', gap_policy: 'hold', refreshed_at: '2026-08-27T00:00:00Z',
      mappings: [{
        series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30,
        points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }],
        art: { basis: 'operator_peak', source_unit: 'seconds', points: [{ offset_ms: 0, art_ms: 1000 }], peak_art_ms: 1000, estimated_peak_vusers: 30 },
      }],
    }
    const draft = { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', goal_config: { min_vusers: 1, max_vusers: 60 }, script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} existingProfile={existingProfile} onApply={vi.fn()} />) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect((container.querySelector('[aria-label="ART basis for Checkout"]') as HTMLSelectElement).value).toBe('operator_peak')
    expect((container.querySelector('[aria-label="Estimated peak ART for Checkout"]') as HTMLInputElement).value).toBe('1')
    expect((container.querySelector('[aria-label="ART unit for Checkout"]') as HTMLSelectElement).value).toBe('seconds')
    await act(async () => root.unmount())
    container.remove()
  })

  it('keeps the full source range visible and highlights the resolved peak window', async () => {
    const sourceStart = Date.parse('2026-08-26T00:00:00Z')
    const sourcePoints = [0, 15, 30, 45, 60].map((minutes, index) => ({
      timestamp: new Date(sourceStart + minutes * 60_000).toISOString(),
      value: [2, 20, 40, 15, 3][index],
    }))
    apiMock.analyzeTrafficProfile.mockResolvedValue({
      ...defaultAnalysis,
      series: [{
        series: { key: 'requests', name: 'requests', points: sourcePoints },
        stats: { count: sourcePoints.length, start: sourcePoints[0].timestamp, end: sourcePoints[sourcePoints.length - 1].timestamp, min: 2, max: 40, mean: 16, detected_bucket: 900_000_000_000, gaps: 0 },
      }],
    })
    const mapping: TrafficProfile['mappings'][number] = {
      series_key: 'requests', series_name: 'requests', group_name: 'Checkout', source_start: '2026-08-26T00:15:00Z', source_end: '2026-08-26T00:45:00Z', source_duration: '30m', peak_target: 40,
      points: [{ offset_ms: 0, target: 20 }, { offset_ms: 300_000, target: 40 }],
    }
    apiMock.deriveTrafficProfile.mockResolvedValue({
      traffic_profile: {
        version: 1, source: defaultAnalysis.source, value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30m', target_duration: '10m', gap_policy: 'hold', window: { mode: 'peak', duration: '30m' }, mappings: [mapping], refreshed_at: '2026-08-27T00:00:00Z',
      },
      preview: [mapping],
      scenario_draft: { name: 'Replay', duration: '10m', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [] },
    } satisfies TrafficProfileDeriveResponse)
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const windowSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.querySelector('span')?.textContent === 'Window') as HTMLSelectElement
    await act(async () => {
      windowSelect.value = 'peak'
      windowSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const peakDurationInput = Array.from(container.querySelectorAll('input')).find(input => input.parentElement?.querySelector('span')?.textContent === 'Peak window duration') as HTMLInputElement
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(peakDurationInput, '30m')
      peakDurationInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(container.textContent).toContain('complete analyzed source range')
    const selection = container.querySelector('[data-testid="profile-preview-selection"]')
    expect(selection?.getAttribute('data-x1')).toBe('900000')
    expect(selection?.getAttribute('data-x2')).toBe('2700000')
    expect(selection?.getAttribute('data-name')).toBe('Checkout selected window')
    expect(container.querySelector('[data-testid="profile-preview-x-axis"]')?.getAttribute('data-domain')).toBe('[0,3600000]')
    const chartRows = JSON.parse(container.querySelector('[data-testid="profile-preview-chart"]')?.getAttribute('data-chart') || '[]') as Array<Record<string, number>>
    expect(chartRows.find(row => row.elapsed === 0)?.source_0).toBe(2)
    expect(chartRows.find(row => row.elapsed === 3_600_000)?.source_0).toBe(3)
    expect(chartRows.find(row => row.elapsed === 900_000)?.candidate_0).toBe(20)
    expect(chartRows.find(row => row.elapsed === 1_800_000)?.candidate_0).toBe(40)
    expect(chartRows.find(row => row.elapsed === 2_700_000)?.candidate_0).toBe(0)

    await act(async () => root.unmount())
    container.remove()
  })

  it('omits unmapped analyzed series from the profile graph', async () => {
    apiMock.analyzeTrafficProfile.mockResolvedValue({
      ...defaultAnalysis,
      series: [
        defaultAnalysis.series[0],
        {
          series: { key: 'ignored', name: 'ignored', points: analyzedSeries.points },
          stats: { ...defaultAnalysis.series[0].stats },
        },
      ],
    })
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(Array.from(container.querySelectorAll('[data-testid="profile-preview-line"]')).map(line => line.getAttribute('data-name'))).toEqual([
      'Checkout production source',
    ])

    await act(async () => root.unmount())
    container.remove()
  })

  it('preserves an isolated production peak while downsampling the graph', async () => {
    const points = Array.from({ length: 1001 }, (_, index) => ({
      timestamp: new Date(Date.parse('2026-08-26T00:00:00Z') + index * 15_000).toISOString(),
      value: index === 501 ? 999 : 1,
    }))
    apiMock.analyzeTrafficProfile.mockResolvedValue({
      ...defaultAnalysis,
      series: [{
        series: { key: 'requests', name: 'requests', points },
        stats: { count: points.length, start: points[0].timestamp, end: points[points.length - 1].timestamp, min: 1, max: 999, mean: 2, detected_bucket: 15_000_000_000, gaps: 0 },
      }],
    })
    const draft = { name: 'Replay', duration: '1h', scenario_mode: 'vuser_driven', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const prometheus = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Prometheus')
    await act(async () => { prometheus?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const chartRows = JSON.parse(container.querySelector('[data-testid="profile-preview-chart"]')?.getAttribute('data-chart') || '[]') as Array<Record<string, number>>
    expect(chartRows.some(row => row.source_0 === 999)).toBe(true)
    expect(chartRows.length).toBeLessThanOrEqual(1000)

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

  it('supports relative elapsed timestamps and restores the saved timezone', async () => {
    apiMock.analyzeTrafficProfile.mockResolvedValue({
      source: { kind: 'file', file: { filename: 'relative.csv', sheet: 'CSV', timestamp_column: '', timestamp_mode: 'relative', relative_start_time: '12:00', value_columns: [], timezone: 'America/Chicago' } },
      sheets: [{ name: 'CSV', columns: ['elapsed', 'transactions'], samples: [['0:00', '10']] }],
      series: [],
    })
    const existingProfile: TrafficProfile = {
      version: 1,
      source: { kind: 'file', file: { filename: 'relative.csv', sheet: 'CSV', timestamp_column: 'elapsed', timestamp_mode: 'relative', relative_start_time: '12:00', value_columns: ['transactions'], timezone: 'America/Chicago' } },
      value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '15m', target_duration: '15m', gap_policy: 'hold',
      mappings: [{ series_key: 'requests', series_name: 'requests', group_name: 'Checkout', peak_target: 30, points: [{ offset_ms: 0, target: 15 }, { offset_ms: 15_000, target: 30 }] }],
      refreshed_at: '2026-08-27T00:00:00Z',
    }
    const draft = { name: 'Replay', duration: '15m', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} existingProfile={existingProfile} onApply={vi.fn()} />) })

    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { configurable: true, value: [new File(['elapsed,transactions\n0:00,10\n'], 'relative.csv')] })
    await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect((container.querySelector('[aria-label="Timestamp values"]') as HTMLSelectElement).value).toBe('relative')
    expect((container.querySelector('[aria-label="Relative 0:00 starts at"]') as HTMLInputElement).value).toBe('12:00')
    expect((container.querySelector('[aria-label="Timezone"]') as HTMLSelectElement).value).toBe('America/Chicago')
    const uploadCalls = apiMock.analyzeTrafficProfile.mock.calls
    const upload = uploadCalls[uploadCalls.length - 1]?.[0] as FormData
    expect(upload.get('timestamp_mode')).toBe('relative')
    expect(upload.get('relative_start_time')).toBe('12:00')
    expect(upload.get('timezone')).toBe('America/Chicago')

    await act(async () => root.unmount())
    container.remove()
  })

  it('requires file re-analysis after timestamp settings change', async () => {
    const fileAnalysis = {
      source: { kind: 'file', file: { filename: 'traffic.csv', sheet: 'CSV', timestamp_column: 'timestamp', timestamp_mode: 'absolute', value_columns: ['transactions'], timezone: 'UTC' } },
      sheets: [{ name: 'CSV', columns: ['timestamp', 'transactions'], samples: [['2026-08-26T00:00:00Z', '15']] }],
      series: defaultAnalysis.series,
    } as const
    apiMock.analyzeTrafficProfile.mockResolvedValue(fileAnalysis)
    const draft = { name: 'Replay', duration: '15m', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10 }] } as ScenarioUpsertPayload
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<TrafficProfileSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const input = container.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { configurable: true, value: [new File(['timestamp,transactions\n'], 'traffic.csv')] })
    await act(async () => { input.dispatchEvent(new Event('change', { bubbles: true })) })
    const analyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze source')
    await act(async () => { analyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Build profile preview')).toBe(true)

    let resolveReanalysis: ((value: typeof fileAnalysis) => void) | undefined
    apiMock.analyzeTrafficProfile.mockReturnValueOnce(new Promise(resolve => { resolveReanalysis = resolve }))
    const reanalyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Re-analyze')
    await act(async () => { reanalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const timestampMode = container.querySelector('[aria-label="Timestamp values"]') as HTMLSelectElement
    await act(async () => {
      timestampMode.value = 'relative'
      timestampMode.dispatchEvent(new Event('change', { bubbles: true }))
      resolveReanalysis?.(fileAnalysis)
    })
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Build profile preview')).toBe(false)
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Re-analyze')).toBe(true)

    const latestReanalyze = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Re-analyze')
    await act(async () => { latestReanalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const windowMode = Array.from(container.querySelectorAll('label')).find(label => label.querySelector('span')?.textContent === 'Window')?.querySelector('select') as HTMLSelectElement
    await act(async () => {
      windowMode.value = 'explicit'
      windowMode.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const startOffset = container.querySelector('[aria-label="Window start offset"]') as HTMLInputElement
    const endOffset = container.querySelector('[aria-label="Window end offset"]') as HTMLInputElement
    expect(startOffset).not.toBeNull()
    expect(endOffset).not.toBeNull()
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(startOffset, '15m')
      startOffset.dispatchEvent(new Event('input', { bubbles: true }))
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(endOffset, '45m')
      endOffset.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build profile preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.deriveTrafficProfile).toHaveBeenCalledWith(expect.objectContaining({ window: { mode: 'explicit', start_offset: '15m', end_offset: '45m' } }))

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
        sheets: [{ name: 'CSV', columns: ['timestamp', 'transactions'], samples: [[point.timestamp, '15']] }],
        series: [{ series: { ...analyzedSeries, key: `${group}-requests`, name: `${group} requests` }, stats: { count: 2, start: point.timestamp, end: '2026-08-26T00:00:15Z', min: 15, max: 30, mean: 22.5, detected_bucket: 15_000_000_000, gaps: 0 } }],
      }
    })
    const compositeMappings: TrafficProfile['mappings'] = [
      { series_key: 'group-1:checkout-requests', series_name: 'checkout requests', group_name: 'Checkout', source_start: '2026-08-26T00:00:00Z', source_end: '2026-08-26T00:00:30Z', source_duration: '30s', peak_target: 30, points: [{ offset_ms: 0, target: 10 }, { offset_ms: 15_000, target: 30 }] },
      { series_key: 'group-2:search-requests', series_name: 'search requests', group_name: 'Search', source_start: '2026-08-26T00:00:00Z', source_end: '2026-08-26T00:00:30Z', source_duration: '30s', peak_target: 20, points: [{ offset_ms: 0, target: 5 }, { offset_ms: 15_000, target: 20 }] },
    ]
    apiMock.deriveTrafficProfile.mockResolvedValue({
      traffic_profile: { version: 1, source: { kind: 'multiple', sources: [] }, value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '30s', target_duration: '30s', mappings: compositeMappings, refreshed_at: '2026-08-27T00:00:00Z' },
      preview: compositeMappings, scenario_draft: { name: 'Replay', duration: '30s', scenario_mode: 'goal_oriented', script_shakeout: false, resource_baseline: false, groups: [] },
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
    apiMock.analyzeTrafficProfile.mockRejectedValueOnce(new Error('temporary analysis failure'))
    await act(async () => { checkoutAnalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.querySelectorAll('.status-badge.success')).toHaveLength(2)
    expect(container.textContent).toContain('temporary analysis failure')

    apiMock.analyzeTrafficProfile.mockResolvedValueOnce({
      source: { kind: 'file' as const, file: { filename: 'checkout.csv', sheet: 'CSV', timestamp_column: 'timestamp', value_columns: ['transactions'], timezone: 'UTC' } },
      sheets: [{ name: 'CSV', columns: ['timestamp', 'transactions'], samples: [] }],
      series: [],
    })
    await act(async () => { checkoutAnalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.querySelectorAll('.status-badge.success')).toHaveLength(1)
    expect(container.querySelector('[aria-label="Workload series for Checkout"]')).toBeNull()

    const checkoutTimestampMode = container.querySelector('[aria-label="Checkout timestamp values"]') as HTMLSelectElement
    await act(async () => {
      checkoutTimestampMode.value = 'relative'
      checkoutTimestampMode.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.querySelectorAll('.status-badge.success')).toHaveLength(1)
    expect(container.querySelector('[aria-label="Workload series for Checkout"]')).toBeNull()
    await act(async () => { checkoutAnalyze?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(container.querySelectorAll('.status-badge.success')).toHaveLength(2)

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
    expect(Array.from(container.querySelectorAll('[data-testid="profile-preview-line"]')).map(line => line.getAttribute('data-name'))).toEqual([
      'Checkout production source',
      'Search production source',
      'Checkout candidate',
      'Search candidate',
      'Total candidate',
    ])
    const chartRows = JSON.parse(container.querySelector('[data-testid="profile-preview-chart"]')?.getAttribute('data-chart') || '[]') as Array<Record<string, number>>
    expect(chartRows.find(row => row.elapsed === 0)).toMatchObject({ candidate_0: 10, candidate_1: 5, candidate_total: 15 })
    expect(chartRows.find(row => row.elapsed === 15_000)).toMatchObject({ candidate_0: 30, candidate_1: 20, candidate_total: 50 })

    await act(async () => root.unmount())
    container.remove()
  })
})
