import { describe, expect, it } from 'vitest'

import { jmeterSourceComments, summarizeImportExtractors, summarizeSupportMatrix, summarizeSupportMatrixEntries } from './jmeterImportUtils'

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

describe('jmeterScenarioPopulation', () => {
  it('carries thread-group load to the saved scripts by name and order', async () => {
    const { jmeterScenarioPopulation, generatedParameterCount } = await import('./jmeterImportUtils')
    const population = jmeterScenarioPopulation({
      name: 'Plan', duration: '', termination_mode: 'iterations', scenario_mode: 'vuser_driven',
      groups: [
        { name: 'B', script_name: 'Beta', vusers: 5, ramp_up: '2s', initialization: 'at_runtime' },
        { name: 'A', script_name: 'Alpha', vusers: 50, ramp_up: '1s', iterations: 200, initialization: 'at_runtime' },
      ],
    }, [{ name: 'Alpha' }, { name: 'Beta' }, { name: 'Gamma' }])
    expect(population).toEqual({
      termination_mode: 'iterations',
      groups: [
        { vusers: 50, ramp_up: '1s', iterations: 200, initialization: 'at_runtime' },
        { vusers: 5, ramp_up: '2s', iterations: undefined, initialization: 'at_runtime' },
        undefined,
      ],
    })
    expect(jmeterScenarioPopulation(undefined, [{ name: 'Alpha' }])).toBeUndefined()
    // Without a draft (or a group for the script), the conversion's preserved
    // thread-group load seeds the scenario.
    const preserved = { import: { preserved: { source_population: { num_threads: 8, ramp_time_seconds: 4, loops: 2 } } } }
    expect(jmeterScenarioPopulation(undefined, [{ name: 'Alpha', metadata: preserved }])).toEqual({
      termination_mode: 'iterations',
      groups: [{ vusers: 8, ramp_up: '4s', iterations: 2 }],
    })
    expect(jmeterScenarioPopulation({ name: 'Plan', duration: '1m', termination_mode: 'duration', scenario_mode: 'vuser_driven', groups: [{ name: 'A', script_name: 'Alpha', vusers: 3, initialization: 'at_runtime' }] }, [{ name: 'Alpha' }, { name: 'Beta', metadata: preserved }])?.groups[1]).toEqual({ vusers: 8, ramp_up: '4s', iterations: 2 })
    expect(generatedParameterCount([
      { name: 'a', version: '1', protocol: 'http', config: {}, actions: [], parameters: [{ name: 'c', type: 'counter' }, { name: 'd', type: 'date_time' }] },
      { name: 'b', version: '1', protocol: 'http', config: {}, actions: [] },
    ] as never)).toBe(2)
  })
})

describe('generatedParameterCount', () => {
  it('counts a generated parameter once per plan, by type, name and source scope, as the converter does', async () => {
    const { generatedParameterCount } = await import('./jmeterImportUtils')
    // A Test Plan-scope Counter is attached to both scripts; a same-named
    // counter from another scope and a timestamp of the same name are distinct.
    const planCounter = { name: 'n', type: 'counter', scope_id: 'plan/counter' }
    expect(generatedParameterCount([
      { name: 'a', version: '1', protocol: 'http', config: {}, actions: [], parameters: [planCounter, { name: 'n', type: 'counter', scope_id: 'a/counter' }, { name: 'n', type: 'date_time', scope_id: 'plan/counter' }] },
      { name: 'b', version: '1', protocol: 'http', config: {}, actions: [], parameters: [planCounter, { name: 'r', type: 'random_number' }] },
    ] as never)).toBe(4)
    // Parameters the converter did not generate from config elements are not counted.
    expect(generatedParameterCount([
      { name: 'a', version: '1', protocol: 'http', config: {}, actions: [], parameters: [{ name: 'rows', type: 'file' }, { name: 'id', type: 'uuid' }] },
    ] as never)).toBe(0)
  })
})

describe('jmeterSourceComments', () => {
  it('reads a thread group\'s comments from the script and the Test Plan\'s from the scenario draft', () => {
    expect(jmeterSourceComments({ metadata: { import: { preserved: { source_comments: '  Billing System = TLG \n' } } } })).toBe('Billing System = TLG')
    expect(jmeterSourceComments({ metadata: { source_comments: 'Owned by billing' } })).toBe('Owned by billing')
    expect(jmeterSourceComments({ metadata: { import: { preserved: { source_comments: 42 } } } })).toBe('')
    expect(jmeterSourceComments({})).toBe('')
    expect(jmeterSourceComments(undefined)).toBe('')
  })
})
