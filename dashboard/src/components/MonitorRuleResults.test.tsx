// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import MonitorRuleResults from './MonitorRuleResults'
import { validateGoalConfig } from './goalConfigValidation'
import { buildGoalConfigPayload } from './scenarioPayloadUtils'
import type { MonitorRule } from '../types/appdynamics'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const rule: MonitorRule = { source_id: 'source', metric: 'response_time', unit: 'ms', statistic: 'avg', comparator: 'lte', threshold: 100, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }
it('retains terminal monitor rules in both scenario modes without changing runtime stop policy', () => {
  for (const mode of ['vuser_driven', 'goal_oriented'] as const) {
    expect(buildGoalConfigPayload(mode, { monitor_rules: [rule] })).toEqual({ monitor_rules: [rule] })
    expect(buildGoalConfigPayload(mode, { monitor_rules: [] })).toEqual({ monitor_rules: [] })
  }
  expect(validateGoalConfig({ monitor_rules: [rule] })).toBeNull()
  expect(validateGoalConfig({ monitor_rules: [{ ...rule, metric: '' }] })).toContain('Monitoring criterion 1')
  expect(validateGoalConfig({ monitor_rules: [{ ...rule, threshold: Number.NaN }] })).toContain('Monitoring criterion 1')
  expect(validateGoalConfig({ monitor_rules: [{ ...rule, start_offset_seconds: 100, end_offset_seconds: 90 }] })).toContain('Monitoring criterion 1')
  expect(validateGoalConfig({ monitor_rules: [{ ...rule, dimensions: { node: 123 } as never }] })).toContain('string entity dimensions')
  expect(validateGoalConfig({ monitor_rules: [{ ...rule, dimensions: Object.fromEntries(Array.from({ length: 17 }, (_, i) => [`node_${i}`, '123'])) }] })).toContain('string entity dimensions')
})
it('shows measured zero and unevaluable evidence distinctly', () => {
  const container = document.createElement('div'); const root = createRoot(container)
  act(() => root.render(<MonitorRuleResults results={[{ rule, status: 'pass', observed: 0, coverage_pct: 100, eligible_buckets: 3, message: 'met' }, { rule, status: 'not_evaluated', coverage_pct: 40, eligible_buckets: 1, message: 'insufficient coverage' }]} />))
  expect(container.textContent).toContain('0 ms'); expect(container.textContent).toContain('Not evaluated'); expect(container.textContent).toContain('40.0%')
  act(() => root.unmount())
})
