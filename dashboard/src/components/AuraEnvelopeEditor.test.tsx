// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  AuraEnvelopeEditor,
  AuraEnvelopePreview,
  AuraRequestBodyTabs,
  AuraResponsePreview,
} from './AuraEnvelopeEditor'
import { AuraEnvelopeDraftContext, useAuraEnvelopeDrafts } from './AuraEnvelopeEditorDrafts'
import type { ScriptDefinition } from '../types/scripts'
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
    expect(formatAuraActionTitle(parsed!.actions[0], 0)).toBe('InteractionController.searchCustomers')
    expect(formatAuraActionTitle(parsed!.actions[1], 1)).toBe('RecordGvpController.getRecord')
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

  it('titles sub-actions as the importer names steps (aura.ActionDescriptor.ShortTitle)', () => {
    const title = (descriptor: string, params?: Record<string, unknown>) => formatAuraActionTitle({ descriptor, params }, 0)
    expect(title('aura://ApexActionController/ACTION$execute', { namespace: 'ns', classname: 'Ctrl', method: 'run' })).toBe('ns.Ctrl.run')
    expect(title('apex://BWC_InteractionController/ACTION$getVisibility')).toBe('BWC_InteractionController.getVisibility')
    expect(title('apex://ns.Ctrl/ACTION$m')).toBe('ns.Ctrl.m')
    expect(title('aura://RecordUiController/ACTION$getRecordWithFields')).toBe('RecordUiController.getRecordWithFields')
    // Apex parameters on another controller do not make it an Apex call.
    expect(title('aura://HostConfigController/ACTION$getConfigData', { classname: 'X', method: 'y' })).toBe('HostConfigController.getConfigData')
    expect(title('aura://ApexActionController/ACTION$execute')).toBe('ApexActionController.execute')
    expect(formatAuraActionTitle({}, 2)).toBe('Action 3')
  })

  it('keeps recorded fields byte-for-byte and encoded {{...}} literal text encoded when another field changes', () => {
    const message = '%7B%22actions%22%3A%5B%7B%22id%22%3A%221%22%2C%22descriptor%22%3A%22aura%3A%2F%2FEmailController%2FACTION%24save%22%2C%22params%22%3A%7B%22htmlValue%22%3A%22Hi%20%7B%7B%7BRecipient.FirstName%7D%7D%7D%22%7D%7D%5D%7D'
    const body = `message=${message}&aura.pageURI=%2Fs%2Fhome&aura.token=old`
    const parsed = parseAuraFormEnvelope(body)!
    expect((parsed.actions[0].params as Record<string, unknown>).htmlValue).toBe('Hi {{{Recipient.FirstName}}}')
    const tokenEdited = serializeAuraFormEnvelope({ ...parsed, token: 'new' })
    expect(tokenEdited).toBe(`message=${message}&aura.pageURI=%2Fs%2Fhome&aura.token=new`)

    // Editing the message re-encodes it, and the literal braces stay encoded.
    const actions = [{ ...parsed.actions[0], params: { htmlValue: 'Hi {{{Recipient.FirstName}}}', subject: '{{var.subject}}' } }]
    const messageEdited = serializeAuraFormEnvelope({ ...parsed, actions })
    expect(messageEdited).toContain('%7B%7B%7BRecipient.FirstName%7D%7D%7D')
    expect(messageEdited).not.toContain('{{Recipient.FirstName}}')
    expect(messageEdited).toContain('{{var.subject}}')
  })

  it('keeps a recorded literal {{...}} encoded where it sits when an author adds the same live template earlier', () => {
    const message = JSON.stringify({ actions: [
      { id: '1', descriptor: 'aura://A/ACTION$one', params: { a: 1 } },
      { id: '2', descriptor: 'aura://B/ACTION$two', params: { q: 'Hello {{name}}' } },
    ] })
    const body = `message=${encodeURIComponent(message)}&aura.token=t`
    const parsed = parseAuraFormEnvelope(body)!
    expect(parsed.literalTemplates?.get('message')).toEqual([{ token: '{{name}}', path: ['actions', 1, 'params', 'q'] }])
    const actions = parsed.actions.map((action, index) => index === 0 ? { ...action, params: { a: 1, label: '{{name}}' } } : action)
    const serialized = serializeAuraFormEnvelope({ ...parsed, actions })
    // The author's template is live (raw) and the recorded literal stays encoded.
    expect(serialized).toContain('%22label%22%3A%22{{name}}%22')
    expect(serialized).toContain('Hello+%7B%7Bname%7D%7D')
    const reparsed = parseAuraFormEnvelope(serialized)!
    expect(reparsed.literalTemplates?.get('message')).toEqual([{ token: '{{name}}', path: ['actions', 1, 'params', 'q'] }])
    expect((reparsed.actions[0].params as Record<string, unknown>).label).toBe('{{name}}')
  })

  it('keeps a recorded literal {{...}} encoded when an edit moves the string that holds it', () => {
    const message = JSON.stringify({ actions: [
      { id: '1', descriptor: 'aura://A/ACTION$one', params: { items: ['{{name}}'] } },
    ] })
    const body = `message=${encodeURIComponent(message)}&aura.token=t`
    const parsed = parseAuraFormEnvelope(body)!
    expect(parsed.literalTemplates?.get('message')).toEqual([{ token: '{{name}}', path: ['actions', 0, 'params', 'items', 0] }])
    // An element inserted before it shifts the literal to items[1].
    const actions = [{ ...parsed.actions[0], params: { items: ['new', '{{name}}'] } }]
    const serialized = serializeAuraFormEnvelope({ ...parsed, actions })
    expect(serialized).toContain('%7B%7Bname%7D%7D')
    expect(serialized).not.toContain('{{name}}')
  })

  it('edits sub-action JSON parameters and validates malformed JSON', () => {
    const parsed = parseAuraFormEnvelope(buildSampleAuraBody())!
    const onChangeBody = vi.fn()
    act(() => root.render(<AuraEnvelopeEditor envelope={parsed} onChangeBody={onChangeBody} />))

    const textarea = container.querySelector(
      'textarea[aria-label="Parameters (JSON) for InteractionController.searchCustomers"]',
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

  it('keeps unapplied Parameters and Context JSON as pending edits the script save resolves, across remounts', () => {
    const body = buildSampleAuraBody()
    const initialScript = { name: 'aura', version: '1', protocol: 'http', config: {}, actions: [{ name: 'Aura call', type: 'request', method: 'POST', target: '/aura', body }] } as unknown as ScriptDefinition
    let registry!: ReturnType<typeof useAuraEnvelopeDrafts>
    let setScript!: (script: ScriptDefinition) => void
    function Harness({ show }: { show: boolean }) {
      const [script, setLocalScript] = useState(initialScript)
      setScript = setLocalScript
      registry = useAuraEnvelopeDrafts(script, () => {})
      const action = script.actions[0]
      const envelope = parseAuraFormEnvelope(action.body)!
      return show ? (
        <AuraEnvelopeDraftContext.Provider value={registry.forAction('action:0', action)}>
          <AuraEnvelopeEditor envelope={envelope} onChangeBody={next => setLocalScript(prev => ({ ...prev, actions: [{ ...prev.actions[0], body: next }] }))} />
        </AuraEnvelopeDraftContext.Provider>
      ) : null
    }
    const type = (selector: string, text: string) => act(() => {
      const textarea = container.querySelector(selector) as HTMLTextAreaElement
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, text)
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => root.render(<Harness show />))
    const recordParams = 'textarea[aria-label="Parameters (JSON) for RecordGvpController.getRecord"]'
    type(recordParams, '{"recordId":"001NEW"}')
    type('textarea[aria-label="Aura context (JSON)"]', '{"mode":"PROD","fwuid":"{{var.aura_fwuid}}","app":"one:one"}')
    // Nothing was applied, yet the edits are pending for save.
    expect(registry.count).toBe(1)
    expect(registry.problem).toBe('')
    // Leaving the step (unmount) and coming back keeps them.
    act(() => root.render(<Harness show={false} />))
    act(() => root.render(<Harness show />))
    expect((container.querySelector(recordParams) as HTMLTextAreaElement).value).toBe('{"recordId":"001NEW"}')
    const saved = parseAuraFormEnvelope(registry.resolve(initialScript).actions[0].body)!
    expect(saved.actions[1].params).toEqual({ recordId: '001NEW' })
    expect(saved.actions[0].params).toEqual(parseAuraFormEnvelope(body)!.actions[0].params)
    expect(saved.contextObject?.app).toBe('one:one')

    // Invalid JSON blocks the save instead of saving the old body.
    type(recordParams, '{broken')
    expect(registry.problem).toContain('RecordGvpController.getRecord parameters: Parameters must be a valid JSON object.')
    expect(() => registry.resolve(initialScript)).toThrow(/Parameters must be a valid JSON object/)

    // A body changed under the drafts never receives them.
    type(recordParams, '{"recordId":"001NEW"}')
    act(() => setScript({ ...initialScript, actions: [{ ...initialScript.actions[0], body: body.replace('aura.token={{var.aura_token}}', 'aura.token=other') }] }))
    expect(registry.problem).toContain('The request body changed')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('no longer apply')
    act(() => Array.from(container.querySelectorAll('button')).find(btn => btn.textContent?.includes('Discard Aura edits'))!.click())
    expect(registry.count).toBe(0)
    expect(registry.problem).toBe('')
  })

  it('keeps pending drafts on the body a token edit or Apply writes', () => {
    const body = buildSampleAuraBody()
    const initialScript = { name: 'aura', version: '1', protocol: 'http', config: {}, actions: [{ name: 'Aura call', type: 'request', method: 'POST', target: '/aura', body }] } as unknown as ScriptDefinition
    let registry!: ReturnType<typeof useAuraEnvelopeDrafts>
    let current = initialScript
    function Harness() {
      const [script, setLocalScript] = useState(initialScript)
      current = script
      registry = useAuraEnvelopeDrafts(script, () => {})
      const action = script.actions[0]
      return (
        <AuraEnvelopeDraftContext.Provider value={registry.forAction('action:0', action)}>
          <AuraEnvelopeEditor envelope={parseAuraFormEnvelope(action.body)!} onChangeBody={next => setLocalScript(prev => ({ ...prev, actions: [{ ...prev.actions[0], body: next }] }))} />
        </AuraEnvelopeDraftContext.Provider>
      )
    }
    act(() => root.render(<Harness />))
    const textarea = container.querySelector('textarea[aria-label="Parameters (JSON) for RecordGvpController.getRecord"]') as HTMLTextAreaElement
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(textarea, '{"recordId":"001NEW"}')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const token = container.querySelector('input[aria-label="Aura token"]') as HTMLInputElement
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(token, '{{var.other_token}}')
      token.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(registry.problem).toBe('')
    const saved = parseAuraFormEnvelope(registry.resolve(current).actions[0].body)!
    expect(saved.token).toBe('{{var.other_token}}')
    expect(saved.actions[1].params).toEqual({ recordId: '001NEW' })
    act(() => Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Apply parameters' && !btn.disabled)!.click())
    expect(registry.count).toBe(0)
    expect(parseAuraFormEnvelope(current.actions[0].body)!.actions[1].params).toEqual({ recordId: '001NEW' })
  })

  it('keeps another sub-action\'s numbers as recorded when one sub-action\'s parameters are applied', () => {
    const message = '{"actions":[{"id":"1;a","descriptor":"aura://A/ACTION$one","params":{"q":"old"}},{"id":"2;a","descriptor":"aura://B/ACTION$two","params":{"recordNum":1234567890123456789,"ratio":1.0,"scale":1e3,"plain":42,"neg":-0}}]}'
    const parsed = parseAuraFormEnvelope(`message=${encodeURIComponent(message)}&aura.token=t`)!
    expect(parsed).not.toBeNull()
    const onChangeBody = vi.fn()
    act(() => root.render(<AuraEnvelopeEditor envelope={parsed} onChangeBody={onChangeBody} />))
    // The untouched action shows its numbers as recorded.
    const second = container.querySelector('textarea[aria-label="Parameters (JSON) for B.two"]') as HTMLTextAreaElement
    expect(second.value).toContain('"recordNum": 1234567890123456789')
    expect(second.value).toContain('"ratio": 1.0')
    const first = container.querySelector('textarea[aria-label="Parameters (JSON) for A.one"]') as HTMLTextAreaElement
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
      setter.call(first, '{"q":"new","limit":9007199254740993}')
      first.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Apply parameters' && !btn.disabled)!.click())
    expect(onChangeBody).toHaveBeenCalledTimes(1)
    const sent = decodeURIComponent((onChangeBody.mock.calls[0][0] as string).split('&')[0].slice('message='.length))
    expect(sent).toContain('"q":"new"')
    // The edited action's own big integer is kept as typed.
    expect(sent).toContain('"limit":9007199254740993')
    expect(sent).toContain('"params":{"recordNum":1234567890123456789,"ratio":1.0,"scale":1e3,"plain":42,"neg":-0}')
  })

  it('shows the whole decoded aura.context, loaded included, and applies edits to it', () => {
    const context = JSON.stringify({ mode: 'PROD', fwuid: '{{var.aura_fwuid}}', app: 'one:one', loaded: { 'APPLICATION@markup://one:one': '{{var.corr_salesforce_aura_loaded}}' } })
    const body = `message=${encodeURIComponent('{"actions":[{"id":"1;a","descriptor":"aura://A/ACTION$one","params":{}}]}')}&aura.context=${encodeFormValuePreservingTemplates(context)}&aura.token=t`
    const parsed = parseAuraFormEnvelope(body)!
    const onChangeBody = vi.fn()
    act(() => root.render(<AuraEnvelopeEditor envelope={parsed} onChangeBody={onChangeBody} />))
    const textarea = container.querySelector('textarea[aria-label="Aura context (JSON)"]') as HTMLTextAreaElement
    expect(textarea.value).toContain('"loaded"')
    expect(textarea.value).toContain('{{var.corr_salesforce_aura_loaded}}')
    const apply = () => Array.from(container.querySelectorAll('button')).find(btn => btn.textContent === 'Apply context')!
    expect(apply().disabled).toBe(true)
    const fwuidInput = container.querySelector('input[aria-label="Framework UID (fwuid)"]') as HTMLInputElement
    expect(fwuidInput.disabled).toBe(false)
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!
    act(() => { setter.call(textarea, '{broken'); textarea.dispatchEvent(new Event('input', { bubbles: true })) })
    // An unapplied context draft carries its own fwuid, so the field waits.
    expect(fwuidInput.disabled).toBe(true)
    act(() => apply().click())
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Aura context must be a valid JSON object.')
    expect(onChangeBody).not.toHaveBeenCalled()
    act(() => {
      setter.call(textarea, JSON.stringify({ mode: 'PROD', fwuid: '{{var.aura_fwuid}}', app: 'one:one', loaded: { 'APPLICATION@markup://one:one': '{{var.aura_loaded_v2}}' } }, null, 2))
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => apply().click())
    expect(onChangeBody).toHaveBeenCalledTimes(1)
    const updated = parseAuraFormEnvelope(onChangeBody.mock.calls[0][0] as string)!
    expect(updated.contextObject?.loaded).toEqual({ 'APPLICATION@markup://one:one': '{{var.aura_loaded_v2}}' })
    expect(updated.contextObject?.fwuid).toBe('{{var.aura_fwuid}}')
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

  it('shows a sent /aura body in Debug as Aura envelope and Raw form body tabs, read-only', () => {
    act(() => root.render(<AuraRequestBodyTabs body={buildSampleAuraBody()}><pre data-testid="raw">raw body</pre></AuraRequestBodyTabs>))
    const tab = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('.aura-envelope-mode-tabs button')).find(button => button.textContent?.trim() === label)!
    expect(tab('Aura envelope').getAttribute('aria-pressed')).toBe('true')
    const region = container.querySelector('[aria-label="Decoded Aura request envelope"]')!
    expect(region.querySelectorAll('pre[aria-label^="Parameters (JSON) for"]')).toHaveLength(2)
    expect(region.textContent).toContain('{{var.account_id}}')
    expect(container.querySelector('textarea, input')).toBeNull()
    expect(container.querySelector('[data-testid="raw"]')).toBeNull()
    act(() => tab('Raw form body').click())
    expect(tab('Raw form body').getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('[data-testid="raw"]')).not.toBeNull()
    expect(container.querySelector('[aria-label="Decoded Aura request envelope"]')).toBeNull()

    // A body that is not an Aura envelope shows only the raw view.
    act(() => root.render(<AuraRequestBodyTabs body={'{"a":1}'}><pre data-testid="raw">raw body</pre></AuraRequestBodyTabs>))
    expect(container.querySelector('.aura-envelope-mode-tabs')).toBeNull()
    expect(container.querySelector('[data-testid="raw"]')).not.toBeNull()
  })
})
