import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import {
  applyMigrationAssistantSuggestion,
  buildImportedDraftDiffSummary,
  buildMigrationAssistantSuggestions,
  buildProtocolStarterTemplates,
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

    expect(browserTemplates.map((template) => template.label)).toContain('Navigate')
    expect(browserTemplates.some((template) => template.action.type === 'browser_fill')).toBe(true)
    expect(httpTemplates.some((template) => template.action.type === 'POST')).toBe(true)
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
})
