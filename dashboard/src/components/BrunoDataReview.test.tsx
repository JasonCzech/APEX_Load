// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import BrunoDataReview from './BrunoDataReview'
import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'
import type { SharedDataPool } from '../types/dataPools'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const candidates = [{ candidate_id: 'a', script: { name: 'First' }, missing_variables: ['env', '$isoTimestamp'] }, { candidate_id: 'b', script: { name: 'Second' }, missing_variables: ['env'] }] as BrunoNativeScriptCandidate[]
const shared = { candidates: [{ id: 'env-id', name: 'env', source_variable: 'env', kind: 'source_variable', occurrence_count: 2 }, { id: 'optional', name: 'trace', kind: 'repeated_literal', script_names: ['First'], occurrence_count: 2 }], summary: {} } as SharedValues

it('supplies values by protected candidate identity, preserves edits across filtering, and links unsupported expressions', () => {
  const inspect = vi.fn()
  const changed = vi.fn()
  function Harness() {
    const [values, setValues] = useState<Record<string, string>>({})
    return <BrunoDataReview candidates={candidates} shared={shared} values={values} onChange={value => { changed(value); setValues(value) }} onInspect={inspect} onCredentials={() => {}} disabled={false} />
  }
  act(() => root.render(<Harness />))
  expect(container.textContent).toContain('This source expression cannot be supplied')
  expect(container.querySelectorAll('.bruno-input-list input')).toHaveLength(1)
  const input = container.querySelector<HTMLInputElement>('.bruno-input-list input')!
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'qa')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(changed).toHaveBeenLastCalledWith({ 'env-id': 'qa' })
  act(() => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
  expect(container.querySelectorAll('.bruno-input-list input')).toHaveLength(0)
  expect(container.textContent).toContain('$isoTimestamp')
  act(() => container.querySelector<HTMLButtonElement>('.bruno-input-list button')!.click())
  expect(inspect).toHaveBeenCalledWith('a')
  act(() => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
  expect(container.querySelector<HTMLInputElement>('.bruno-input-list input')!.value).toBe('qa')
})

it('limits required and optional rows to the selected APIs and disables edits during expiry or save', () => {
  act(() => root.render(<BrunoDataReview candidates={[candidates[1]]} shared={shared} values={{}} onChange={() => {}} onInspect={() => {}} onCredentials={() => {}} disabled />))
  expect(container.textContent).not.toContain('$isoTimestamp')
  expect(container.textContent).toContain('Optional substitutions (0)')
  expect(container.querySelector<HTMLInputElement>('.bruno-input-list input')!.disabled).toBe(true)
})

const pools = [{ id: 'pool-1', name: 'Subscribers', columns: ['number', 'ban'], created_at: '', updated_at: '' }] as SharedDataPool[]

it('reads an input from a data pool column or generates it, and says when a re-import kept the last choice', async () => {
  const loadDataPools = vi.fn(async () => pools)
  const bindingsChanged = vi.fn()
  const envCandidate = [candidates[1]]
  function Harness() {
    const [values, setValues] = useState<Record<string, string>>({ 'env-id': 'qa' })
    const [bindings, setBindings] = useState<Record<string, import('./brunoReviewState').BrunoInputBinding>>({})
    return <BrunoDataReview candidates={envCandidate} shared={shared} values={values} onChange={setValues}
      bindings={bindings} onBindingsChange={next => { bindingsChanged(next); setBindings(next) }}
      previous={{ 'env-id': { mode: 'value', value: 'qa' } }} loadDataPools={loadDataPools}
      onInspect={() => {}} onCredentials={() => {}} disabled={false} />
  }
  await act(async () => { root.render(<Harness />); await Promise.resolve() })
  expect(loadDataPools).toHaveBeenCalled()
  expect(container.querySelector('.bruno-input-previous')?.textContent).toBe('As the last import of these requests supplied it.')

  const select = (label: string) => Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(label))!.querySelector('select')!
  const choose = (element: HTMLSelectElement, value: string) => act(() => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
  choose(select('Supply env'), 'data')
  choose(select('Data pool for env'), 'pool-1')
  expect(bindingsChanged).toHaveBeenLastCalledWith({ 'env-id': { mode: 'data', alias: 'subscribers', column: 'number' } })
  choose(select('Column for env'), 'ban')
  expect(bindingsChanged).toHaveBeenLastCalledWith({ 'env-id': { mode: 'data', alias: 'subscribers', column: 'ban' } })
  expect(container.textContent).toContain('{{data.subscribers.ban}}')
  // The value for every user gives way to the binding.
  expect(container.querySelector('.bruno-input-previous')).toBeNull()

  choose(select('Supply env'), 'generated')
  choose(select('Generate for env'), 'digits')
  expect(bindingsChanged).toHaveBeenLastCalledWith({ 'env-id': { mode: 'generated', generator: 'digits', digits: 10 } })
  choose(select('Supply env'), 'value')
  expect(bindingsChanged).toHaveBeenLastCalledWith({})
})

it('fills inputs from a chosen environment as one value for every user', () => {
  const changed = vi.fn()
  const bindingsChanged = vi.fn()
  const environments: { name: string; path: string; values?: Record<string, string> }[] = [
    { name: 'dev', path: 'CTA/environments/dev.yml', values: { env: 'dev.example.test' } },
    { name: 'empty', path: 'CTA/environments/empty.yml', values: { unrelated: 'x' } },
  ]
  act(() => root.render(<BrunoDataReview candidates={[candidates[1]]} shared={shared} values={{}} onChange={changed}
    bindings={{ 'env-id': { mode: 'generated', generator: 'uuid' } }} onBindingsChange={bindingsChanged} environments={environments}
    onInspect={() => {}} onCredentials={() => {}} disabled={false} />))
  const fill = container.querySelector('.bruno-environment-fill')!
  expect(fill.textContent).toContain('Fill these inputs again')
  const select = fill.querySelector('select')!
  // Only an environment that defines one of the inputs is offered.
  expect(Array.from(select.options).map(option => option.textContent)).toEqual(['Choose an environment', 'dev (1 input)'])
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'CTA/environments/dev.yml')
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  expect(changed).toHaveBeenLastCalledWith({ 'env-id': 'dev.example.test' })
  expect(bindingsChanged).toHaveBeenLastCalledWith({})
  expect(fill.textContent).toContain('Filled 1 input with one value for every user.')
})

it('asks for an environment while the inputs it defines are unset', () => {
  act(() => root.render(<BrunoDataReview candidates={[candidates[1]]} shared={shared} values={{}} onChange={() => {}}
    environments={[{ name: 'dev', path: 'dev.yml', values: { env: 'dev.example.test' } }]}
    onInspect={() => {}} onCredentials={() => {}} disabled={false} />))
  expect(container.querySelector('.bruno-environment-fill')?.textContent).toContain('The collection\'s environment defines some of these inputs. Choose one to fill them with its values.')
})
