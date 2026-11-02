// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  AuraEnvelopeEditor,
  AuraEnvelopePreview,
  AuraResponsePreview,
} from './AuraEnvelopeEditor'
import {
  encodeFormValuePreservingTemplates,
  formatAuraActionTitle,
  isAuraFormRequest,
  parseAuraFormEnvelope,
  parseAuraResponseEnvelope,
  serializeAuraFormEnvelope,
} from './auraEnvelopeModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function buildSampleAuraBody(): string {
  const message = JSON.stringify({
    actions: [
      {
        id: '128;a',
        descriptor: 'aura://ApexActionController/ACTION$execute',
        callingDescriptor: 'markup://c:customerSearch',
        params: {
          namespace: '',
          classname: 'InteractionController',
          method: 'searchCustomers',
          params: { accountId: '{{var.account_id}}', query: 'Acme' },
        },
      },
      {
        id: '129;a',
        descriptor: 'serviceComponent://ui.force.components.controllers.recordGlobalValueProvider.RecordGvpController/ACTION$getRecord',
        callingDescriptor: 'UNKNOWN',
        params: { recordId: '{{param.record_id}}' },
      },
    ],
  })
  const context = JSON.stringify({
    mode: 'PROD',
    fwuid: '{{var.aura_fwuid}}',
    app: 'siteforce:loginApp2',
  })
  return [
    `message=${encodeFormValuePreservingTemplates(message)}`,
    `aura.context=${encodeFormValuePreservingTemplates(context)}`,
    `aura.pageURI=${encodeFormValuePreservingTemplates('/lightning/r/Interaction__c/view')}`,
    'aura.token={{var.aura_token}}',
  ].join('&')
}

describe('AuraEnvelopeEditor', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('detects, parses, and round-trips /aura form envelopes while keeping literal {{...}} template tokens', () => {
    const body = buildSampleAuraBody()
    expect(isAuraFormRequest({ target: '/aura?r=12&aura.ApexAction.execute=1', body })).toBe(true)
    expect(isAuraFormRequest({ target: '/api/orders', body: '{"id":1}' })).toBe(false)

    const parsed = parseAuraFormEnvelope(body)
    expect(parsed).not.toBeNull()
    expect(parsed!.actions).toHaveLength(2)
    expect(formatAuraActionTitle(parsed!.actions[0], 0)).toBe('Apex · InteractionController.searchCustomers')
    expect(formatAuraActionTitle(parsed!.actions[1], 1)).toBe('RecordGvp.getRecord')
    expect(parsed!.token).toBe('{{var.aura_token}}')
    expect(parsed!.pageURI).toBe('/lightning/r/Interaction__c/view')

    const roundTripped = serializeAuraFormEnvelope(parsed!)
    expect(roundTripped).toContain('aura.token={{var.aura_token}}')
    expect(roundTripped).toContain('{{var.aura_fwuid}}')
    expect(roundTripped).toContain('{{var.account_id}}')
    expect(roundTripped).not.toContain('%7B%7Bvar.aura_token%7D%7D')
  })

  it('preserves unquoted {{...}} template tokens inside JSON numbers/booleans and rejects duplicate message keys', () => {
    const unquotedBody = 'message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FListController%2FACTION%24getItems%22%2C%22params%22%3A%7B%22pageSize%22%3A{{param.page_size}}%7D%7D%5D%7D&aura.token={{var.aura_token}}'
    const parsed = parseAuraFormEnvelope(unquotedBody)
    expect(parsed).not.toBeNull()
    const serialized = serializeAuraFormEnvelope(parsed!)
    expect(serialized).toContain('%22pageSize%22%3A{{param.page_size}}')
    expect(parseAuraFormEnvelope(`${unquotedBody}&message=%7B%22actions%22%3A%5B%5D%7D`)).toBeNull()
  })

  it('edits sub-action JSON parameters and validates malformed JSON', () => {
    const parsed = parseAuraFormEnvelope(buildSampleAuraBody())!
    const onChangeBody = vi.fn()
    act(() => root.render(<AuraEnvelopeEditor envelope={parsed} onChangeBody={onChangeBody} />))

    const textarea = container.querySelector(
      'textarea[aria-label="Parameters (JSON) for Apex · InteractionController.searchCustomers"]',
    ) as HTMLTextAreaElement
    expect(textarea.value).toContain('"InteractionController"')

    const applyBtn = Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Apply parameters')!
    expect(applyBtn.disabled).toBe(true)

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      setter.call(textarea, '{broken json')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
      textarea.dispatchEvent(new Event('change', { bubbles: true }))
    })
    act(() => applyBtn.click())
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Parameters must be a valid JSON object.')
    expect(onChangeBody).not.toHaveBeenCalled()

    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      setter.call(textarea, JSON.stringify({
        namespace: '',
        classname: 'InteractionController',
        method: 'searchCustomers',
        params: { accountId: '{{var.account_id}}', query: 'GlobalCorp' },
      }, null, 2))
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
      textarea.dispatchEvent(new Event('change', { bubbles: true }))
    })
    act(() => applyBtn.click())
    expect(onChangeBody).toHaveBeenCalledTimes(1)
    const updatedEnvelope = parseAuraFormEnvelope(onChangeBody.mock.calls[0][0] as string)!
    expect((updatedEnvelope.actions[0].params?.params as Record<string, unknown>).query).toBe('GlobalCorp')
  })

  it('parses XSSI-prefixed Aura responses and highlights action-level ERROR states in HTTP 200 payloads', () => {
    const rawResponse = `while(1);\n${JSON.stringify({
      actions: [
        { id: '128;a', state: 'SUCCESS', returnValue: { totalCount: 3 } },
        { id: '129;a', state: 'ERROR', error: [{ message: 'Invalid CSRF token for action' }] },
      ],
    })}/*ERROR*/`
    const parsed = parseAuraResponseEnvelope(rawResponse)
    expect(parsed).toEqual({
      hasActionError: true,
      actions: [
        { id: '128;a', state: 'SUCCESS', returnValuePreview: '{"totalCount":3}' },
        { id: '129;a', state: 'ERROR', errorSummary: 'Invalid CSRF token for action' },
      ],
    })

    act(() => root.render(
      <div>
        <AuraEnvelopePreview body={buildSampleAuraBody()} />
        <AuraResponsePreview body={rawResponse} />
      </div>,
    ))
    expect(container.textContent).toContain('Decoded Aura envelope (2 actions)')
    expect(container.textContent).toContain('Aura action error')
    expect(container.textContent).toContain('Invalid CSRF token for action')
  })
})
