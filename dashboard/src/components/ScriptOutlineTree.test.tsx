// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import ScriptOutlineTree, { type OutlineDebug, type OutlineMultiSelect, type ScriptOutlineSelection } from './ScriptOutlineTree'
import { buildScriptPhaseActionRows, type DebugRailRenderRow } from './scriptEditorWorkbench'
import { buildOutlineModel } from './scriptOutlineModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Raw = Record<string, unknown>
const step = (name: string): Raw => ({ name, type: 'request', method: 'GET', target: `/${name}` })
const marker = (role: 'start' | 'end', name: string): Raw => ({ name: `${role} ${name}`, type: 'source_transaction', params: { function: `lr_${role}_transaction`, args: [name] } })
const SCRIPT = {
  name: 'tree', protocol: 'http',
  actions: [step('home'), step('login'), marker('start', 'Browse'), step('list'), marker('end', 'Browse'), step('logout')],
} as unknown as ScriptDefinition
const MODEL = buildOutlineModel(buildScriptPhaseActionRows(SCRIPT))

interface Spies {
  onSelect: (item: DebugRailRenderRow, options?: { fromMenu?: boolean }) => void
  onContextMenu: OutlineMultiSelect['onContextMenu']
  onWrap: OutlineMultiSelect['onWrap']
}

function Harness({ spies, debug, wrapUnavailable = '' }: { spies: Spies; debug?: OutlineDebug; wrapUnavailable?: string }) {
  const [keys, setKeys] = useState<Set<string>>(new Set())
  const [selection, setSelection] = useState<ScriptOutlineSelection | null>(null)
  return (
    <>
      <output data-testid="keys">{[...keys].join(',')}</output>
      {/* A selection made elsewhere (Problems, the action list). */}
      <button type="button" data-testid="select-logout" onClick={() => setSelection({ phase: 'action', actionIndex: 5 })}>elsewhere</button>
      <ScriptOutlineTree
        model={MODEL}
        resetKey="tree"
        query=""
        expandedContainers={new Set()}
        onToggleContainer={() => undefined}
        selection={selection}
        onSelect={(item, options) => { spies.onSelect(item, options); setSelection({ phase: item.row.phase, actionIndex: item.row.actionIndex }) }}
        hasBreakpoint={() => false}
        hasIssue={() => false}
        showMarkers={false}
        onShowMarkersChange={() => undefined}
        debug={debug}
        multiSelect={{
          keys,
          onChange: next => setKeys(new Set(next)),
          onContextMenu: spies.onContextMenu,
          onWrap: spies.onWrap,
          wrapUnavailable,
        }}
      />
    </>
  )
}

const rowNamed = (label: string) => Array.from(document.querySelectorAll<HTMLElement>('[role="treeitem"]')).find(row => row.getAttribute('aria-label')?.startsWith(`${label} `) || row.getAttribute('aria-label') === label)!
const selectedKeys = () => document.querySelector('[data-testid="keys"]')!.textContent
const click = (row: HTMLElement, init: MouseEventInit = {}) => act(() => { row.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, ...init })) })
const key = (row: HTMLElement, name: string, init: KeyboardEventInit = {}) => act(() => { row.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true, ...init })) })

