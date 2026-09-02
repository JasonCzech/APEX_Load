// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { TrafficProfileAnalyzeResponse, TrafficProfileDeriveResponse } from '../types'
import CSVDrivenSourcePanel from './CSVDrivenSourcePanel'
import type { ScenarioUpsertPayload } from './scenarioPayloadUtils'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const inspection = {
  source: { kind: 'file' as const, file: { filename: 'schedule.csv', timestamp_column: '', value_columns: [] } },
  sheets: [{ name: 'CSV', columns: ['elapsed', 'checkout', 'art_ms'] }],
  series: [],
} satisfies TrafficProfileAnalyzeResponse

const analysis = {
  ...inspection,
  source: {
    kind: 'file' as const,
    file: {
      filename: 'schedule.csv',
      timestamp_column: 'elapsed',
      timestamp_mode: 'relative' as const,
      relative_start_time: '00:00',
      value_columns: ['checkout'],
      art_columns: ['art_ms'],
    },
  },
  series: [{
    series: {
      key: 'checkout',
      name: 'checkout',
      points: [
        { timestamp: '1970-01-01T00:00:00Z', value: 10 },
        { timestamp: '1970-01-01T00:00:05Z', value: 20 },
      ],
    },
    stats: { count: 2, start: '1970-01-01T00:00:00Z', end: '1970-01-01T00:00:05Z', min: 10, max: 20, mean: 15, detected_bucket: 5_000_000_000, gaps: 0 },
  }],
} satisfies TrafficProfileAnalyzeResponse

const derived = {
  traffic_profile: {
    version: 1,
    source: analysis.source,
    value_semantics: 'vusers' as const,
    runtime_target: 'vusers' as const,
    interpolation: 'step' as const,
    source_duration: '10s',
    target_duration: '10s',
    gap_policy: 'drop' as const,
    mappings: [{ series_key: 'checkout', series_name: 'checkout', group_name: 'Checkout', peak_target: 20, points: [{ offset_ms: 0, target: 10 }, { offset_ms: 5_000, target: 20 }] }],
    refreshed_at: '2026-09-02T00:00:00Z',
  },
  preview: [{ series_key: 'checkout', series_name: 'checkout', group_name: 'Checkout', peak_target: 20, points: [{ offset_ms: 0, target: 10 }, { offset_ms: 5_000, target: 20 }] }],
  scenario_draft: { name: 'CSV Replay', duration: '10s', scenario_mode: 'vuser_driven' as const, script_shakeout: false, resource_baseline: false, groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 20, initialization: 'at_runtime' as const }] },
} satisfies TrafficProfileDeriveResponse

const apiMock = {
  analyzeCSVDriven: vi.fn(),
  deriveCSVDriven: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))
vi.mock('recharts', () => {
  const Wrapper = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  const Chart = ({ children, data }: { children?: ReactNode; data?: unknown[] }) => <div data-testid="csv-preview-chart" data-chart={JSON.stringify(data || [])}>{children}</div>
  const MockLine = ({ type, name }: { type?: string; name?: string }) => <span data-testid="csv-preview-line" data-type={type} data-name={name} />
  return { CartesianGrid: Wrapper, Legend: Wrapper, Line: MockLine, LineChart: Chart, ResponsiveContainer: Wrapper, Tooltip: Wrapper, XAxis: Wrapper, YAxis: Wrapper }
})

const draft = {
  name: 'CSV Replay',
  duration: '1m',
  scenario_mode: 'vuser_driven' as const,
  script_shakeout: false,
  resource_baseline: false,
  groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' as const }],
} satisfies ScenarioUpsertPayload

