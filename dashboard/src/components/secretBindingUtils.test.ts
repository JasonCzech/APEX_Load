import { describe, expect, it } from 'vitest'

import { type SecretBindingRequirement } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  friendlySecretBindingName,
  refreshedSecretBindingRequirements,
  replaceSecretBindingRequirementsForScript,
  requirementsPreserveDestinationValues,
  secretBindingRequirementsForScript,
  updateSecretBindingRequirementNames,
} from './secretBindingUtils'

const first = { name: 'First' } as ScriptDefinition
const second = { name: 'Second' } as ScriptDefinition

describe('friendlySecretBindingName', () => {
  it.each([
    ['PASSWORD_13860464B9306046D342E1F5', 'PASSWORD'],
    ['USERNAME_0B2497BE76736358F5C883E7', 'USERNAME'],
    ['CLIENT_ID_12ABCDEF3456', 'CLIENT_ID'],
    ['CLIENT_ID_12ABCDEF3456_2', 'CLIENT_ID'],
    ['LOGIN_PASSWORD', 'LOGIN_PASSWORD'],
    ['TOKEN_1234', 'TOKEN_1234'],
  ])('presents %s as %s without changing ordinary names', (name, expected) => {
    expect(friendlySecretBindingName(name)).toBe(expected)
  })
})

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
  it('preserves only when the server marks every exact requirement occurrence', () => {
    const preserved = {
      ...requirement('preserved'),
      destination_value_preserved: true,
    }

    expect(requirementsPreserveDestinationValues([preserved])).toBe(true)
    expect(requirementsPreserveDestinationValues([
      preserved,
      { ...requirement('second'), destination_value_preserved: true },
    ])).toBe(true)
    expect(requirementsPreserveDestinationValues([
      preserved,
      requirement('unmarked'),
    ])).toBe(false)
    expect(requirementsPreserveDestinationValues([])).toBe(false)
  })

  it('selects requirements for the active generated script', () => {
    const requirements = [requirement('first', 0), requirement('second', 1)]

    expect(secretBindingRequirementsForScript(requirements, 1, second).map(item => item.id)).toEqual(['second'])
  })

  it('replaces only the active script requirements', () => {
    const requirements = [requirement('first', 0), requirement('second', 1)]
    const remaining = replaceSecretBindingRequirementsForScript(requirements, 0, first, [])

    expect(remaining.map(item => item.id)).toEqual(['second'])
  })

  it('retains archive-wide suffixes when a per-script API refresh returns standalone names', () => {
    const firstRequirement = requirement('first', 0)
    const secondRequirement = {
      ...requirement('second', 1),
      suggested_name: 'API_TOKEN_2',
    }
    const refreshedSecond = requirement('second', 1)
    const newlyDetected = {
      ...requirement('new', 1),
      suggested_name: 'SESSION_TOKEN',
    }

    const remaining = replaceSecretBindingRequirementsForScript(
      [firstRequirement, secondRequirement],
      1,
      second,
      [refreshedSecond, newlyDetected],
    )

    expect(remaining.map(item => [item.id, item.suggested_name])).toEqual([
      ['first', 'API_TOKEN'],
      ['second', 'API_TOKEN_2'],
      ['new', 'SESSION_TOKEN'],
    ])
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

  it('persists each archive migration name without changing unrelated requirements', () => {
    const requirements = [requirement('first', 0), requirement('second', 1), requirement('third', 1)]

    const updated = updateSecretBindingRequirementNames(requirements, [
      { requirement_id: 'first', secret_name: 'API_TOKEN' },
      { requirement_id: 'second', secret_name: 'API_TOKEN_2' },
    ])

    expect(updated.map(item => item.suggested_name)).toEqual([
      'API_TOKEN',
      'API_TOKEN_2',
      'API_TOKEN',
    ])
    expect(updated[2]).toBe(requirements[2])
  })
})
