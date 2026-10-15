import { describe, expect, it } from 'vitest'

import { type SecretBindingRequirement } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  refreshedSecretBindingRequirements,
  replaceSecretBindingRequirementsForScript,
  requirementsPreserveDestinationValues,
  secretBindingDisplayNames,
  secretBindingRequirementsForScript,
  secretDisplayName,
  updateSecretBindingRequirementNames,
} from './secretBindingUtils'

const first = { name: 'First' } as ScriptDefinition
const second = { name: 'Second' } as ScriptDefinition

describe('secretBindingDisplayNames', () => {
  const nativeBrunoScript = {
    ...first,
    name: 'Imported',
    metadata: { import: { source: 'bruno', preserved: { native_importer: 'apexload-bruno-native-importer' } } },
  }

  it('simplifies every provenance-marked native Bruno identity length', () => {
    const names = [
      'PASSWORD_13860464B9306046D342E1F5',
      'USERNAME_0B2497BE76736358F5C883E7',
      'AUTH_TOKEN_84921F7A1055',
    ]

    expect(Object.fromEntries(secretBindingDisplayNames(names, nativeBrunoScript))).toEqual({
      PASSWORD_13860464B9306046D342E1F5: 'PASSWORD',
      USERNAME_0B2497BE76736358F5C883E7: 'USERNAME',
      AUTH_TOKEN_84921F7A1055: 'AUTH_TOKEN',
    })
  })

  it('does not alter manually authored or unsupported suffix shapes', () => {
    const names = ['TOKEN_1234567890ABCDEF12345678', 'CLIENT_ID_12ABCDEF3456', 'LOGIN_PASSWORD']

    expect(Object.fromEntries(secretBindingDisplayNames(names, first))).toEqual({
      TOKEN_1234567890ABCDEF12345678: 'TOKEN_1234567890ABCDEF12345678',
      CLIENT_ID_12ABCDEF3456: 'CLIENT_ID_12ABCDEF3456',
      LOGIN_PASSWORD: 'LOGIN_PASSWORD',
    })
    expect(secretBindingDisplayNames(['CLIENT_ID_12ABCDEF345'], nativeBrunoScript).get('CLIENT_ID_12ABCDEF345')).toBe('CLIENT_ID_12ABCDEF345')
  })

  it('labels an older path-prefixed native Bruno field secret by the field alone', () => {
    const names = [
      'API_ORDERS_GET_ORDER_HEADER_X_API_KEY_84921F7A1055',
      // A header named with AUTH keeps its whole name.
      'API_ACCOUNT_PROFILE_HEADER_X_CDF_AUTH_TOKEN_53E6F3601255',
      'API_ORDERS_GET_ORDER_QUERY_ACCESS_TOKEN_0B2497BE7673',
      'API_ORDERS_CREATE_BODY_JSON_CLIENT_SECRET_161E2747AD92',
      'API_ORDERS_CREATE_BODY_XML_USER_PASSWORD_53E6F3601255',
      'API_LOGIN_BODY_FORM_PASSWORD_9C0FFEE12345',
      'API_UPLOAD_BODY_MULTIPART_API_KEY_0DDBA11C0FFE',
      'API_ORDERS_LOGIN_AUTH_PASSWORD_A3CA62479DB7',
      // A file named after headers: the last marker is the field's.
      'API_SET_HEADER_TEST_HEADER_AUTHORIZATION_1EE1A2332399',
    ]

    expect(Object.fromEntries(secretBindingDisplayNames(names, nativeBrunoScript))).toEqual({
      API_ORDERS_GET_ORDER_HEADER_X_API_KEY_84921F7A1055: 'X_API_KEY',
      API_ACCOUNT_PROFILE_HEADER_X_CDF_AUTH_TOKEN_53E6F3601255: 'X_CDF_AUTH_TOKEN',
      API_ORDERS_GET_ORDER_QUERY_ACCESS_TOKEN_0B2497BE7673: 'ACCESS_TOKEN',
      API_ORDERS_CREATE_BODY_JSON_CLIENT_SECRET_161E2747AD92: 'CLIENT_SECRET',
      API_ORDERS_CREATE_BODY_XML_USER_PASSWORD_53E6F3601255: 'USER_PASSWORD',
      API_LOGIN_BODY_FORM_PASSWORD_9C0FFEE12345: 'PASSWORD',
      API_UPLOAD_BODY_MULTIPART_API_KEY_0DDBA11C0FFE: 'API_KEY',
      API_ORDERS_LOGIN_AUTH_PASSWORD_A3CA62479DB7: 'AUTH_PASSWORD',
      API_SET_HEADER_TEST_HEADER_AUTHORIZATION_1EE1A2332399: 'AUTHORIZATION',
    })
    // Only a native Bruno import named secrets this way.
    expect(secretBindingDisplayNames(['API_ORDERS_HEADER_X_API_KEY_84921F7A1055'], { ...first, metadata: { import: { source: 'har' } } })
      .get('API_ORDERS_HEADER_X_API_KEY_84921F7A1055')).toBe('API_ORDERS_HEADER_X_API_KEY')
  })

  it('simplifies the collision identity any older import gave a second credential', () => {
    const harScript = { ...first, name: 'Recorded', metadata: { import: { source: 'har' } } }
    const labels = secretBindingDisplayNames(['API_TOKEN', 'API_TOKEN_84921F7A1055', 'SESSION_DEADBEEFCAFE', 'TOKEN_2'], harScript)

    expect(Object.fromEntries(labels)).toEqual({
      API_TOKEN: 'API_TOKEN · source 1',
      API_TOKEN_84921F7A1055: 'API_TOKEN · source 2',
      // Twelve letters with no digit read as a word, not an identity.
      SESSION_DEADBEEFCAFE: 'SESSION_DEADBEEFCAFE',
      TOKEN_2: 'TOKEN_2',
    })
    // The 24-digit identity only ever came from a native Bruno import.
    expect(secretBindingDisplayNames(['TOKEN_1234567890ABCDEF12345678'], harScript).get('TOKEN_1234567890ABCDEF12345678'))
      .toBe('TOKEN_1234567890ABCDEF12345678')
    expect(secretDisplayName(labels, ' API_TOKEN_84921F7A1055 ')).toBe('API_TOKEN · source 2')
    expect(secretDisplayName(labels, 'UNLISTED')).toBe('UNLISTED')
  })

  it('keeps distinct case-sensitive Bruno variables distinguishable', () => {
    const firstName = 'CLIENT_ID_111111111111'
    const secondName = 'CLIENT_ID_222222222222222222222222'

    expect(Object.fromEntries(secretBindingDisplayNames([secondName, firstName], nativeBrunoScript))).toEqual({
      CLIENT_ID_111111111111: 'CLIENT_ID · source 1',
      CLIENT_ID_222222222222222222222222: 'CLIENT_ID · source 2',
    })
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

  it('refreshes a review from a rebind refusal so it stops offering the refused choice', () => {
    const refreshed = { ...requirement('kept', 0), rebind_unavailable_reason: 'destination_bytes_unaligned' as const }
    const error = {
      code: 'secret_binding_rebind_unavailable',
      payload: { secret_binding_requirements: [refreshed] },
    }

    expect(refreshedSecretBindingRequirements(error)).toEqual([refreshed])
    expect(refreshedSecretBindingRequirements({ code: 'secret_bindings_required', payload: { secret_binding_requirements: [refreshed] } })).toBeNull()
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
