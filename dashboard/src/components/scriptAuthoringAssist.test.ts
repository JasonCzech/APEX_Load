import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import {
  applyAuthoringComponentToScript,
  applyMigrationAssistantSuggestion,
  applyScriptRepairSuggestion,
  buildImportedDraftDiffSummary,
  buildMigrationAssistantSuggestions,
  buildProtocolStarterTemplates,
  buildScriptRepairSuggestions,
  collectAuthoringIssues,
  groupIssuesByAction,
  normalizeImportedActionNames,
} from './scriptAuthoringAssist'

const importedScript: ScriptDefinition = {
  name: 'Imported Checkout',
  version: '1.0.0',
  protocol: 'http',
  config: {
    base_url: 'https://api.example.test',
    think_time: { type: 'fixed', mean_ms: 0, ignore: true },
    pacing: { type: 'none' },
    run_logic: { auto_transactions: 'none' },
    error_handling: { continue_on_error: false },
    log: { level: 'standard' },
  },
  actions: [
    {
      name: ' Login Request ',
      type: 'POST',
      method: 'POST',
      target: '/login',
      headers: { 'Content-Type': 'application/json' },
      body: '{"username":"{{param.user_name}}","tenant":"{{data.users.tenant}}"}',
      extractors: [{ name: '', source: 'body', type: 'jsonpath', expression: '' }],
      assertions: [],
    },
    {
      name: 'Login Request',
      type: 'GET',
      method: 'GET',
      target: '',
      headers: {},
      body: '',
      extractors: [],
      assertions: [],
    },
  ],
  parameters: [],
  data_sources: [],
  metadata: {
    import: {
      source: 'loadrunner',
      source_script_name: 'checkout.usr',
      preserved: {},
    },
  },
}

