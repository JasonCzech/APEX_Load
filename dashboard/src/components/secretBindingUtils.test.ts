import { describe, expect, it } from 'vitest'

import { type SecretBindingRequirement } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  refreshedSecretBindingRequirements,
  replaceSecretBindingRequirementsForScript,
  secretBindingRequirementsForScript,
} from './secretBindingUtils'

const first = { name: 'First' } as ScriptDefinition
const second = { name: 'Second' } as ScriptDefinition

function requirement(id: string, scriptIndex?: number): SecretBindingRequirement {
  return {
    id,
    path: '/actions/0/url',
    label: 'Request URL',
    context: 'First action',
    suggested_name: 'API_TOKEN',
    ...(scriptIndex === undefined ? {} : { script_index: scriptIndex }),
  }
}

describe('secret binding requirement scoping', () => {
  it('selects requirements for the active generated script', () => {
    const requirements = [requirement('first', 0), requirement('second', 1)]

    expect(secretBindingRequirementsForScript(requirements, 1, second).map(item => item.id)).toEqual(['second'])
  })

  it('replaces only the active script requirements', () => {
    const requirements = [requirement('first', 0), requirement('second', 1)]
    const remaining = replaceSecretBindingRequirementsForScript(requirements, 0, first, [])

    expect(remaining.map(item => item.id)).toEqual(['second'])
  })

  it('treats an unscoped requirement as belonging to a single primary script', () => {
    const unscoped = requirement('single')

    expect(secretBindingRequirementsForScript([unscoped], 0, first)).toEqual([unscoped])
    expect(secretBindingRequirementsForScript([unscoped], 1, second)).toEqual([])
  })

  it('extracts refreshed requirements from a stale-binding API error', () => {
    const refreshed = requirement('refreshed', 1)
    const error = {
      code: 'secret_binding_requirements_changed',
      payload: { secret_binding_requirements: [refreshed] },
    }

    expect(refreshedSecretBindingRequirements(error)).toEqual([refreshed])
    expect(refreshedSecretBindingRequirements(new Error('other'))).toBeNull()
  })
})
