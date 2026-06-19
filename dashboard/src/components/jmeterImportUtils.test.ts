import { describe, expect, it } from 'vitest'

import { summarizeImportExtractors, summarizeSupportMatrix, summarizeSupportMatrixEntries } from './jmeterImportUtils'

describe('jmeterImportUtils', () => {
  it('summarizes generated extractors for review rendering', () => {
    const summary = summarizeImportExtractors([
      {
        name: 'Auth_Users',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://example.com' },
        actions: [
          {
            name: 'Create_Session',
            type: 'http',
            target: '/session',
            extractors: [
              { name: 'session_token', source: 'response', type: 'regex', expression: 'token=([^&]+)' },
              { name: 'access_token', source: 'response', type: 'jsonpath', expression: '$.access_token' },
            ],
          },
        ],
      },
    ])

    expect(summary).toEqual([
      {
        scriptName: 'Auth_Users',
        actionName: 'Create_Session',
        extractorName: 'session_token',
        extractorType: 'regex',
        expression: 'token=([^&]+)',
      },
      {
        scriptName: 'Auth_Users',
        actionName: 'Create_Session',
        extractorName: 'access_token',
        extractorType: 'jsonpath',
        expression: '$.access_token',
      },
    ])
  })

  it('summarizes support matrix sections for inline coverage review', () => {
    const sections = summarizeSupportMatrix({
      supported: ['Thread Group', 'HTTP Request Defaults', 'Regex Extractor'],
      partial: ['Variable substitution across URLs, headers, and bodies'],
      unsupported: ['JSR223 / BeanShell / Groovy scripting', 'JMS samplers'],
      notes: ['HTTP focused'],
    })

    expect(sections).toEqual([
      {
        key: 'supported',
        title: 'Supported',
        itemLabel: 'constructs',
        items: ['Thread Group', 'HTTP Request Defaults', 'Regex Extractor'],
        preview: 'Thread Group • HTTP Request Defaults • Regex Extractor',
      },
      {
        key: 'partial',
        title: 'Partial',
        itemLabel: 'behaviors',
        items: ['Variable substitution across URLs, headers, and bodies'],
        preview: 'Variable substitution across URLs, headers, and bodies',
      },
      {
        key: 'unsupported',
        title: 'Unsupported',
        itemLabel: 'constructs',
        items: ['JSR223 / BeanShell / Groovy scripting', 'JMS samplers'],
        preview: 'JSR223 / BeanShell / Groovy scripting • JMS samplers',
      },
    ])
  })

  it('groups structured entries into coverage categories by execution status', () => {
    const sections = summarizeSupportMatrixEntries([
      { id: 'http', area: 'Samplers', construct: 'HTTP Request samplers', status: 'supported', execution_status: 'executable' },
      { id: 'jdbc', area: 'Samplers', construct: 'JDBC Request samplers', status: 'supported', execution_status: 'executable' },
      { id: 'jsr223', area: 'Scripting', construct: 'JSR223 scripting', status: 'partial', execution_status: 'review_required' },
      { id: 'jms', area: 'Samplers', construct: 'JMS samplers', status: 'review_only', execution_status: 'adapter_required' },
      { id: 'plugins', area: 'Plugins', construct: 'Unknown plugins', status: 'review_only', execution_status: 'not_executable' },
    ])

    expect(sections.map(section => section.key)).toEqual(['mapped', 'review_required', 'adapter_required', 'unsupported'])
    const mapped = sections.find(section => section.key === 'mapped')
    expect(mapped?.items.map(item => item.construct)).toEqual(['HTTP Request samplers', 'JDBC Request samplers'])
    const adapter = sections.find(section => section.key === 'adapter_required')
    expect(adapter?.items).toHaveLength(1)
    expect(adapter?.items[0].construct).toBe('JMS samplers')
  })

  it('returns no coverage sections when there are no structured entries', () => {
    expect(summarizeSupportMatrixEntries(undefined)).toEqual([])
    expect(summarizeSupportMatrixEntries([])).toEqual([])
  })
})
