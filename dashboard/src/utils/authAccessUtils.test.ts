import { describe, expect, it } from 'vitest'

import { type DashboardAuthEffectiveAccessResource } from '../types'
import { canCreateScriptFolders, isEffectiveOperationAllowed } from './authAccessUtils'

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

describe('canCreateScriptFolders', () => {
  it('allows the dedicated editor permission without project administration', () => {
    expect(canCreateScriptFolders([{
      id: 'scripts',
      name: 'Scripts',
      description: 'Script access',
      operations: [
        { id: 'create_script_folders', label: 'Create script folders', allowed: true },
        { id: 'manage_projects', label: 'Manage projects', allowed: false },
      ],
    }])).toBe(true)
  })

  it('retains compatibility with administrators on older controllers', () => {
    expect(canCreateScriptFolders([{
      id: 'projects',
      name: 'Projects',
      description: 'Project access',
      operations: [{ id: 'manage_projects', label: 'Manage projects', allowed: true }],
    }])).toBe(true)
    expect(canCreateScriptFolders([])).toBe(false)
  })
})
