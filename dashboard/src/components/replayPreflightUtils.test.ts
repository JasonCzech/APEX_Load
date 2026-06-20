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
    expect(preflight.checks?.find(check => check.id === 'evidence_capture')).toMatchObject({
      status: 'pass',
      detail: 'Replay will capture screenshot, DOM, console, and network evidence for browser review.',
    })
  })

  it('walks nested control-flow bodies for variables and replay action counts', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      config: {
        base_url: 'https://app.example.com',
        browser: {
          engine: 'chromium',
        },
      },
      actions: [
        {
          name: 'Ready Branch',
          type: 'control_if',
          target: '',
          control: {
            condition: { left: '{{var.ready}}', op: 'eq', right: 'true' },
            steps: [
              {
                name: 'Create Cart',
                type: 'browser_click',
                target: '#create-cart',
                extractors: [
                  { name: 'cart_id', source: 'body', type: 'json_path', expression: '$.cart_id' },
                ],
              },
              {
                name: 'Add Items',
                type: 'control_loop',
                target: '',
                control: {
                  count: 3,
                  steps: [
                    { name: 'Add SKU', type: 'browser_click', target: '#sku-{{var.sku}}-{{var.cart_id}}' },
                  ],
                },
              },
            ],
            else: [
              { name: 'Guest Cart', type: 'browser_click', target: '#guest-cart' },
            ],
          },
        },
        {
          name: 'Checkout',
          type: 'browser_click',
          target: '#checkout-{{var.cart_id}}',
        },
      ],
    }, { ready: 'true' })

    expect(preflight.action_count).toBe(4)
    expect(preflight.expected_action_count).toBe(5)
    expect(preflight.missing_variables).toEqual(['sku'])
    expect(preflight.missing_variables).not.toContain('cart_id')
  })

  it('treats loop_index and foreach row bindings inside a loop body as available', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      data_sources: [
        { name: 'users', type: 'csv', path: '', inline_data: 'email\na@x.com', strategy: 'sequential' },
      ],
      actions: [
        {
          name: 'Each User',
          type: 'control_loop',
          target: '',
          control: {
            for_each: { source: 'users', var: 'row' },
            steps: [
              { name: 'Greet', type: 'GET', method: 'GET', target: '/u/{{var.row.email}}/pass/{{var.loop_index}}' },
            ],
          },
        },
      ],
    })

    expect(preflight.missing_variables).toEqual([])
    expect(preflight.checks?.find(check => check.id === 'variables')?.status).toBe('pass')
  })

  it('allows loop_index in a control_loop while-condition', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        {
          name: 'While',
          type: 'control_loop',
          target: '',
          control: {
            condition: { left: '{{var.loop_index}}', op: 'lt', right: '3' },
            steps: [
              { name: 'Tick', type: 'GET', method: 'GET', target: '/tick' },
            ],
          },
        },
      ],
    })

    expect(preflight.missing_variables).toEqual([])
  })

  it('still flags loop_index referenced outside any loop body', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        { name: 'Stray', type: 'GET', method: 'GET', target: '/x/{{var.loop_index}}' },
      ],
    })

    expect(preflight.missing_variables).toEqual(['loop_index'])
  })

  it('still flags a foreach row binding used outside its loop body', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      data_sources: [
        { name: 'users', type: 'csv', path: '', inline_data: 'email\na@x.com', strategy: 'sequential' },
      ],
      actions: [
        {
          name: 'Loop',
          type: 'control_loop',
          target: '',
          control: {
            for_each: { source: 'users', var: 'row' },
            steps: [
              { name: 'In', type: 'GET', method: 'GET', target: '/{{var.row.email}}' },
            ],
          },
        },
        { name: 'After', type: 'GET', method: 'GET', target: '/{{var.row.email}}' },
      ],
    })

    expect(preflight.missing_variables).toEqual(['row.email'])
  })

  it('warns when browser replay screenshot evidence is disabled', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      config: {
        base_url: 'https://app.example.com',
        browser: {
          engine: 'chromium',
          capture_screenshots: 'off',
        },
      },
      actions: [
        { name: 'Open', type: 'browser_navigate', target: '/' },
      ],
    })

    expect(preflight.valid).toBe(true)
    expect(preflight.checks?.find(check => check.id === 'evidence_capture')).toMatchObject({
      status: 'warn',
      detail: 'Screenshot capture is disabled; enable per_action or on_failure before using replay as promotion evidence.',
    })
  })
})