describe('ScriptOutlineTree multi-select', () => {
  let host: HTMLDivElement
  let root: Root
  let spies: { onSelect: ReturnType<typeof vi.fn>; onContextMenu: ReturnType<typeof vi.fn>; onWrap: ReturnType<typeof vi.fn> }
  const platform = (value: string) => Object.defineProperty(navigator, 'platform', { configurable: true, value })
  beforeEach(() => {
    host = document.createElement('div')
    document.body.append(host)
    root = createRoot(host)
    spies = { onSelect: vi.fn(), onContextMenu: vi.fn(), onWrap: vi.fn() }
    platform('MacIntel')
  })
  afterEach(() => { act(() => root.unmount()); host.remove() })
  const render = (props: { debug?: OutlineDebug; wrapUnavailable?: string } = {}) => act(() => root.render(<Harness spies={spies as unknown as Spies} {...props} />))

  it('adds rows with Cmd-click on macOS, starting from the selected row, and toggles them off', () => {
    render()
    expect(document.querySelector('[role="tree"]')?.getAttribute('aria-multiselectable')).toBe('true')
    click(rowNamed('home'))
    expect(spies.onSelect).toHaveBeenCalledTimes(1)
    click(rowNamed('login'), { metaKey: true })
    expect(selectedKeys()).toBe('action:0,action:1')
    expect(spies.onSelect).toHaveBeenCalledTimes(1)
    expect(rowNamed('login').getAttribute('aria-selected')).toBe('true')
    expect(rowNamed('login').className).toContain('multi-selected')
    click(rowNamed('home'), { metaKey: true })
    expect(selectedKeys()).toBe('action:1')
    // Ctrl-click on macOS is the context menu, not a toggle.
    click(rowNamed('logout'), { ctrlKey: true })
    expect(selectedKeys()).toBe('action:1')
  })

  it('uses Ctrl off macOS', () => {
    platform('Win32')
    render()
    click(rowNamed('home'))
    click(rowNamed('logout'), { ctrlKey: true })
    expect(selectedKeys()).toBe('action:0,action:5')
    click(rowNamed('login'), { metaKey: true })
    expect(selectedKeys()).toBe('')
  })

  it('selects a range with Shift-click and Shift+arrows, and a plain click starts over', () => {
    render()
    click(rowNamed('home'))
    click(rowNamed('Browse'), { shiftKey: true })
    const browse = MODEL.rowKeyByAction.get('action:2')!
    expect(selectedKeys()).toBe(`action:0,action:1,${browse}`)
    key(rowNamed('Browse'), 'ArrowDown', { shiftKey: true })
    expect(selectedKeys()).toBe(`action:0,action:1,${browse},action:3`)
    click(rowNamed('logout'))
    expect(selectedKeys()).toBe('')
    expect(spies.onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'action:5' }), undefined)
  })

  it('selects the rows of one level with Cmd+A and clears with Escape', () => {
    render()
    click(rowNamed('home'))
    key(rowNamed('home'), 'a', { metaKey: true })
    expect(selectedKeys()).toBe(`action:0,action:1,${MODEL.rowKeyByAction.get('action:2')},action:5`)
    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
    act(() => { rowNamed('home').dispatchEvent(escape) })
    expect(selectedKeys()).toBe('')
    expect(escape.defaultPrevented).toBe(true)
  })

  it('opens the menu for the selection, or for an unselected row on its own', () => {
    render()
    click(rowNamed('home'))
    click(rowNamed('login'), { metaKey: true })
    act(() => { rowNamed('login').dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 30, clientY: 40 })) })
    expect(spies.onContextMenu).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'action:1' }), { x: 30, y: 40 }, ['action:0', 'action:1'])
    act(() => { rowNamed('logout').dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 5, clientY: 6 })) })
    expect(spies.onContextMenu).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'action:5' }), { x: 5, y: 6 }, ['action:5'])
    expect(selectedKeys()).toBe('')
    expect(spies.onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'action:5' }), { fromMenu: true })
  })

  it('starts over on Enter as on a plain click, and ranges from a selection made elsewhere', () => {
    render()
    click(rowNamed('home'))
    key(rowNamed('home'), 'ArrowDown', { shiftKey: true })
    expect(selectedKeys()).toBe('action:0,action:1')
    key(rowNamed('list'), 'Enter')
    expect(selectedKeys()).toBe('')
    expect(spies.onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'action:3' }), undefined)
    act(() => document.querySelector<HTMLButtonElement>('[data-testid="select-logout"]')!.click())
    click(rowNamed('Browse'), { shiftKey: true })
    expect(selectedKeys()).toBe(`${MODEL.rowKeyByAction.get('action:2')},action:3,action:5`)
  })

  it('opens the menu from the keyboard (Shift+F10, the menu key) but not on a phase', () => {
    render()
    key(rowNamed('home'), 'F10', { shiftKey: true })
    expect(spies.onContextMenu).toHaveBeenCalledTimes(1)
    key(rowNamed('home'), 'ContextMenu')
    expect(spies.onContextMenu).toHaveBeenCalledTimes(2)
    key(rowNamed('Actions'), 'ContextMenu')
    expect(spies.onContextMenu).toHaveBeenCalledTimes(2)
    // F10 alone is the debugger's Step Over: not handled here.
    const f10 = new KeyboardEvent('keydown', { key: 'F10', bubbles: true, cancelable: true })
    act(() => { rowNamed('home').dispatchEvent(f10) })
    expect(f10.defaultPrevented).toBe(false)
  })

  it('shows a selection bar for two or more rows, with the wrap command', () => {
    render({ wrapUnavailable: '' })
    expect(document.querySelector('.script-outline-selection')).toBeNull()
    click(rowNamed('home'))
    click(rowNamed('login'), { metaKey: true })
    const bar = document.querySelector('.script-outline-selection')!
    expect(bar.textContent).toContain('2 selected')
    const wrap = Array.from(bar.querySelectorAll('button')).find(button => button.textContent === 'Wrap in transaction…')!
    act(() => wrap.click())
    expect(spies.onWrap).toHaveBeenCalledWith(expect.objectContaining({ x: expect.any(Number), y: expect.any(Number) }))
    act(() => Array.from(bar.querySelectorAll('button')).find(button => button.textContent === 'Clear')!.click())
    expect(selectedKeys()).toBe('')
    // The bar goes; focus stays in the tree.
    expect(document.activeElement?.getAttribute('role')).toBe('treeitem')
    expect(document.querySelector('.sr-only[aria-live="polite"]')?.textContent).toBe('')
  })

  it('disables the bar’s wrap command with the reason', () => {
    render({ wrapUnavailable: 'Select steps at the same level to wrap them together.' })
    click(rowNamed('home'))
    click(rowNamed('login'), { metaKey: true })
    const wrap = Array.from(document.querySelectorAll<HTMLButtonElement>('.script-outline-selection button')).find(button => button.textContent === 'Wrap in transaction…')!
    expect(wrap.disabled).toBe(true)
    expect(wrap.getAttribute('data-tooltip')).toContain('same level')
  })

  it('keeps Debug to one selection and no menu', () => {
    const debug: OutlineDebug = { breakpoint: () => null, onToggleBreakpoint: () => undefined, status: () => null }
    render({ debug })
    expect(document.querySelector('[role="tree"]')?.getAttribute('aria-multiselectable')).toBeNull()
    click(rowNamed('home'))
    click(rowNamed('login'), { metaKey: true })
    expect(selectedKeys()).toBe('')
    expect(spies.onSelect).toHaveBeenCalledTimes(2)
    const menu = new MouseEvent('contextmenu', { bubbles: true, cancelable: true })
    act(() => { rowNamed('login').dispatchEvent(menu) })
    expect(spies.onContextMenu).not.toHaveBeenCalled()
    expect(menu.defaultPrevented).toBe(false)
  })
})

