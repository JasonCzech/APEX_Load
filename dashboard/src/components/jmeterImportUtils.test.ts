import { describe, expect, it } from 'vitest'

import { summarizeImportExtractors, summarizeSupportMatrix } from './jmeterImportUtils'

describe('jmeterImportUtils', () => {
  it('summarizes generated extractors for review rendering', () => {
    const summary = summarizeImportExtractors([
      {
        name: 'Auth_Users',
        actions: [
          {
            name: 'Create_Session',
            extractors: [
              { name: 'session_token', type: 'regex', expression: 'token=([^&]+)' },
              { name: 'access_token', type: 'jsonpath', expression: '$.access_token' },
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
})
