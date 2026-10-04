import { expect, it } from 'vitest'
import { monitorMetricCatalog, monitorSourceSelectionComplete, nativeMonitorProvider } from './monitorProviderModel'
import { monitorJobState } from './appDynamicsConnectionModel'
import { emptyDynatraceSelection } from '../types/dynatrace'
import type { MonitorSource } from '../types/appdynamics'

const source: MonitorSource = { id: 'dt', name: 'SaaS', pack: 'dynatrace', revision: 1, enabled: true, collector_id: '', executor_mode: 'direct', connection_id: 'connection', interval_seconds: 60, dynatrace: emptyDynatraceSelection() }
it('offers Dynatrace and AppDynamics canonical sources without changing legacy packs', () => {
  expect(nativeMonitorProvider('dynatrace')).toBe(true); expect(nativeMonitorProvider('appdynamics')).toBe(true); expect(nativeMonitorProvider('cloudwatch')).toBe(false)
  expect(monitorSourceSelectionComplete(source)).toBe(true)
  expect(monitorSourceSelectionComplete({ ...source, connection_id: undefined })).toBe(false)
  expect(monitorSourceSelectionComplete({ ...source, executor_mode: 'collector' })).toBe(false)
  expect(monitorSourceSelectionComplete({ ...source, executor_mode: 'collector', collector_id: 'collector' })).toBe(true)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: { ...emptyDynatraceSelection(), problems: false } })).toBe(false)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: { ...emptyDynatraceSelection(), metrics: [{ name: '', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg' }] } })).toBe(false)
  const grail = { name: 'cpu', kind: 'metric' as const, query: 'timeseries value=avg(dt.host.cpu.usage)', value_field: 'value', statistic: 'time_average', unit: '%', bucket_seconds: 60 }
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: { ...emptyDynatraceSelection(), grail: [{ ...grail, value_field: '' }] } })).toBe(false)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: { ...emptyDynatraceSelection(), grail: [{ ...grail, bucket_seconds: 0 }] } })).toBe(false)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: { ...emptyDynatraceSelection(), grail: [grail] } })).toBe(true)
  expect(monitorJobState({ id: 'job', source, state: 'reconciling', next_sequence: 1 })).toContain('collecting delayed Dynatrace data')
})
it('offers numeric series only and preserves exact source statistics and units', () => {
  const selected = { ...source, dynatrace: { ...emptyDynatraceSelection(), metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', unit: '%', aggregation: 'avg' as const }], grail: [{ name: 'errors', query: 'fetch logs', kind: 'metric' as const, statistic: 'sum', unit: 'count' }, { name: 'logs', query: 'fetch logs', kind: 'log' as const }] } }
  expect(monitorMetricCatalog(selected)).toEqual([{ name: 'cpu', unit: '%', statistic: 'average', path: 'builtin:host.cpu.usage' }, { name: 'errors', unit: 'count', statistic: 'sum' }])
  expect(monitorMetricCatalog()).toEqual([])
})
it('reads persisted selections with omitted empty arrays and false capabilities', () => {
  const persisted = JSON.parse('{"metrics":[{"name":"cpu","metric_selector":"builtin:host.cpu.usage","aggregation":"avg"}]}')
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: persisted })).toBe(true)
  expect(monitorMetricCatalog({ ...source, dynatrace: persisted })).toHaveLength(1)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: JSON.parse('{"events":true}') })).toBe(true)
  expect(monitorSourceSelectionComplete({ ...source, dynatrace: JSON.parse('{}') })).toBe(false)
})