describe('ScriptOutlineTree expansion across edits', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); host.remove() })
  const modelOf = (actions: Raw[]) => buildOutlineModel(buildScriptPhaseActionRows({ name: 'x', protocol: 'http', actions } as unknown as ScriptDefinition))
  const render = (model: ReturnType<typeof buildOutlineModel>) => act(() => root.render(
    <ScriptOutlineTree model={model} resetKey="x" query="" expandedContainers={new Set()} onToggleContainer={() => undefined} selection={null}
      onSelect={() => undefined} hasBreakpoint={() => false} hasIssue={() => false} showMarkers={false} onShowMarkersChange={() => undefined} />,
  ))
  const expanded = (label: string) => rowNamed(label)?.getAttribute('aria-expanded')
  const collapse = (label: string) => act(() => rowNamed(label).querySelector<HTMLElement>('.script-outline-toggle')!.click())
  const A = [marker('start', 'A'), step('a1'), marker('end', 'A')]
  const B = [marker('start', 'B'), step('b1'), marker('end', 'B')]

  it('keeps a renamed group closed', () => {
    render(modelOf([...A, ...B]))
    collapse('A')
    expect(expanded('A')).toBe('false')
    render(modelOf([marker('start', 'Renamed'), step('a1'), marker('end', 'Renamed'), ...B]))
    expect(expanded('Renamed')).toBe('false')
    expect(expanded('B')).toBe('true')
  })

  it('opens the group it is asked to reveal and hands the request back', () => {
    const many = Array.from({ length: 70 }, (_, index) => [marker('start', `T${index}`), step(`s${index}a`), step(`s${index}b`), step(`s${index}c`), marker('end', `T${index}`)]).flat()
    const tagged = Array.from({ length: 3 }, (_, index) => ({ ...step(`g${index}`), metadata: { source_transaction: 'Grouped' } }))
    const model = modelOf([...many, ...tagged])
    const onRevealed = vi.fn()
    const renderWith = (reveal: { phase: string; actionIndex: number } | null) => act(() => root.render(
      <ScriptOutlineTree model={model} resetKey="x" query="" expandedContainers={new Set()} onToggleContainer={() => undefined} selection={null}
        onSelect={() => undefined} hasBreakpoint={() => false} hasIssue={() => false} showMarkers={false} onShowMarkersChange={() => undefined} revealGroup={reveal} onRevealed={onRevealed} />,
    ))
    renderWith(null)
    // A long script's groups start closed.
    expect(expanded('T0')).toBe('false')
    renderWith({ phase: 'action', actionIndex: 0 })
    expect(expanded('T0')).toBe('true')
    expect(onRevealed).toHaveBeenCalledTimes(1)
    collapse('T0')
    renderWith(null)
    expect(expanded('T0')).toBe('false')
    // A tagged run (a Grouped wrap) opens from its first step.
    expect(expanded('Grouped')).toBe('false')
    renderWith({ phase: 'action', actionIndex: many.length })
    expect(expanded('Grouped')).toBe('true')
    expect(onRevealed).toHaveBeenCalledTimes(2)
  })

  it('does not pass a deleted group’s state to the group that moves into its place', () => {
    render(modelOf([...A, ...B]))
    collapse('A')
    render(modelOf(B))
    expect(expanded('B')).toBe('true')
  })
})
