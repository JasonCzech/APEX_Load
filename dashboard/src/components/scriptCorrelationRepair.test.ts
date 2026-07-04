import { describe, expect, it } from 'vitest'

import { applyCorrelationRepairToAction } from './scriptCorrelationRepair'
import type { ScriptActionDefinition } from '../types/scripts'

describe('applyCorrelationRepairToAction', () => {
  it('replaces stale diagnosed values in replayed request fields only', () => {
    const action: ScriptActionDefinition = {
      name: 'Use OLD literally in name',
      type: 'POST',
      method: 'POST',
      target: '/orders?csrf=OLD',
      headers: { Authorization: 'Bearer OLD', 'X-Static': 'ok' },
      body: '{"csrf":"OLD"}',
      value: 'OLD',
      params: { token: 'OLD', nested: ['keep', 'OLD'] },
      metadata: { recorded: 'OLD' },
      extractors: [{ name: 'csrf', source: 'body', type: 'jsonpath', expression: '$.OLD' }],
    }

    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      recordedValue: 'OLD',
      replacement: '{{var.csrf}}',
    })

    expect(repaired.target).toBe('/orders?csrf={{var.csrf}}')
    expect(repaired.headers?.Authorization).toBe('Bearer {{var.csrf}}')
    expect(repaired.body).toBe('{"csrf":"{{var.csrf}}"}')
    expect(repaired.value).toBe('{{var.csrf}}')
    expect(repaired.params).toEqual({ token: '{{var.csrf}}', nested: ['keep', '{{var.csrf}}'] })
    expect(repaired.name).toBe('Use OLD literally in name')
    expect(repaired.metadata).toEqual({ recorded: 'OLD' })
    expect(repaired.extractors?.[0].expression).toBe('$.OLD')
  })

  it('returns the original action when no replay request field contains the stale value', () => {
    const action: ScriptActionDefinition = {
      name: 'Submit',
      type: 'POST',
      method: 'POST',
      target: '/orders',
      headers: { Authorization: 'Bearer live' },
      params: { page: '1' },
    }

    const repaired = applyCorrelationRepairToAction(action, {
      failingActionIndex: 1,
      recordedValue: 'OLD',
      replacement: '{{var.csrf}}',
    })

    expect(repaired).toBe(action)
  })
})
