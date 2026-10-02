import { describe, expect, it } from 'vitest'
import { validateGoalConfig } from './goalConfigValidation'
import type { TransactionSLARule } from '../types/goals'

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
