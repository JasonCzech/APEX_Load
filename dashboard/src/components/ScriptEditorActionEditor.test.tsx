// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { ScriptActionDefinition } from '../types/scripts'
import { ControlBranchEditor } from './ScriptEditorActionEditor'

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

  it('shows an empty hint and adds a default step', () => {
    let last: ScriptActionDefinition[] | null = null
    act(() => { root.render(<ControlBranchEditor label="Then steps" steps={[]} onChange={s => { last = s }} />) })
    expect(container.querySelector('.control-step-empty')).not.toBeNull()
    act(() => { button('+ Add step')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(last).toHaveLength(1)
    expect(last![0].type).toBe('GET')
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
