import { afterEach, describe, expect, it } from 'vitest'

import {
  dslActionNeedsExplicitTarget,
  dslActionTypesForProtocol,
  dslActionUsesSelector,
  dslActionUsesValue,
  dslProtocolFamily,
  dslSpecForActionType,
  setCachedDslActionCatalog,
  type DslActionCatalog,
} from './dslActionCatalog'

const testCatalog: DslActionCatalog = {
  protocols: ['browser', 'database', 'http', 'jms', 'kafka', 'websocket'],
  common_fields: [
    { name: 'name', required: true, kind: 'string' },
    { name: 'type', required: true, kind: 'string' },
  ],
  actions: [
    {
      type: 'browser_click',
      protocols: ['browser'],
      summary: 'Clicks an element.',
      fields: [
        { name: 'target', kind: 'string' },
        { name: 'selector', kind: 'string' },
        { name: 'sync', kind: 'object' },
      ],
      require_one_of: [['target', 'selector']],
    },
    {
      type: 'browser_press_key',
      protocols: ['browser'],
      summary: 'Presses a key.',
      fields: [
        { name: 'selector', kind: 'string' },
        { name: 'value', kind: 'string' },
        { name: 'params.key', kind: 'string' },
      ],
      require_one_of: [['value', 'params.key', 'params.key_name']],
    },
    {
      type: 'db_query',
      aliases: ['database_query', 'query', 'select'],
      protocols: ['database'],
      summary: 'Runs a SQL query.',
      fields: [
        { name: 'target', kind: 'string' },
        { name: 'body', kind: 'string' },
      ],
      require_one_of: [['target', 'body']],
    },
    {
      type: 'ws_connect',
      protocols: ['websocket'],
      summary: 'Opens the connection.',
      fields: [{ name: 'target', required: true, kind: 'string' }],
    },
    {
      type: 'jms_publish',
      protocols: ['jms'],
      summary: 'Publishes a message.',
      fields: [{ name: 'target', kind: 'string' }],
    },
    {
      type: 'source_transaction',
      protocols: ['*'],
      summary: 'Imported transaction marker.',
      fields: [{ name: 'params.function', kind: 'string' }],
    },
  ],
}

afterEach(() => setCachedDslActionCatalog(null))

describe('dslProtocolFamily', () => {
  it('collapses jms provider protocols onto the jms family', () => {
    expect(dslProtocolFamily('jms-solace')).toBe('jms')
    expect(dslProtocolFamily('jms-ibmmq')).toBe('jms')
    expect(dslProtocolFamily('http')).toBe('http')
  })
})

describe('dslSpecForActionType', () => {
  it('resolves canonical types, aliases, and legacy loadrunner_* spellings', () => {
    setCachedDslActionCatalog(testCatalog)
    expect(dslSpecForActionType('browser_click')?.type).toBe('browser_click')
    expect(dslSpecForActionType('select')?.type).toBe('db_query')
    expect(dslSpecForActionType('loadrunner_transaction')?.type).toBe('source_transaction')
    expect(dslSpecForActionType('nope')).toBeNull()
  })
})

describe('dslActionTypesForProtocol', () => {
  it('filters to the protocol family and includes engine actions where valid', () => {
    setCachedDslActionCatalog(testCatalog)
    const browserTypes = dslActionTypesForProtocol('browser').map(spec => spec.type)
    expect(browserTypes).toContain('browser_click')
    expect(browserTypes).toContain('source_transaction')
    expect(browserTypes).not.toContain('db_query')

    const jmsTypes = dslActionTypesForProtocol('jms-solace').map(spec => spec.type)
    expect(jmsTypes).toContain('jms_publish')
    expect(jmsTypes).not.toContain('source_transaction')
  })

  it('returns empty without a catalog', () => {
    expect(dslActionTypesForProtocol('browser')).toEqual([])
  })
})

describe('dslActionUsesSelector / dslActionUsesValue', () => {
  it('derives input visibility from catalog fields', () => {
    setCachedDslActionCatalog(testCatalog)
    expect(dslActionUsesSelector('browser_click')).toBe(true)
    expect(dslActionUsesValue('browser_click')).toBe(false)
    expect(dslActionUsesValue('browser_press_key')).toBe(true)
  })

  it('falls back to the legacy lists when the catalog is missing', () => {
    expect(dslActionUsesSelector('browser_click')).toBe(true)
    expect(dslActionUsesSelector('browser_navigate')).toBe(false)
    expect(dslActionUsesValue('browser_fill')).toBe(true)
    expect(dslActionUsesValue('browser_click')).toBe(false)
  })
})

describe('dslActionNeedsExplicitTarget', () => {
  const fallback = (actionType: string) => actionType.startsWith('browser_')

  it('uses required target and one-of groups from the catalog', () => {
    setCachedDslActionCatalog(testCatalog)
    expect(dslActionNeedsExplicitTarget('ws_connect', fallback)).toBe(true)
    expect(dslActionNeedsExplicitTarget('browser_click', fallback)).toBe(true)
    // press_key has no target requirement — the legacy heuristic false-flagged it.
    expect(dslActionNeedsExplicitTarget('browser_press_key', fallback)).toBe(false)
    // jms_publish target falls back to config.jms.destination.
    expect(dslActionNeedsExplicitTarget('jms_publish', fallback)).toBe(false)
    expect(dslActionNeedsExplicitTarget('source_transaction', fallback)).toBe(false)
  })

  it('delegates to the fallback when the catalog is missing or lacks the type', () => {
    expect(dslActionNeedsExplicitTarget('browser_press_key', fallback)).toBe(true)
    setCachedDslActionCatalog(testCatalog)
    expect(dslActionNeedsExplicitTarget('mystery_type', fallback)).toBe(false)
  })
})