function setSelect(select: HTMLSelectElement, value: string) {
  select.value = value
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('CSVDrivenSourcePanel', () => {
  beforeEach(() => {
    apiMock.analyzeCSVDriven.mockReset().mockResolvedValueOnce(inspection).mockResolvedValueOnce(analysis)
    apiMock.deriveCSVDriven.mockReset().mockResolvedValue(derived)
  })

  it('keeps VUser schedules free of TPS, ART, and capacity controls', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<CSVDrivenSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    expect(container.querySelector('[role="radio"][aria-checked="true"]')?.textContent).toContain('VUser Driven')
    expect(container.textContent).not.toContain('Optional ART columns')
    expect(container.textContent).not.toContain('Minimum VUsers')
    expect(container.textContent).not.toContain('Maximum VUsers')

    await act(async () => root.unmount())
    container.remove()
  })

  it('inspects, maps, previews, and applies an exact relative VUser schedule', async () => {
    const onApply = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<CSVDrivenSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={onApply} />) })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const file = new File(['elapsed,checkout\n0,10\n5,20'], 'schedule.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
    await act(async () => { fileInput.dispatchEvent(new Event('change', { bubbles: true })) })

    const inspect = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Inspect columns')
    await act(async () => { inspect?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.analyzeCSVDriven).toHaveBeenCalledTimes(1)

    const selects = Array.from(container.querySelectorAll('select'))
    const timeColumn = selects.find(select => select.parentElement?.querySelector('span')?.textContent === 'Time column')
    const timeValues = selects.find(select => select.parentElement?.querySelector('span')?.textContent === 'Time values')
    const targetColumns = selects.find(select => select.parentElement?.querySelector('span')?.textContent === 'Target columns')
    expect(timeColumn && timeValues && targetColumns).toBeTruthy()
    await act(async () => {
      setSelect(timeColumn as HTMLSelectElement, 'elapsed')
      setSelect(timeValues as HTMLSelectElement, 'relative')
      Array.from((targetColumns as HTMLSelectElement).options).forEach(option => { option.selected = option.value === 'checkout' })
      targetColumns?.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const analyzeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze schedule')
    await act(async () => { analyzeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(apiMock.analyzeCSVDriven).toHaveBeenCalledTimes(2)

    const map = container.querySelector('[aria-label="Map checkout"]') as HTMLSelectElement
    await act(async () => setSelect(map, 'Checkout'))
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build exact schedule preview')
    await act(async () => { preview?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(apiMock.deriveCSVDriven).toHaveBeenCalledWith(expect.objectContaining({
      value_semantics: 'vusers',
      gap_policy: 'drop',
      window: { mode: 'full' },
      mappings: [{ series_key: 'checkout', group_name: 'Checkout' }],
      scenario_draft: expect.objectContaining({ scenario_mode: 'vuser_driven' }),
      source: expect.objectContaining({ file: expect.not.objectContaining({ art_columns: expect.anything() }) }),
    }))
    const request = apiMock.deriveCSVDriven.mock.calls[0][0]
    expect(request).not.toHaveProperty('amplitude')
    expect(request).not.toHaveProperty('target_duration')
    expect(container.textContent).toContain('VUser schedule · 10s')
    expect(container.querySelector('[data-testid="csv-preview-line"]')?.getAttribute('data-type')).toBe('stepAfter')
    expect(JSON.parse(container.querySelector('[data-testid="csv-preview-chart"]')?.getAttribute('data-chart') || '[]')).toEqual([
      { offset: 0, group_0: 10 },
      { offset: 5000, group_0: 20 },
      { offset: 10000, group_0: 20 },
    ])

    const apply = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply CSV schedule')
    await act(async () => { apply?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(onApply).toHaveBeenCalledWith(derived)

    await act(async () => setSelect(timeColumn as HTMLSelectElement, 'checkout'))
    expect(container.textContent).not.toContain('VUser schedule · 10s')
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply CSV schedule')).toBeUndefined()
    expect(container.textContent).toContain('Analyze schedule')

    await act(async () => root.unmount())
    container.remove()
  })

  it('reveals TPS ART and guardrail controls only in Goal Driven mode', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => { root.render(<CSVDrivenSourcePanel groupNames={['Checkout']} scenarioDraft={draft} onApply={vi.fn()} />) })

    const goal = Array.from(container.querySelectorAll('[role="radio"]')).find(element => element.textContent?.includes('Goal Driven'))
    await act(async () => { goal?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['elapsed,checkout'], 'schedule.csv', { type: 'text/csv' })] })
    await act(async () => { fileInput.dispatchEvent(new Event('change', { bubbles: true })) })
    const inspect = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Inspect columns')
    await act(async () => { inspect?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(container.textContent).toContain('Optional ART columns')
    expect(container.textContent).not.toContain('Minimum VUsers')

    await act(async () => root.unmount())
    container.remove()
  })

  it('requires scripts before deriving a mapped schedule', async () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    const draftWithoutScript = { ...draft, groups: [{ ...draft.groups[0], script_id: '' }] }
    await act(async () => { root.render(<CSVDrivenSourcePanel groupNames={['Checkout']} scenarioDraft={draftWithoutScript} onApply={vi.fn()} />) })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['elapsed,checkout\n0,10\n5,20'], 'schedule.csv', { type: 'text/csv' })] })
    await act(async () => { fileInput.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Inspect columns')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    const selects = Array.from(container.querySelectorAll('select'))
    const timeColumn = selects.find(select => select.parentElement?.querySelector('span')?.textContent === 'Time column') as HTMLSelectElement
    const targetColumns = selects.find(select => select.parentElement?.querySelector('span')?.textContent === 'Target columns') as HTMLSelectElement
    await act(async () => {
      setSelect(timeColumn, 'elapsed')
      Array.from(targetColumns.options).forEach(option => { option.selected = option.value === 'checkout' })
      targetColumns.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Analyze schedule')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => setSelect(container.querySelector('[aria-label="Map checkout"]') as HTMLSelectElement, 'Checkout'))

    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Build exact schedule preview') as HTMLButtonElement
    expect(preview.disabled).toBe(true)
    expect(container.textContent).toContain('Assign a script to every scenario group')
    expect(apiMock.deriveCSVDriven).not.toHaveBeenCalled()

    await act(async () => root.unmount())
    container.remove()
  })
})
