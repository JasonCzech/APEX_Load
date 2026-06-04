import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { buildLocalReplayPreflight } from './replayPreflightUtils'

const baseScript: ScriptDefinition = {
  name: 'API Smoke',
  version: '1.0.0',
  protocol: 'http',
  config: { base_url: 'https://api.example.com' },
  actions: [
    {
      name: 'Get profile',
      type: 'GET',
      method: 'GET',
      target: '/profile',
    },
  ],
}

describe('buildLocalReplayPreflight', () => {
  it('uses seeded variables and parameters as available replay variables', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      parameters: [
        {
          name: 'user_id',
          type: 'file',
          data_source: 'users',
          column: 'id',
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          inline_data: 'id\n42',
          strategy: 'sequential',
        },
      ],
      actions: [
        {
          name: 'Get user',
          type: 'GET',
          method: 'GET',
          target: '/users/{{var.account_id}}/{{var.user_id}}/{{param.user_id}}/{{data.users.id}}',
          headers: { Authorization: 'Bearer {{env.API_TOKEN}}' },
        },
      ],
    }, { account_id: 'acct-1' })

    expect(preflight.valid).toBe(true)
    expect(preflight.missing_variables).toEqual([])
    expect(preflight.required_secrets).toEqual(['API_TOKEN'])
    expect(preflight.checks?.find(check => check.id === 'secrets')?.status).toBe('warn')
  })

  it('makes extractor variables available only to later actions', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        {
          name: 'Create session',
          type: 'POST',
          method: 'POST',
          target: '/session/{{var.session_id}}',
          extractors: [
            { name: 'session_id', source: 'body', type: 'json_path', expression: '$.id' },
            { name: 'csrf_token', source: 'body', type: 'json_path', expression: '$.csrf' },
          ],
        },
        {
          name: 'Use token',
          type: 'GET',
          method: 'GET',
          target: '/session/{{var.session_id}}?csrf={{var.csrf_token}}',
        },
      ],
    })

    expect(preflight.valid).toBe(false)
    expect(preflight.missing_variables).toEqual(['session_id'])
    expect(preflight.checks?.find(check => check.id === 'variables')?.detail).toBe('session_id')
  })

  it('flags empty and missing data source bindings', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
        },
      ],
      parameters: [
        {
          name: 'account_id',
          type: 'file',
          data_source: 'accounts',
          column: 'id',
        },
      ],
    })

    expect(preflight.valid).toBe(false)
    expect(preflight.warnings).toEqual([
      'Data source users needs a file path or inline data.',
      'Parameter account_id references missing data source accounts.',
    ])
    expect(preflight.checks?.find(check => check.id === 'data_sources')?.status).toBe('fail')
  })

  it('counts init, actions, and teardown in replay order', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      config: {
        base_url: 'https://app.example.com',
        browser: {
          engine: 'webkit',
          capture_screenshots: 'on_failure',
        },
      },
      init: [
        { name: 'Init', type: 'GET', method: 'GET', target: '/init' },
      ],
      actions: [
        { name: 'Open', type: 'browser_navigate', target: '/' },
      ],
      teardown: [
        { name: 'Logout', type: 'GET', method: 'GET', target: '/logout' },
      ],
    } as ScriptDefinition & {
      init: ScriptActionDefinition[]
      teardown: ScriptActionDefinition[]
    })

    expect(preflight.action_count).toBe(3)
    expect(preflight.browser_required).toBe(true)
    expect(preflight.runtime_prerequisites).toEqual([
      'Browser engine: webkit',
      'Screenshot capture: on_failure',
    ])
    expect(preflight.checks?.find(check => check.id === 'runtime')?.status).toBe('warn')
  })
})
