import { describe, expect, it } from 'vitest'
import { validateGoalConfig } from './goalConfigValidation'
import type { TransactionSLARule } from '../types/goals'
import type { MonitorRule } from '../types/appdynamics'

describe('scoped archive SLA validation', () => {
  const rule: TransactionSLARule = { population: 'action', protocol: 'kafka', group: 'consumer', transaction: 'Receive', metric: 'max_acknowledgement_ms', comparator: 'lte', threshold: 10 }
  it('preserves valid action scope and measured acknowledgement rules', () => {
    expect(validateGoalConfig({ transaction_rules: [rule] })).toBeNull()
    expect(validateGoalConfig({ transaction_rules: [{ ...rule, metric: 'p99_duration_ms' }] })).toBeNull()
  })
  it.each([{ protocol: ' kafka' }, { group: 'consumer\n' }, { group: 'x'.repeat(257) }])('rejects invalid exact scope %j', invalid => {
    expect(validateGoalConfig({ transaction_rules: [{ ...rule, ...invalid }] })).toContain('bounded exact')
  })
})

describe('native monitoring acceptance validation', () => {
  const rule: MonitorRule = { source_id: 'dt', metric: 'cpu', unit: '%', statistic: 'avg', comparator: 'lte', threshold: 80, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0, provider: 'dynatrace', backend: 'classic', metric_id: 'metrics:catalog', source_statistic: 'time_average', resolution_seconds: 60, evaluation_operation: 'time_average', entity_ids: { host: 'HOST-0123456789ABCDEF' } }
  it('accepts exact canonical identities and time-weighted gauge evaluation', () => {
    expect(validateGoalConfig({ monitor_rules: [rule] })).toBeNull()
  })
  it.each([{ resolution_seconds: 0 }, { resolution_seconds: 60.5 }, { metric_id: 'x'.repeat(257) }])('rejects invalid series filters %j', change => {
    expect(validateGoalConfig({ monitor_rules: [{ ...rule, ...change }] })).toContain('bounded exact provider series')
  })
  it.each([{ host: '' }, { host: 'HOST-secret\n' }, { host: 'x'.repeat(257) }])('rejects unbounded entity identities %j', entity_ids => {
    expect(validateGoalConfig({ monitor_rules: [{ ...rule, entity_ids }] })).toContain('bounded exact entity identities')
  })
})
