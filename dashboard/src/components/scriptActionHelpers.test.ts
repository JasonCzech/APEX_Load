import { afterEach, describe, expect, it } from 'vitest'

import { actionRowLabel, actionTypeSummary, humanizeActionType } from './scriptActionHelpers'
import { setCachedDslActionCatalog, type DslActionCatalog } from './dslActionCatalog'
import type { ScriptActionDefinition } from '../types/scripts'

function action(partial: Partial<ScriptActionDefinition>): ScriptActionDefinition {
  return { name: '', type: '', ...partial } as ScriptActionDefinition
}

const catalog: DslActionCatalog = {
  protocols: ['http'],
  common_fields: [],
  actions: [
    { type: 'source_transaction', protocols: ['*'], summary: 'Marks a transaction boundary.' },
    { type: 'GET', protocols: ['http'], summary: 'Sends an HTTP GET request.' },
  ],
}

afterEach(() => {
  setCachedDslActionCatalog(null)
})

describe('humanizeActionType', () => {
  it('title-cases and de-snakes the type', () => {
    expect(humanizeActionType('source_transaction')).toBe('Source transaction')
    expect(humanizeActionType('ws_send')).toBe('Ws send')
  })

  it('leaves a single token as-is apart from capitalization', () => {
    expect(humanizeActionType('GET')).toBe('GET')
  })

  it('falls back to "Action" for an empty type', () => {
    expect(humanizeActionType('')).toBe('Action')
  })
})

describe('actionRowLabel', () => {
  it('uses the author-given name when present', () => {
    expect(actionRowLabel(action({ name: 'Login', type: 'POST' }))).toBe('Login')
  })

  it('humanizes the type instead of showing a blank/Unnamed label', () => {
    expect(actionRowLabel(action({ name: '', type: 'source_transaction' }))).toBe('Source transaction')
    expect(actionRowLabel(action({ name: '   ', type: 'ws_send' }))).toBe('Ws send')
  })
})

describe('actionTypeSummary', () => {
  it('returns the catalog summary when the catalog is loaded', () => {
    expect(actionTypeSummary(action({ type: 'source_transaction' }), catalog)).toBe('Marks a transaction boundary.')
  })

  it('reads the module-cached catalog when none is passed explicitly', () => {
    setCachedDslActionCatalog(catalog)
    expect(actionTypeSummary(action({ type: 'GET' }))).toBe('Sends an HTTP GET request.')
  })

  it('falls back to a humanized type when the type is not in the catalog', () => {
    expect(actionTypeSummary(action({ type: 'mystery_step' }), catalog)).toBe('Mystery step')
  })

  it('falls back when the catalog has not loaded', () => {
    expect(actionTypeSummary(action({ type: 'source_transaction' }), null)).toBe('Source transaction')
  })
})
