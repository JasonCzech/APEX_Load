// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptPicker, { type ScriptOption } from './ScriptPicker'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const scriptApiMock = { listScripts: vi.fn() }
vi.mock('../hooks/api/useScriptApi', () => ({
  useScriptApi: () => scriptApiMock,
}))

const script = (id: string, name: string) => ({ id, name, protocol: 'web' })

let container: HTMLElement
let root: Root

beforeEach(() => {
  vi.useFakeTimers()
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.useRealTimers()
  vi.clearAllMocks()
})

async function settle(ms = 0) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms) })
}

function Harness({ onChange, initialValue = '', scripts }: { onChange?: (id: string) => void; initialValue?: string; scripts?: ScriptOption[] }) {
  const [value, setValue] = useState(initialValue)
  return <ScriptPicker projectId="proj-a" label="Script" value={value} scripts={scripts} onChange={id => { setValue(id); onChange?.(id) }} />
}

const select = () => container.querySelector('select') as HTMLSelectElement
const search = () => container.querySelector('input[type=search]') as HTMLInputElement
const optionLabels = () => Array.from(select().options).map(option => option.textContent)

function choose(id: string) {
  act(() => {
    select().value = id
    select().dispatchEvent(new Event('change', { bubbles: true }))
  })
}

function type(value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(search(), value)
    search().dispatchEvent(new Event('input', { bubbles: true }))
  })
}

describe('ScriptPicker', () => {
  it('searches on the server and keeps the chosen script when a later search drops it', async () => {
    scriptApiMock.listScripts.mockImplementation((_project: string, _folder: undefined, { search: query }: { search: string }) => Promise.resolve(
      query ? { scripts: [script('s2', 'Login')], count: 1 } : { scripts: [script('s1', 'Checkout'), script('s2', 'Login')], count: 2 },
    ))
    const onChange = vi.fn()
    await act(async () => { root.render(<Harness onChange={onChange} />) })
    await settle()
    expect(optionLabels()).toEqual(['Choose a script', 'Checkout · web', 'Login · web'])

    choose('s1')
    expect(onChange).toHaveBeenCalledWith('s1')

    type('log')
    await settle(100)
    expect(scriptApiMock.listScripts).toHaveBeenCalledTimes(1)
    await settle(200)
    expect(scriptApiMock.listScripts).toHaveBeenLastCalledWith('proj-a', undefined, { search: 'log', limit: 50 })
    expect(optionLabels()).toEqual(['Choose a script', 'Checkout · web', 'Login · web'])
    expect(select().value).toBe('s1')
  })

  it('says how many scripts the list leaves out', async () => {
    scriptApiMock.listScripts.mockResolvedValue({ scripts: [script('s1', 'Checkout')], count: 120 })
    await act(async () => { root.render(<Harness />) })
    await settle()
    expect(container.textContent).toContain('Showing 1 of 120 scripts')
    expect(select().getAttribute('aria-describedby')).toBeTruthy()
  })

  it('tells an empty project from a search with no matches, and shows a load failure', async () => {
    scriptApiMock.listScripts.mockResolvedValueOnce({ scripts: [], count: 0 })
    await act(async () => { root.render(<Harness />) })
    await settle()
    expect(optionLabels()).toEqual(['No scripts in this project'])

    scriptApiMock.listScripts.mockResolvedValueOnce({ scripts: [], count: 0 })
    type('zzz')
    await settle(300)
    expect(optionLabels()).toEqual(['No scripts match the search'])

    scriptApiMock.listScripts.mockRejectedValueOnce(new Error('scripts offline'))
    type('zz')
    await settle(300)
    expect(container.querySelector('[role=alert]')?.textContent).toBe('scripts offline')
  })

  it('still names a chosen script after remounting with it outside the first page', async () => {
    scriptApiMock.listScripts.mockImplementation((_project: string, _folder: undefined, { search: query }: { search: string }) => Promise.resolve(
      query ? { scripts: [script('s300', 'Deep checkout')], count: 1 } : { scripts: [script('s1', 'Checkout')], count: 300 },
    ))
    await act(async () => { root.render(<Harness />) })
    await settle()
    type('deep')
    await settle(300)
    choose('s300')

    // The form that held the picker closes and opens again with the choice.
    await act(async () => { root.render(<div />) })
    await act(async () => { root.render(<Harness initialValue="s300" />) })
    await settle()
    expect(select().value).toBe('s300')
    expect(select().selectedOptions[0]?.textContent).toBe('Deep checkout · web')
  })

  it('never hides a selection it cannot name', async () => {
    scriptApiMock.listScripts.mockResolvedValue({ scripts: [script('s1', 'Checkout')], count: 1 })
    await act(async () => { root.render(<Harness initialValue="unknown-script" />) })
    await settle()
    expect(select().value).toBe('unknown-script')
    expect(select().selectedOptions[0]?.textContent).toBe('The chosen script')
  })

  it('searches scripts the page already holds without asking the server', async () => {
    const held = Array.from({ length: 60 }, (_, index) => ({ id: `s${index}`, name: `Journey ${index}`, protocol: 'http', refKey: index === 7 ? 'LR-CHECKOUT' : undefined }))
    await act(async () => { root.render(<Harness scripts={held} />) })
    await settle()
    expect(scriptApiMock.listScripts).not.toHaveBeenCalled()
    expect(select().options).toHaveLength(51)
    expect(container.textContent).toContain('Showing 50 of 60 scripts')

    type('lr-check')
    await settle()
    expect(optionLabels()).toEqual(['Choose a script', 'Journey 7 · http'])
    expect(container.textContent).not.toContain('Showing')
    expect(scriptApiMock.listScripts).not.toHaveBeenCalled()
  })

  it('puts the search before the list it narrows', async () => {
    scriptApiMock.listScripts.mockResolvedValue({ scripts: [], count: 0 })
    await act(async () => { root.render(<Harness />) })
    await settle()
    const controls = Array.from(container.querySelectorAll('.script-picker-controls > *')).map(element => element.tagName)
    expect(controls).toEqual(['INPUT', 'SELECT'])
  })
})
