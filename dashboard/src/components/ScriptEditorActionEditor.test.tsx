// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useState } from 'react'
import type { ScriptActionDefinition, ScriptAssertionDefinition } from '../types/scripts'
import { ActionEditor, ControlBranchEditor, ControlFlowFields } from './ScriptEditorActionEditor'
import { ProtocolAssertionsSection } from './scriptActionFields'
import { CODE_LINE_WRAP_STORAGE_KEY } from './codeLineWrapPreference'

vi.mock('./SourceCodeViewer', () => ({
  default: ({ ariaLabel, value }: { ariaLabel?: string; value: string }) => <pre aria-label={ariaLabel}>{value}</pre>,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
})

const steps = (): ScriptActionDefinition[] => ([
  { name: 'first', type: 'GET', target: '/a', params: { keep: 1 } },
  { name: 'second', type: 'POST', target: '/b' },
] as unknown as ScriptActionDefinition[])

const button = (text: string) =>
  [...container.querySelectorAll('button')].find(b => b.textContent === text) as HTMLButtonElement | undefined

const setInputValue = (input: HTMLInputElement, value: string) => {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

const setSelectValue = (select: HTMLSelectElement, value: string) => {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
  setter.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('ControlBranchEditor', () => {
  it('renders one structured row per step', () => {
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={steps()} onChange={() => {}} />) })
    expect(container.querySelectorAll('.control-step-row')).toHaveLength(2)
    expect(container.querySelector('.control-branch-header small')?.textContent).toBe('2 steps')
  })

  it('shows an empty hint and adds a default protocol-agnostic step', () => {
    let last: ScriptActionDefinition[] | null = null
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={[]} onChange={s => { last = s }} />) })
    expect(container.querySelector('.control-step-empty')).not.toBeNull()
    act(() => { button('+ Step')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(last).toHaveLength(1)
    // The default step seeds an empty type so the author picks one matching the protocol.
    expect(last![0].type).toBe('')
  })

  it('adds control_break / control_continue leaf steps', () => {
    let last: ScriptActionDefinition[] | null = null
    act(() => { root.render(<ControlBranchEditor label="Loop steps" steps={[]} onChange={s => { last = s }} />) })
    act(() => { button('+ Break')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(last![0].type).toBe('control_break')
    act(() => { root.render(<ControlBranchEditor label="Loop steps" steps={last!} onChange={s => { last = s }} />) })
    act(() => { button('+ Continue')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(last!.map(s => s.type)).toEqual(['control_break', 'control_continue'])
  })

  it('expands a nested control_loop step inline (recursive editor)', () => {
    const nested: ScriptActionDefinition[] = [
      { name: 'inner', type: 'control_loop', target: '', control: { count: 2, steps: [] } },
    ] as unknown as ScriptActionDefinition[]
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={nested} onChange={() => {}} />) })
    // The nested control step renders its own control-flow editor inline.
    expect(container.querySelector('.control-step-nested')).not.toBeNull()
    const headings = [...container.querySelectorAll('.control-flow-section h4')].map(h => h.textContent)
    expect(headings).toContain('Loop')
  })

  it('edits a step field while preserving its other (unknown) keys', () => {
    let last: ScriptActionDefinition[] | null = null
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={steps()} onChange={s => { last = s }} />) })
    const targetInput = container.querySelector('.control-step-row .control-step-target') as HTMLInputElement
    act(() => { setInputValue(targetInput, '/changed') })
    expect(last![0]).toEqual({ name: 'first', type: 'GET', target: '/changed', params: { keep: 1 } })
  })

  it('removes a step', () => {
    let last: ScriptActionDefinition[] | null = null
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={steps()} onChange={s => { last = s }} />) })
    const removeBtn = container.querySelector('.control-step-row .control-step-actions button.danger') as HTMLButtonElement
    act(() => { removeBtn.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(last!.map(s => s.name)).toEqual(['second'])
  })

  it('exposes a JSON escape hatch that swaps in a textarea', () => {
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={steps()} onChange={() => {}} />) })
    expect(container.querySelector('textarea')).toBeNull()
    act(() => { button('JSON')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea).not.toBeNull()
    expect(JSON.parse(textarea.value)).toHaveLength(2)
  })
})

const labelInputIn = (root: HTMLElement, labelText: string) =>
  [...root.querySelectorAll('label')]
    .find(l => l.querySelector('span')?.textContent === labelText)
    ?.querySelector('input') as HTMLInputElement | undefined

