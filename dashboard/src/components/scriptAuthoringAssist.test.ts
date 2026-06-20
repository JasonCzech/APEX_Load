import { afterEach, describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { getCachedDslActionCatalog, setCachedDslActionCatalog } from './dslActionCatalog'
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
      source: 'enterprise_script_archive',
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

  it('does not flag backend-deduped action names as duplicates', () => {
    // Importers now emit case-insensitively-unique names (e.g. an Aura
    // token-refresh sequence becomes c_AuraToken_URL / _2 / _3), which must NOT
    // trip the duplicate-name warning.
    const script: ScriptDefinition = {
      ...importedScript,
      actions: ['c_AuraToken_URL', 'c_AuraToken_URL_2', 'c_AuraToken_URL_3'].map((name) => ({
        name,
        type: 'GET',
        method: 'GET',
        target: '/aura',
        headers: {},
        body: '',
        extractors: [],
        assertions: [],
      })),
    }

    const issues = collectAuthoringIssues(script)
    expect(issues.some((issue) => issue.message.includes('duplicated'))).toBe(false)
  })

  describe('target-required validation with the DSL catalog not yet loaded', () => {
    const previousCatalog = getCachedDslActionCatalog()
    afterEach(() => {
      setCachedDslActionCatalog(previousCatalog)
    })

    // Reproduces the import-time false positive: the catalog fetch is async, so
    // the first validation pass runs with cachedCatalog === null and the legacy
    // heuristic governs. Target-less importer types must NOT be flagged.
    it('does not flag target-less imported action types as missing a target', () => {
      setCachedDslActionCatalog(null)
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: '99999', type: 'custom_call', target: '', headers: {}, body: '', extractors: [], assertions: [], params: { function: 'web_set_max_html_param_len' } },
          { name: 'T01_LaunchSFApp', type: 'http_sequence', target: '', headers: {}, body: '', extractors: [], assertions: [], params: { requests: [{ name: 'a', type: 'GET', target: '/x' }] } },
          { name: 'T02_Marker', type: 'source_transaction', target: '', headers: {}, body: '', extractors: [], assertions: [] },
        ],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((issue) => issue.message.includes('target is required'))).toBe(false)
    })

    it('still flags HTTP-method actions with an empty target', () => {
      setCachedDslActionCatalog(null)
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: 'orphan_get', type: 'GET', method: 'GET', target: '', headers: {}, body: '', extractors: [], assertions: [] },
        ],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((issue) => issue.message.includes('target is required'))).toBe(true)
    })
  })

  it('still flags genuine case-insensitive collisions as duplicates', () => {
    // The dashboard folds case (normalizeTokenName); names differing only by
    // case are real collisions the backend must resolve.
    const script: ScriptDefinition = {
      ...importedScript,
      actions: ['c_AuraToken_URL', 'c_AuraToken_url'].map((name) => ({
        name,
        type: 'GET',
        method: 'GET',
        target: '/aura',
        headers: {},
        body: '',
        extractors: [],
        assertions: [],
      })),
    }

    const issues = collectAuthoringIssues(script)
    expect(issues.some((issue) => issue.message.includes('duplicated'))).toBe(true)
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

  it('splices main actions after the selected action when a target index is supplied', () => {
    const component = {
      id: 'cart-group',
      kind: 'request_group',
      name: 'Cart Request',
      protocols: ['http'],
      summary: 'Add to cart.',
      init: [{ name: 'Warm Up', type: 'GET', method: 'GET', target: '/warm', headers: {}, body: '' }],
      actions: [{ name: 'Add To Cart', type: 'POST', method: 'POST', target: '/cart', headers: {}, body: '' }],
      teardown: [{ name: 'Clear Cart', type: 'POST', method: 'POST', target: '/cart/clear', headers: {}, body: '' }],
    }

    // importedScript has two main actions (indices 0 and 1). Insert after index 0.
    const targeted = applyAuthoringComponentToScript(importedScript, component, 0)
    expect(targeted.script.actions).toHaveLength(5)
    // Main action lands between the two originals; it carries no phase tag.
    expect(targeted.script.actions[1].name).toBe('Add_To_Cart')
    expect(targeted.script.actions[1].metadata?.phase).toBeUndefined()
    // init/teardown stay appended (grouped by phase in the outline regardless of position).
    expect(targeted.script.actions[3].metadata?.phase).toBe('init')
    expect(targeted.script.actions[4].metadata?.phase).toBe('teardown')

    // With no target index the block appends as before (init, main, teardown).
    const appended = applyAuthoringComponentToScript(importedScript, component, null)
    expect(appended.script.actions.slice(-3).map(action => action.name)).toEqual(['Warm_Up', 'Add_To_Cart', 'Clear_Cart'])

    // An out-of-range index falls back to appending.
    const fallback = applyAuthoringComponentToScript(importedScript, component, 99)
    expect(fallback.script.actions.slice(-3).map(action => action.name)).toEqual(['Warm_Up', 'Add_To_Cart', 'Clear_Cart'])
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

  describe('control-flow validation', () => {
    it('flags a control_loop with no count, condition, or for-each source', () => {
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: 'Loop', type: 'control_loop', target: '', headers: {}, body: '', extractors: [], assertions: [], control: { steps: [] } },
        ] as unknown as ScriptDefinition['actions'],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((i) => i.actionIndex === 0 && /needs a count, a condition, or a for-each/.test(i.message))).toBe(true)
    })

    it('flags a control_if with neither then-steps nor else-steps', () => {
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: 'Branch', type: 'control_if', target: '', headers: {}, body: '', extractors: [], assertions: [], control: {} },
        ] as unknown as ScriptDefinition['actions'],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((i) => i.actionIndex === 0 && /control_if needs then-steps or else-steps/.test(i.message))).toBe(true)
    })

    it('flags a for-each control_loop missing its data source', () => {
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: 'Loop', type: 'control_loop', target: '', headers: {}, body: '', extractors: [], assertions: [], control: { for_each: { var: 'row' }, steps: [{ name: 's', type: 'GET', target: '/x' }] } },
        ] as unknown as ScriptDefinition['actions'],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((i) => /for-each loop requires a data source/.test(i.message))).toBe(true)
    })

    it('accepts a for-each control_loop with a data source', () => {
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          { name: 'Loop', type: 'control_loop', target: '', headers: {}, body: '', extractors: [], assertions: [], control: { for_each: { source: 'users', var: 'row' }, steps: [{ name: 's', type: 'GET', target: '/x' }] } },
        ] as unknown as ScriptDefinition['actions'],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((i) => /control_loop needs|for-each loop requires/.test(i.message))).toBe(false)
    })

    it('flags a malformed control block nested inside a control_if', () => {
      const script: ScriptDefinition = {
        ...importedScript,
        actions: [
          {
            name: 'Outer', type: 'control_if', target: '', headers: {}, body: '', extractors: [], assertions: [],
            control: { condition: { left: '{{var.x}}', op: 'eq', right: '1' }, steps: [{ name: 'InnerLoop', type: 'control_loop', target: '', control: { steps: [] } }] },
          },
        ] as unknown as ScriptDefinition['actions'],
      }
      const issues = collectAuthoringIssues(script)
      expect(issues.some((i) => i.actionIndex === 0 && /nested step 1 → control_loop needs/.test(i.message))).toBe(true)
    })
  })
})
