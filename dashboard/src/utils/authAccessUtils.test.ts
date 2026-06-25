import { describe, expect, it } from 'vitest'

import { type DashboardAuthEffectiveAccessResource } from '../types'
import { isEffectiveOperationAllowed } from './authAccessUtils'

const resources: DashboardAuthEffectiveAccessResource[] = [
  {
    id: 'tests',
    name: 'Tests',
    description: 'Test access',
    operations: [
      { id: 'view_tests', label: 'View tests', allowed: true, grant_source: 'project' },
      { id: 'manage_tests', label: 'Manage tests', allowed: false, grant_source: 'none' },
    ],
  },
]

describe('isEffectiveOperationAllowed', () => {
  it('returns explicit operation permissions when present', () => {
    expect(isEffectiveOperationAllowed(resources, 'view_tests')).toBe(true)
    expect(isEffectiveOperationAllowed(resources, 'manage_tests')).toBe(false)
  })

  it('defaults to deny when an operation is missing, honoring an explicit fallback', () => {
    expect(isEffectiveOperationAllowed(resources, 'missing_operation')).toBe(false)
    expect(isEffectiveOperationAllowed(resources, 'missing_operation', false)).toBe(false)
    expect(isEffectiveOperationAllowed(resources, 'missing_operation', true)).toBe(true)
  })
})
