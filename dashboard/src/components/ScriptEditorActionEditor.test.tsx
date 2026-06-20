// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useState } from 'react'
import type { ScriptActionDefinition } from '../types/scripts'
import { ActionEditor, ControlBranchEditor, ControlFlowFields } from './ScriptEditorActionEditor'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

  const setSelectValue = (select: HTMLSelectElement, value: string) => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
    setter.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  }

  it('renders a condition editor for control_if (works in an HTTP script)', () => {
    act(() => { root.render(<Controlled initial={{ name: 'Branch', type: 'control_if', target: '', control: { condition: { op: 'eq' }, steps: [] } } as unknown as ScriptActionDefinition} />) })
    expect(labelInputIn(container, 'Condition Left')).toBeTruthy()
    expect(labelSelectIn(container, 'Operator')).toBeTruthy()
    // control_if has no loop-mode selector.
    expect(labelSelectIn(container, 'Loop Mode')).toBeFalsy()
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
})

describe('ActionEditor transaction field', () => {
  const labelInput = (labelText: string) =>
    [...container.querySelectorAll('label')]
      .find(l => l.querySelector('span')?.textContent === labelText)
      ?.querySelector('input') as HTMLInputElement | undefined

  const renderEditor = (action: ScriptActionDefinition) => {
    const updates: Array<[number, string, unknown]> = []
    const noop = () => {}
    act(() => {
      root.render(
        <ActionEditor
          action={action}
          index={0}
          parameters={[]}
          onUpdate={(i, f, v) => updates.push([i, f as string, v])}
          onAddExtractor={noop}
          onUpdateExtractor={noop}
          onRemoveExtractor={noop}
          onAddAssertion={noop}
          onUpdateAssertion={noop}
          onRemoveAssertion={noop}
          onAddHeader={noop}
          onUpdateHeader={noop}
          onRemoveHeader={noop}
          onAddMetadata={noop}
          onUpdateMetadata={noop}
          onRemoveMetadata={noop}
          onBeginParameterEdit={noop}
          methodColor={() => '#000'}
          protocol="http"
          isBrowserScript={false}
          isJmsScript={false}
          isSoapScript={false}
          issues={[]}
        />,
      )
    })
    return updates
  }

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

  it('lists a grouped request\'s extractors for an http_sequence action', () => {
    renderEditor({
      name: 'Login Flow',
      type: 'http_sequence',
      params: {
        requests: [
          { name: 'Login', method: 'POST', target: '/login', extractors: [{ name: 'session_id', source: 'body', type: 'json', expression: '$.token' }] },
          { name: 'Profile', method: 'GET', target: '/me' },
        ],
      },
    } as unknown as ScriptActionDefinition)
    const headings = [...container.querySelectorAll('.http-sequence-section h4')].map(h => h.textContent)
    expect(headings).toContain('Extractors')
    const text = container.textContent || ''
    expect(text).toContain('session_id')
    expect(text).toContain('$.token')
    expect(text).toContain('json')
  })
})
