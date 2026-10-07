// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import BrunoDataReview from './BrunoDataReview'
import type { BrunoNativeScriptCandidate, SharedValues } from '../types/scriptImports'
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