describe('scriptAuthoringAssist', () => {
  it('collects action-level lint issues for missing references and malformed steps', () => {
    const issues = collectAuthoringIssues(importedScript)
    const grouped = groupIssuesByAction(issues, importedScript.actions.length)

    expect(issues.some((issue) => issue.message.includes('parameter "user_name"'))).toBe(true)
    expect(issues.some((issue) => issue.message.includes('data source "users"'))).toBe(true)
    expect(issues.some((issue) => issue.message.includes('missing a name or expression'))).toBe(true)
    expect(grouped[1].some((issue) => issue.message.includes('target is required'))).toBe(true)
    expect(grouped[1].some((issue) => issue.message.includes('duplicated'))).toBe(true)
  })

  it('builds protocol starter blocks for browser and HTTP drafts', () => {
    const browserTemplates = buildProtocolStarterTemplates('browser', 2)
    const httpTemplates = buildProtocolStarterTemplates('http', 0)
    const databaseTemplates = buildProtocolStarterTemplates('database', 0)
    const networkTemplates = buildProtocolStarterTemplates('network', 0)

    expect(browserTemplates.map((template) => template.label)).toContain('Navigate')
    expect(browserTemplates.some((template) => template.action.type === 'browser_fill')).toBe(true)
    expect(httpTemplates.some((template) => template.action.type === 'POST')).toBe(true)
    expect(databaseTemplates.some((template) => template.action.type === 'db_query')).toBe(true)
    expect(networkTemplates.some((template) => template.action.type === 'dns_query')).toBe(true)
  })

  it('does not require explicit targets for transaction and health native actions', () => {
    const script: ScriptDefinition = {
      ...importedScript,
      protocol: 'database',
      actions: [
        { name: 'Begin', type: 'db_begin', target: '', headers: {}, body: '' },
        { name: 'Health', type: 'network_health', target: '', headers: {}, body: '' },
      ],
    }

    const issues = collectAuthoringIssues(script)

    expect(issues.some(issue => issue.message.includes('target is required'))).toBe(false)
  })

  it('normalizes imported action names and applies migration suggestions', () => {
    const normalized = normalizeImportedActionNames(importedScript)
    const suggestionKeys = buildMigrationAssistantSuggestions(importedScript).map((suggestion) => suggestion.key)
    const pacingResult = applyMigrationAssistantSuggestion(importedScript, 'apply_gentle_pacing')

    expect(normalized.script.actions[0].name).toBe('Login_Request')
    expect(normalized.script.actions[1].name).toBe('Login_Request_2')
    expect(suggestionKeys).toContain('normalize_action_names')
    expect(suggestionKeys).toContain('normalize_think_time')
    expect(pacingResult.script.config.pacing?.type).toBe('fixed_delay')
    expect(pacingResult.script.config.pacing?.delay_ms).toBe(1000)
  })

  it('summarizes imported draft deltas against the working draft', () => {
    const currentDraft: ScriptDefinition = {
      ...importedScript,
      config: {
        ...importedScript.config,
        pacing: { type: 'fixed_delay', delay_ms: 1500 },
      },
      actions: [
        {
          ...importedScript.actions[0],
          name: 'Login_Request',
        },
        {
          ...importedScript.actions[1],
          name: 'Fetch_Profile',
          target: '/profile',
        },
        {
          name: 'Submit_Order',
          type: 'POST',
          method: 'POST',
          target: '/orders',
          headers: {},
          body: '',
          extractors: [],
          assertions: [],
        },
      ],
      parameters: [
        {
          name: 'user_name',
          type: 'list',
          update: 'each_iteration',
          values: ['demo'],
        },
      ],
      data_sources: [
        {
          name: 'users',
          type: 'csv',
          path: '',
          strategy: 'sequential',
          delimiter: ',',
          first_row_header: true,
          columns: [],
          inline_data: 'tenant\nperf',
        },
      ],
    }

    const summary = buildImportedDraftDiffSummary(importedScript, currentDraft)

    expect(summary.changed).toBe(true)
    expect(summary.configChanged).toBe(true)
    expect(summary.actionChanges).toBeGreaterThan(0)
    expect(summary.parameterChanges).toBe(1)
    expect(summary.dataSourceChanges).toBe(1)
    expect(summary.details.some((detail) => detail.includes('Action count changed'))).toBe(true)
  })

  it('inserts reusable authoring components into the editable script', () => {
    const result = applyAuthoringComponentToScript(importedScript, {
      id: 'oauth-flow',
      kind: 'auth_flow',
      name: 'OAuth Bearer Token Flow',
      protocols: ['http'],
      summary: 'Acquire a token and reuse it.',
      config: {
        http: {
          default_headers: {
            Authorization: 'Bearer {{var.access_token}}',
          },
        },
      },
      data_sources: [{
        name: 'users',
        type: 'csv',
        path: '',
        strategy: 'unique',
        delimiter: ',',
        first_row_header: true,
        columns: ['username'],
      }],
      parameters: [{
        name: 'username',
        type: 'file',
        data_source: 'users',
        column: 'username',
      }],
      init: [{
        name: 'Acquire Access Token',
        type: 'POST',
        method: 'POST',
        target: '/oauth/token',
        headers: {},
        body: '',
      }],
      actions: [{
        name: 'Lookup Entity',
        type: 'GET',
        method: 'GET',
        target: '/api/entities/{{data.users.entity_id}}',
        headers: {},
        body: '',
      }],
      teardown: [{
        name: 'Logout',
        type: 'POST',
        method: 'POST',
        target: '/logout',
        headers: {},
        body: '',
      }],
    })

    expect(result.summary).toContain('Inserted OAuth Bearer Token Flow')
    expect(result.script.config.http?.default_headers?.Authorization).toBe('Bearer {{var.access_token}}')
    expect(result.script.data_sources?.some(source => source.name === 'users')).toBe(true)
    expect(result.script.parameters?.some(parameter => parameter.name === 'username')).toBe(true)
    expect(result.script.actions.slice(-3).map(action => action.name)).toEqual(['Acquire_Access_Token', 'Lookup_Entity', 'Logout'])
    expect(result.script.actions[result.script.actions.length - 3]?.metadata?.phase).toBe('init')
    expect(result.script.actions[result.script.actions.length - 1]?.metadata?.phase).toBe('teardown')
  })

  it('builds and applies sample-backed repair suggestions', () => {
    const repairDraft: ScriptDefinition = {
      ...importedScript,
      actions: [
        {
          name: 'Submit Token',
          type: 'POST',
          method: 'POST',
          target: '/orders/{{data.orders.order_id}}',
          headers: { Authorization: 'Bearer hard-coded-token' },
          body: '{"session_id":"abc123"}',
          extractors: [],
          assertions: [],
        },
        {
          name: 'Click Submit',
          type: 'browser_click',
          target: '',
          selector: 'div:nth-child(2) > button',
          value: '',
        },
      ],
      parameters: [],
      data_sources: [],
    }

    const suggestions = buildScriptRepairSuggestions(repairDraft).map(suggestion => suggestion.key)
    expect(suggestions).toContain('create_missing_data_source')
    expect(suggestions).toContain('parameterize_hard_coded_tokens')
    expect(suggestions).toContain('add_default_assertions')
    expect(suggestions).toContain('stabilize_browser_waits')
    expect(suggestions).toContain('stabilize_browser_selectors')

    const withData = applyScriptRepairSuggestion(repairDraft, 'create_missing_data_source').script
    expect(withData.data_sources?.[0]?.name).toBe('orders')
    expect(withData.data_sources?.[0]?.inline_data).toContain('order_id')

    const withTokens = applyScriptRepairSuggestion(repairDraft, 'parameterize_hard_coded_tokens').script
    expect(withTokens.actions[0].headers?.Authorization).toBe('Bearer {{param.access_token}}')
    expect(withTokens.actions[0].body).toContain('{{param.session_id}}')
    expect(withTokens.parameters?.map(parameter => parameter.name)).toEqual(expect.arrayContaining(['access_token', 'session_id']))

    const withAssertions = applyScriptRepairSuggestion(repairDraft, 'add_default_assertions').script
    expect(withAssertions.actions[0].assertions?.[0]).toEqual({ type: 'status_code', expected: 201 })

    const withBrowserRepairs = applyScriptRepairSuggestion(
      applyScriptRepairSuggestion(repairDraft, 'stabilize_browser_waits').script,
      'stabilize_browser_selectors',
    ).script
    expect(withBrowserRepairs.actions[1].wait_for?.timeout_ms).toBe(10000)
    expect(withBrowserRepairs.actions[1].selector).toBe('[data-testid="click_submit"]')
    expect(withBrowserRepairs.actions[1].metadata?.original_selector).toContain('nth-child')
  })
})
