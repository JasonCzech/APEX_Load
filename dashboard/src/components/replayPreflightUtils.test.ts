import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import {
  buildLocalReplayPreflight,
  reconcileReplayPreflightSecretStatus,
  replayPreflightStatusLabel,
} from './replayPreflightUtils'

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
  it('reports durable execution blockers as replay-blocking readiness failures', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        metadata: {
          execution_blocker: 'credential_parameter_source_redacted',
        },
      }],
    })

    expect(preflight.valid).toBe(false)
    expect(preflight.issues).toEqual([
      'actions[0] "Get profile": execution blocker credential_parameter_source_redacted blocks replay until the imported behavior is resolved',
    ])
    expect(preflight.checks?.find(check => check.id === 'review_gates')).toMatchObject({
      status: 'fail',
      detail: expect.stringContaining('credential_parameter_source_redacted'),
    })
    expect(replayPreflightStatusLabel(preflight)).toBe('Needs attention')
  })

  it('finds execution blockers throughout sequence and deeply nested control surfaces', () => {
    let nested: ScriptActionDefinition = {
      name: 'Deep blocked request',
      type: 'GET',
      method: 'GET',
      target: '/deep',
      metadata: { execution_blocker: 'deep_import_gate' },
    }
    for (let depth = 0; depth < 33; depth += 1) {
      nested = {
        name: `Loop ${depth}`,
        type: 'control_loop',
        target: '',
        control: { steps: [nested], count: 1 },
      }
    }
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        nested,
        {
          name: 'Sequence',
          type: 'http_sequence',
          target: '',
          params: {
            requests: [{
              name: 'Blocked child',
              type: 'GET',
              target: '/child',
              metadata: { execution_blockers: ['second_gate', 'first_gate'] },
            }],
          },
        },
      ],
    })

    expect(preflight.valid).toBe(false)
    expect(preflight.issues).toEqual(expect.arrayContaining([
      expect.stringContaining('deep_import_gate'),
      'actions[1].params.requests[0] "Blocked child": execution blocker first_gate, second_gate blocks replay until the imported behavior is resolved',
    ]))
  })

  it('mirrors durable importer and browser review-policy gates', () => {
    const durable = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        metadata: { review_gate: 'bruno_missing_variables' },
      }],
    })
    expect(durable.valid).toBe(false)
    expect(durable.issues).toEqual([
      'actions[0] "Get profile": review-required action blocks replay until review metadata is removed',
    ])

    const browserAction: ScriptActionDefinition = {
      name: 'Evaluate imported JavaScript',
      type: 'BROWSER_EVALUATE_JS',
      target: 'document.title',
      metadata: { review_gate: 'javascript' },
    }
    const blocked = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      config: {
        base_url: 'https://app.example.com',
        browser: { review_policy: { allow_reviewed_javascript: false } },
      },
      actions: [browserAction],
    })
    expect(blocked.valid).toBe(false)
    expect(blocked.issues).toEqual([
      'actions[0] "Evaluate imported JavaScript": review-gated JavaScript requires config.browser.review_policy.allow_reviewed_javascript=true before replay',
    ])

    const allowed = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      config: {
        base_url: 'https://app.example.com',
        browser: { review_policy: { allow_reviewed_javascript: true } },
      },
      actions: [browserAction],
    })
    expect(allowed.valid).toBe(true)
    expect(allowed.issues).toEqual([])
  })

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
          headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
        },
      ],
    }, { account_id: 'acct-1' })

    expect(preflight.valid).toBe(true)
    expect(preflight.missing_variables).toEqual([])
    expect(preflight.required_secrets).toEqual(['API_TOKEN'])
    expect(preflight.missing_secrets).toEqual(['API_TOKEN'])
    expect(preflight.checks?.find(check => check.id === 'secrets')?.status).toBe('warn')
  })

  it('detects project secret references in script configuration without treating env values as project secrets', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      config: {
        base_url: 'https://api.example.com',
        http: {
          auth: {
            type: 'basic',
            username: '{{env.API_USERNAME}}',
            password: '{{secret.LOGIN_PASSWORD}}',
          },
        },
      },
    })

    expect(preflight.required_secrets).toEqual(['LOGIN_PASSWORD'])
    expect(preflight.missing_secrets).toEqual(['LOGIN_PASSWORD'])
  })

  it('collects dedicated logical secret fields while excluding provenance and review-only actions', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      config: {
        ...baseScript.config,
        database: { dsn_secret: 'database.dsn' },
      },
      metadata: {
        import: {
          source: 'fixture',
          preserved: { source_secret: '{{secret.PROVENANCE_ONLY}}' },
        },
      },
      actions: [
        ...baseScript.actions,
        {
          name: 'Source review',
          type: 'source_review',
          target: '{{secret.REVIEW_ONLY}}',
          params: { dsn_secret: 'REVIEW_DSN' },
        },
      ],
    })

    expect(preflight.required_secrets).toEqual(['DATABASE_DSN'])
  })

  it('uses authoritative project status so configured references are not shown as missing', () => {
    const local = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
      }],
    })

    const reconciled = reconcileReplayPreflightSecretStatus(local, [
      { name: 'API_TOKEN', configured: true },
    ], true)

    expect(reconciled.required_secrets).toEqual(['API_TOKEN'])
    expect(reconciled.missing_secrets).toEqual([])
    expect(reconciled.checks?.find(check => check.id === 'secrets')).toMatchObject({
      status: 'pass',
      detail: '1 project secret configured.',
    })
  })

  it('makes authoritative missing project secrets a blocking preflight prerequisite', () => {
    const local = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
      }],
    })

    const reconciled = reconcileReplayPreflightSecretStatus(local, [
      { name: 'API_TOKEN', configured: false },
    ], true)

    expect(reconciled.valid).toBe(false)
    expect(reconciled.missing_secrets).toEqual(['API_TOKEN'])
    expect(reconciled.checks?.find(check => check.id === 'secrets')).toMatchObject({
      status: 'fail',
      detail: 'Configure missing project secret: API_TOKEN.',
    })
  })

  it('does not claim draft references are missing before the project-scoped status probe completes', () => {
    const local = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
      }],
    })

    const checking = reconcileReplayPreflightSecretStatus(local, [], false)

    expect(checking.valid).toBe(false)
    expect(checking.missing_secrets).toEqual([])
    expect(checking.checks?.find(check => check.id === 'secrets')?.detail).toBe('Checking project secret configuration…')
  })

  it('distinguishes an unavailable project status probe from a missing secret', () => {
    const local = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{
        ...baseScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
      }],
    })

    const unavailable = reconcileReplayPreflightSecretStatus(local, [], false, true)

    expect(unavailable.valid).toBe(false)
    expect(unavailable.missing_secrets).toEqual([])
    expect(unavailable.checks?.find(check => check.id === 'secrets')?.detail).toBe('Project secret status is temporarily unavailable.')
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

    // HTTP replay sends the unresolved reference as placeholder text and warns.
    expect(preflight.valid).toBe(true)
    expect(preflight.missing_variables).toEqual(['session_id'])
    expect(preflight.checks?.find(check => check.id === 'variables')).toMatchObject({ status: 'warn', detail: 'Sent as placeholder text: session_id' })
    expect(preflight.warnings).toEqual(['variable(s) with no seed or preceding extractor are sent as placeholder text: session_id'])
  })

  it('counts a live-table step’s results as set for later steps', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        { name: 'Too early', type: 'GET', method: 'GET', target: '/jobs/{{var.job.values.id}}' },
        { name: 'Lease', type: 'live_table', target: '', params: { alias: 'jobs', operation: 'lease', result_prefix: 'job' } },
        { name: 'Use', type: 'GET', method: 'GET', target: '/jobs/{{var.job.values.id}}?v={{var.job.record_version}}&j={{var.jobless}}' },
      ],
    } as unknown as ScriptDefinition)
    // The use before the step stays flagged; a name that merely starts the same does not count.
    expect(preflight.missing_variables).toEqual(['job.values.id', 'jobless'])
  })

  it('blocks a missing variable that has no placeholder fallback', () => {
    const jsonHelper = buildLocalReplayPreflight({
      ...baseScript,
      actions: [{ name: 'Eval', type: 'source_json', target: '', params: { json_object: 'doc', buffer: '{{var.document}}' } }],
    })
    const browser = buildLocalReplayPreflight({
      ...baseScript,
      protocol: 'browser',
      actions: [{ name: 'Open', type: 'browser_navigate', target: '/stores/{{var.store}}' }],
    })
    expect(jsonHelper.valid).toBe(false)
    expect(jsonHelper.checks?.find(check => check.id === 'variables')).toMatchObject({ status: 'fail', detail: 'Unresolved: document' })
    expect(browser.valid).toBe(false)
    expect(browser.checks?.find(check => check.id === 'variables')?.status).toBe('fail')
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

	it('walks control_switch conditions, branches, review gates, and replay counts', () => {
		const preflight = buildLocalReplayPreflight({
			...baseScript,
			actions: [{
				name: 'Route', type: 'control_switch', target: '',
				control: {
					cases: [
						{ condition: { expr: 'var.route == "fast"' }, steps: [{ name: 'Fast', type: 'GET', target: '/fast/{{var.fast_id}}' }] },
						{ condition: { left: '{{var.route}}', op: 'eq', right: 'slow' }, steps: [{ name: 'Blocked', type: 'GET', target: '/slow', metadata: { execution_blocker: 'switch_gate' } }] },
					],
					default: [{ name: 'Fallback', type: 'GET', target: '/fallback' }],
				},
			}],
		}, { route: 'fast' })

		expect(preflight.valid).toBe(false)
		expect(preflight.issues).toEqual(expect.arrayContaining([expect.stringContaining('switch_gate')]))
		expect(preflight.action_count).toBe(3)
		expect(preflight.expected_action_count).toBe(1)
		expect(preflight.missing_variables).toEqual(['fast_id'])
	})

  it('treats http_sequence child extractors as available replay variables', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        {
          name: 'Create BAN',
          type: 'http_sequence',
          method: 'SEQUENCE',
          target: '/create',
          params: {
            requests: [
              {
                name: 'Submit',
                method: 'POST',
                target: '/create',
                extractors: [
                  { name: 'bannumber', source: 'body', type: 'regex', expression: 'BAN=(\\d+)' },
                  { name: 'error_message', source: 'body', type: 'regex', expression: 'Error=(.+)' },
                ],
              },
              {
                name: 'Confirm',
                method: 'POST',
                target: '/confirm',
                body: 'ban={{var.bannumber}}',
              },
            ],
          },
        },
        {
          name: 'Use BAN',
          type: 'POST',
          method: 'POST',
          target: '/ban',
          body: 'ban={{var.bannumber}}&message={{var.error_message}}',
        },
      ],
    })

    expect(preflight.missing_variables).toEqual([])
    expect(preflight.checks?.find(check => check.id === 'variables')?.status).toBe('pass')
  })

  it('walks http_sequence child extractors inside control-flow bodies', () => {
    const preflight = buildLocalReplayPreflight({
      ...baseScript,
      actions: [
        {
          name: 'Reserve Products',
          type: 'control_loop',
          target: '',
          control: {
            count: 1,
            steps: [
              {
                name: 'Get Available Sub List',
                type: 'http_sequence',
                method: 'SEQUENCE',
                target: '/available',
                params: {
                  requests: [
                    {
                      name: 'Submit',
                      method: 'POST',
                      target: '/available',
                      extractors: [
                        { name: 'subno', source: 'body', type: 'regex_ordinal', expression: 'SUB=(\\d+)' },
                      ],
                    },
                  ],
                },
              },
              {
                name: 'Set Product Number',
                type: 'http_sequence',
                method: 'SEQUENCE',
                target: '/set-product',
                params: {
                  requests: [
                    {
                      name: 'Submit',
                      method: 'POST',
                      target: '/set-product',
                      body: 'subNo={{var.subno}}',
                    },
                  ],
                },
              },
            ],
          },
        },
      ],
    })

    expect(preflight.missing_variables).toEqual([])
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
  it.each(['http', 'java_vuser', 'sap_gui'])('does not claim %s runtime availability from static checks', protocol => {
    const preflight = buildLocalReplayPreflight({ ...baseScript, protocol, actions: [] })
    expect(preflight.checks?.find(check => check.id === 'runtime')).toMatchObject({ status: 'warn' })
  })

})