const labelSelectIn = (root: HTMLElement, labelText: string) =>
  [...root.querySelectorAll('label')]
    .find(l => l.querySelector('span')?.textContent === labelText)
    ?.querySelector('select') as HTMLSelectElement | undefined

describe('ControlFlowFields', () => {
  function Controlled({ initial }: { initial: ScriptActionDefinition }) {
    const [action, setAction] = useState(initial)
    return <ControlFlowFields action={action} index={0} onUpdate={(_i, f, v) => setAction(a => ({ ...a, [f]: v }) as ScriptActionDefinition)} />
  }

  it('renders a condition editor for control_if (works in an HTTP script)', () => {
    act(() => { root.render(<Controlled initial={{ name: 'Branch', type: 'control_if', target: '', control: { condition: { op: 'eq' }, steps: [] } } as unknown as ScriptActionDefinition} />) })
    expect(labelInputIn(container, 'Condition Left')).toBeTruthy()
    expect(labelSelectIn(container, 'Operator')).toBeTruthy()
    // control_if has no loop-mode selector.
    expect(labelSelectIn(container, 'Loop Mode')).toBeFalsy()
  })

	it('edits named parallel branches and bounded concurrency', () => {
		act(() => { root.render(<Controlled initial={{
			name: 'Fan out', type: 'control_parallel', target: '',
			control: { concurrency: 4, branches: [{ name: 'one', steps: [] }, { name: 'two', steps: [] }] },
		} as unknown as ScriptActionDefinition} />) })
		expect(container.querySelector('.control-flow-section h4')?.textContent).toBe('Parallel branches')
		expect(container.querySelectorAll('.control-switch-case')).toHaveLength(2)
		const concurrency = labelInputIn(container, 'Concurrency')!
		act(() => { setInputValue(concurrency, '99') })
		expect(concurrency.value).toBe('16')
		act(() => { button('+ Branch')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
		expect(container.querySelectorAll('.control-switch-case')).toHaveLength(3)
	})

  it('offers Count / While / For-each loop modes and reveals foreach inputs', () => {
    act(() => { root.render(<Controlled initial={{ name: 'Loop', type: 'control_loop', target: '', control: { count: 3, steps: [] } } as unknown as ScriptActionDefinition} />) })
    const modeSelect = labelSelectIn(container, 'Loop Mode')!
    expect(modeSelect).toBeTruthy()
    expect([...modeSelect.options].map(o => o.value)).toEqual(['count', 'while', 'foreach'])
    expect(labelInputIn(container, 'Loop Count')).toBeTruthy()

    act(() => { setSelectValue(modeSelect, 'foreach') })
    expect(labelInputIn(container, 'Data Source')).toBeTruthy()
    expect(labelInputIn(container, 'Row Variable')).toBeTruthy()
    expect(labelInputIn(container, 'Loop Count')).toBeFalsy()
  })

	it('authors ordered control_switch cases and a default branch', () => {
		act(() => {
			root.render(<Controlled initial={{
				name: 'Route', type: 'control_switch', target: '',
				control: {
					cases: [{ condition: { expr: 'var.kind == "a"' }, steps: [{ name: 'A', type: 'GET', target: '/a' }] }],
					default: [{ name: 'Fallback', type: 'GET', target: '/fallback' }],
				},
			} as unknown as ScriptActionDefinition} />)
		})
		expect(container.querySelector('.control-flow-section h4')?.textContent).toBe('Switch')
		expect(labelInputIn(container, 'CEL Expression (optional)')?.value).toBe('var.kind == "a"')
		expect(container.textContent).toContain('Case 1 steps')
		expect(container.textContent).toContain('Default steps')
		act(() => { button('+ Case')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
		expect(container.querySelectorAll('.control-switch-case')).toHaveLength(2)
		act(() => { container.querySelector<HTMLButtonElement>('button[aria-label="Remove case 1"]')?.click() })
		expect(container.querySelectorAll('.control-switch-case')).toHaveLength(1)
	})
})

describe('ActionEditor transaction field', () => {
  const labelInput = (labelText: string) =>
    [...container.querySelectorAll('label')]
      .find(l => l.querySelector('span')?.textContent === labelText)
      ?.querySelector('input') as HTMLInputElement | undefined

  const renderEditor = (
    action: ScriptActionDefinition,
    options: { protocol?: string; isJmsScript?: boolean; inspector?: boolean; assertionUpdate?: (ai: number, ai2: number, field: keyof ScriptAssertionDefinition, value: unknown) => void } = {},
  ) => {
    const updates: Array<[number, string, unknown]> = []
    const noop = () => {}
    act(() => {
      root.render(
        <ActionEditor
          inspector={options.inspector}
          action={action}
          index={0}
          parameters={[]}
          onUpdate={(i, f, v) => updates.push([i, f as string, v])}
          onAddExtractor={noop}
          onUpdateExtractor={noop}
          onRemoveExtractor={noop}
          onAddAssertion={noop}
          onUpdateAssertion={options.assertionUpdate || noop}
          onRemoveAssertion={noop}
          onAddHeader={noop}
          onUpdateHeader={noop}
          onRemoveHeader={noop}
          onAddMetadata={noop}
          onUpdateMetadata={noop}
          onRemoveMetadata={noop}
          onBeginParameterEdit={noop}
          methodColor={() => '#000'}
          protocol={options.protocol || 'http'}
          isBrowserScript={false}
          isJmsScript={options.isJmsScript || false}
          isSoapScript={false}
          issues={[]}
        />,
      )
    })
    return updates
  }

  it.each(['source_review', 'review_required'])('uses semantic theme colors for the %s badge', (type) => {
    renderEditor({ name: 'Review imported source', type } as ScriptActionDefinition)
    const badge = container.querySelector<HTMLElement>('.action-badge')!
    expect(badge.textContent).toBe(type)
    expect(badge.classList.contains('action-badge-review')).toBe(true)
    expect(badge.getAttribute('style')).toBeNull()
  })

  it('colours request badges with the method colour as text, never as a fill under white text', () => {
    renderEditor({ name: 'Login', type: 'POST', target: '/login' } as ScriptActionDefinition)
    const badge = container.querySelector<HTMLElement>('.action-badge')!
    expect(badge.classList.contains('action-badge-review')).toBe(false)
    expect(badge.style.color).toBe('rgb(0, 0, 0)')
    // A text token used as a fill failed contrast in the dark themes.
    expect(badge.style.background).toBe('')
  })

  it('shows the imported call for a transaction marker instead of request fields', () => {
    renderEditor({ name: 'Start 01_Login', type: 'source_transaction', params: { function: 'lr_start_transaction', args: ['"01_Login"'] } } as unknown as ScriptActionDefinition, { inspector: true })
    expect(labelInput('Transaction')).toBeUndefined()
    expect(labelInput('Target (path)')).toBeUndefined()
    expect(container.textContent).not.toContain('Headers')
    expect(container.querySelector('.form-hint code')?.textContent).toBe('lr_start_transaction("01_Login")')
    expect([...container.querySelectorAll('.inspector-section-tabs button')].map(button => button.textContent)[0]).toBe('Source call')
  })

  it('keeps the Transaction field on other imported calls such as rendezvous', () => {
    renderEditor({ name: 'Meet', type: 'rendezvous', params: { function: 'lr_rendezvous', key: 'checkout' } } as unknown as ScriptActionDefinition)
    expect(labelInput('Transaction')).toBeTruthy()
    expect(labelInput('Target (path)')).toBeUndefined()
    expect(container.querySelector('.form-hint')?.textContent).toContain('VUsers wait at rendezvous point checkout')
  })

  it('shows an empty Transaction field and tags the action on input', () => {
    const updates = renderEditor({ name: 'Login', type: 'POST', target: '/login' } as unknown as ScriptActionDefinition)
    const input = labelInput('Transaction')!
    expect(input).toBeTruthy()
    expect(input.value).toBe('')
    act(() => { setInputValue(input, 'Checkout') })
    expect(updates[updates.length - 1]).toEqual([0, 'metadata', { source_transaction: 'Checkout' }])
  })

  it('prefills the Transaction field from the existing tag', () => {
    renderEditor({ name: 'Login', type: 'POST', target: '/login', metadata: { source_transaction: 'Auth', keep: 1 } } as unknown as ScriptActionDefinition)
    expect(labelInput('Transaction')!.value).toBe('Auth')
  })

  it('edits imported Aura success assertions without value fields and offers them only for HTTP', () => {
    const action: ScriptActionDefinition = { name: 'Aura batch', type: 'request', method: 'POST', target: '/aura', assertions: [{ type: 'aura_actions_success' }] }
    const update = vi.fn()
    for (const protocol of ['http', 'http/1.1', 'http/2']) {
      renderEditor(action, { protocol, assertionUpdate: update })
      const row = container.querySelector('.assertion-row')!
      const select = row.querySelector('select') as HTMLSelectElement
      expect(select.value).toBe('aura_actions_success')
      expect(select.selectedOptions[0].textContent).toBe('Aura actions succeed')
      expect(row.querySelectorAll('input')).toHaveLength(0)
      act(() => { setSelectValue(select, 'status_code') })
      expect(update).toHaveBeenCalledWith(0, 0, 'type', 'status_code')
    }
    for (const protocol of ['browser', 'grpc', 'soap', 'database', 'network', 'websocket', 'java_http', 'kafka']) {
      renderEditor({ ...action, assertions: [{ type: 'body_contains', value: 'ok' }] }, { protocol })
      const select = container.querySelector('.assertion-row select') as HTMLSelectElement
      expect([...select.options].map(option => option.value)).not.toContain('aura_actions_success')
    }
  })

  it('clears incompatible fields when switching an authored check to Aura success', () => {
    let current: ScriptActionDefinition
    function AuthoredCheck() {
      const [action, setAction] = useState<ScriptActionDefinition>({ name: 'Aura request', type: 'request', target: '/aura', assertions: [{ type: 'status_code', expected: 200, value: 'recorded-id', min: 1, max: 5000, legacy_html_check: true }] })
      current = action
      return <ProtocolAssertionsSection action={action} index={0} protocol="http" onAddAssertion={() => {}} onRemoveAssertion={() => {}} onUpdateAssertion={(_index, assertionIndex, field, value) => setAction(previous => ({ ...previous, assertions: previous.assertions!.map((assertion, index) => index === assertionIndex ? { ...assertion, [field]: value } : assertion) }))} />
    }
    act(() => { root.render(<AuthoredCheck />) })
    act(() => { setSelectValue(container.querySelector('.assertion-row select') as HTMLSelectElement, 'aura_actions_success') })
    expect(JSON.parse(JSON.stringify(current!.assertions))).toEqual([{ type: 'aura_actions_success' }])
    expect(container.querySelectorAll('.assertion-row input')).toHaveLength(0)
  })

  it('offers exact HTTP header and regex-capture assertion types', () => {
    renderEditor({
      name: 'Imported request',
      type: 'POST',
      target: '/service',
      assertions: [{ type: 'header_contains', value: 'Content-Type', expected: 'text/xml' }],
    } as unknown as ScriptActionDefinition)

    const assertionType = container.querySelector('.assertion-row select') as HTMLSelectElement
    expect([...assertionType.options].map(option => option.value)).toContain('header_contains')
    expect([...assertionType.options].map(option => option.value)).toContain('body_regex_capture_equals')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Header name"]')?.value).toBe('Content-Type')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Expected value"]')?.value).toBe('text/xml')
  })

  it('preserves and edits authoritative ordered header occurrences', () => {
    const updates = renderEditor({
      name: 'Imported request',
      type: 'request',
      method: 'POST',
      target: '/service',
      headers: { 'x-trace': 'second', 'X-Map-Only': 'preserved' },
      header_list: [
        { name: 'X-Trace', value: 'first' },
        { name: 'x-trace', value: 'second' },
      ],
    } as unknown as ScriptActionDefinition)

    const names = [...container.querySelectorAll<HTMLInputElement>('.kv-key')].map(input => input.value)
    const values = [...container.querySelectorAll<HTMLInputElement>('.kv-val')].map(input => input.value)
    expect(names).toEqual(['X-Trace', 'x-trace', 'X-Map-Only'])
    expect(values).toEqual(['first', 'second', 'preserved'])

    act(() => { setInputValue(container.querySelectorAll<HTMLInputElement>('.kv-val')[1], 'updated') })
    expect(updates.slice(-2)).toEqual([
      [0, 'header_list', [
        { name: 'X-Trace', value: 'first' },
        { name: 'x-trace', value: 'updated' },
      ]],
      [0, 'headers', { 'X-Map-Only': 'preserved', 'x-trace': 'updated' }],
    ])
    expect(container.querySelector<HTMLButtonElement>('button[aria-label="Remove header 1"]')).toBeTruthy()
  })

  it('promotes a compatibility-only header renamed to an ordered name instead of hiding it', () => {
    const updates = renderEditor({
      name: 'Imported request',
      type: 'request',
      method: 'GET',
      target: '/service',
      headers: { 'X-Trace': 'first', 'X-Map-Only': 'extra' },
      header_list: [{ name: 'X-Trace', value: 'first' }],
    } as unknown as ScriptActionDefinition)

    const compatibilityName = container.querySelector<HTMLInputElement>('input[aria-label="Header X-Map-Only name"]')!
    act(() => { setInputValue(compatibilityName, 'x-trace') })
    expect(updates.slice(-2)).toEqual([
      [0, 'header_list', [
        { name: 'X-Trace', value: 'first' },
        { name: 'x-trace', value: 'extra' },
      ]],
      [0, 'headers', { 'x-trace': 'extra' }],
    ])
  })

  it('surfaces base64 request-body semantics and allows an explicit switch to plain text', () => {
    const updates = renderEditor({
      name: 'Binary request',
      type: 'request',
      method: 'POST',
      target: '/upload',
      body: 'SGVsbG8=',
      body_encoding: ' Base64 ',
    } as unknown as ScriptActionDefinition)

    const representation = labelSelectIn(container, 'Body Representation')!
    expect(representation.value).toBe('base64')
    expect(container.querySelector<HTMLTextAreaElement>('.body-textarea')?.value).toBe('SGVsbG8=')
    expect(container.textContent).toContain('runtime decodes it before sending')

    act(() => { setSelectValue(representation, '') })
    expect(updates[updates.length - 1]).toEqual([0, 'body_encoding', undefined])
  })

  it('allows HTTP request bodies to disable word wrapping and persists the source-view preference', () => {
    renderEditor({
      name: 'Long request',
      type: 'request',
      method: 'POST',
      target: '/policy',
      body: '{"policy":"a very long value"}',
    } as unknown as ScriptActionDefinition)

    const textarea = container.querySelector<HTMLTextAreaElement>('.body-textarea')!
    expect(textarea.wrap).toBe('soft')

    act(() => {
      button('Word Wrap: On')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(textarea.wrap).toBe('off')
    expect(textarea.dataset.wordWrap).toBe('false')
    expect(window.localStorage.getItem(CODE_LINE_WRAP_STORAGE_KEY)).toBe('off')
  })

  it.each([
    ['WebSocket', { name: 'Send', type: 'ws_send', target: '/events', body: '{"event":"ping"}' }, { protocol: 'websocket' }],
    ['gRPC stream', { name: 'Stream', type: 'grpc_bidi_stream', target: 'Events/Stream', params: { messages: ['{"id":1}', '{"id":2}'] } }, { protocol: 'grpc' }],
    ['database', { name: 'Query', type: 'db_query', target: 'select * from events', params: { binds: ['{{param.id}}'] } }, { protocol: 'database' }],
    ['network', { name: 'TCP', type: 'tcp_request', target: 'localhost:9000', body: 'ping' }, { protocol: 'network' }],
    ['JMS', { name: 'Publish', type: 'jms_publish', target: 'events', body: '{"event":"ping"}' }, { protocol: 'jms', isJmsScript: true }],
  ])('exposes and applies word wrapping for %s code fields', (_label, action, options) => {
    renderEditor(action as unknown as ScriptActionDefinition, options)

    const textareas = Array.from(container.querySelectorAll<HTMLTextAreaElement>('.body-textarea'))
    expect(textareas.length).toBeGreaterThan(0)
    expect(textareas.every(textarea => textarea.wrap === 'soft')).toBe(true)
    expect(button('Word Wrap: On')).toBeDefined()

    act(() => {
      button('Word Wrap: On')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(textareas.every(textarea => textarea.wrap === 'off')).toBe(true)
    expect(button('Word Wrap: Off')).toBeDefined()
  })

  it('shows a formatted XML projection without changing the executable body', () => {
    const body = '<env:Envelope xmlns:env="urn:soap"><env:Body><Check><Username>{{secret.USERNAME}}</Username></Check></env:Body></env:Envelope>'
    const updates = renderEditor({
      name: 'SOAP request',
      type: 'request',
      method: 'POST',
      target: '/policy',
      headers: { 'Content-Type': 'application/xml' },
      body,
    } as unknown as ScriptActionDefinition)

    act(() => {
      button('Word Wrap: On')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const preview = container.querySelector<HTMLElement>('[aria-label="Formatted XML body preview"]')
    expect(preview?.textContent).toContain('\n  <env:Body>')
    expect(preview?.textContent).toContain('\n      <Username>{{secret.USERNAME}}</Username>')
    expect(container.textContent).toContain('stored request body and executable DSL payload are unchanged')
    expect(updates).toEqual([])

    const rawEditor = container.querySelector<HTMLDetailsElement>('.body-xml-raw-editor')
    expect(rawEditor?.open).toBe(false)
    expect(rawEditor?.querySelector<HTMLTextAreaElement>('textarea')?.value).toBe(body)
  })

  it('keeps mixed-content XML in the exact raw editor when wrapping is disabled', () => {
    const body = '<p>Hello <b>world</b> again</p>'
    const updates = renderEditor({
      name: 'Mixed XML request',
      type: 'request',
      method: 'POST',
      target: '/message',
      body,
    } as unknown as ScriptActionDefinition)

    act(() => {
      button('Word Wrap: On')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[aria-label="Formatted XML body preview"]')).toBeNull()
    expect(container.querySelector<HTMLTextAreaElement>('.body-textarea')?.value).toBe(body)
    expect(container.querySelector<HTMLTextAreaElement>('.body-textarea')?.wrap).toBe('off')
    expect(updates).toEqual([])
  })

  it('does not hide an imported body on a method that normally has no body editor', () => {
    renderEditor({
      name: 'GET with authored body',
      type: 'request',
      method: 'GET',
      target: '/search',
      body: 'preserve-me',
    } as unknown as ScriptActionDefinition)

    expect(container.querySelector<HTMLTextAreaElement>('.body-textarea')?.value).toBe('preserve-me')
  })

  it('shows durable execution blockers independently of legacy review metadata', () => {
    renderEditor({
      name: 'Missing input',
      type: 'request',
      method: 'GET',
      target: '/orders/{{var.order_id}}',
      metadata: { execution_blocker: 'import_missing_variables' },
    } as unknown as ScriptActionDefinition)

    const pill = [...container.querySelectorAll<HTMLElement>('.action-signal-pill')]
      .find(element => element.textContent === 'blocked')
    expect(pill?.getAttribute('title')).toContain('import_missing_variables')
  })

  it('renders separate RE2 pattern and expected-value fields', () => {
    renderEditor({
      name: 'Imported request',
      type: 'POST',
      target: '/service',
      assertions: [{ type: 'body_regex_capture_equals', value: '<code>([^<]+)</code>', expected: '500' }],
    } as unknown as ScriptActionDefinition)

    expect(container.querySelector<HTMLInputElement>('input[placeholder="RE2 pattern"]')?.value).toBe('<code>([^<]+)</code>')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Expected value"]')?.value).toBe('500')
  })

  it.each([
    ['cookie_clear', 'has no response to extract or check'],
    ['http_sequence', 'Select an individual request'],
  ])('explains unavailable response checks for %s in the inspector', (type, explanation) => {
    renderEditor({ name: 'Control', type } as ScriptActionDefinition, { inspector: true })
    act(() => button('Correlation & checks (0)')!.click())
    const note = [...container.querySelectorAll('.sub-section')].find(element => element.textContent?.includes(explanation))
    expect(note).toBeTruthy()
    expect(note?.closest('[hidden]')).toBeNull()
  })

  it('summarizes an http_sequence action without listing grouped child requests', () => {
    renderEditor({
      name: 'Login Flow',
      type: 'http_sequence',
      params: {
        requests: [
          { name: 'Login', method: 'POST', target: '/login', extractors: [{ name: 'session_id', source: 'body', type: 'json', expression: '$.token' }] },
          {
            name: 'Profile',
            method: 'GET',
            target: '/me',
            headers: { 'x-trace': 'second' },
            header_list: [
              { name: 'X-Trace', value: 'first' },
              { name: 'x-trace', value: 'second' },
            ],
          },
        ],
      },
    } as unknown as ScriptActionDefinition)
    const headings = [...container.querySelectorAll('.http-sequence-section h4')].map(h => h.textContent)
    expect(headings).toEqual(['Sequence Summary'])
    const text = container.textContent || ''
    expect(text).toContain('2 req')
    expect(text).toContain('2 hdr')
    expect(text).toContain('1 ext')
    expect(text).not.toContain('Profile')
    expect(text).not.toContain('/login')
    expect(text).not.toContain('session_id')
  })
})
