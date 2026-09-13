// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { DEFAULT_REPLAY_OPTIONS } from './scriptEditorState'
import { parameterUsageLabel, debugOverrideCount, handleOutlineKeys, sessionVariablesError } from './scriptSidebarModel'
import { DebugParameterInspector } from './DebugParameterInspector'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('sidebar navigation and session context', () => {
  it.each(['', '   ', '{}', '{"count":3,"empty":null}'])('accepts runtime-supported variable objects: %s', text => {
    expect(sessionVariablesError(text)).toBeNull()
    expect(() => debugOverrideCount({ ...DEFAULT_REPLAY_OPTIONS, variablesText: text })).not.toThrow()
  })
  it.each(['[1]', 'null', '"text"', '{'])('reports invalid variables without crashing the options summary: %s', text => {
    expect(sessionVariablesError(text)).toBeTruthy()
    expect(debugOverrideCount({ ...DEFAULT_REPLAY_OPTIONS, variablesText: text })).toBe(1)
  })
  it('names nested usages by action without losing the field path', () => {
    expect(parameterUsageLabel({ actions: [{ name: 'Order' }] } as ScriptDefinition, 'actions[0].control.steps[2].body')).toBe('Order · control.steps[2].body')
    expect(parameterUsageLabel({ actions: [] } as unknown as ScriptDefinition, 'config.base_url')).toBe('config.base_url')
  })
  it('counts semantic overrides without treating whitespace as a change', () => {
    expect(debugOverrideCount({ ...DEFAULT_REPLAY_OPTIONS, variablesText: '{  }' })).toBe(0)
    expect(debugOverrideCount({ ...DEFAULT_REPLAY_OPTIONS, logLevel: 'debug', startPaused: true, variablesText: '{"token":"x"}' })).toBe(3)
  })
  it('moves focus through visible rows and preserves text-input arrow keys', () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    act(() => root.render(<nav onKeyDown={handleOutlineKeys}><input /><button data-outline-select>First</button><button data-outline-select hidden>Filtered out</button><button data-outline-select>Last</button></nav>))
    const buttons = Array.from(host.querySelectorAll('button'))
    for (const button of buttons) {
      vi.spyOn(button, 'getClientRects').mockReturnValue((button.hidden ? [] : [{}]) as unknown as DOMRectList)
      button.scrollIntoView = vi.fn()
    }
    buttons[0].focus()
    buttons[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    expect(document.activeElement).toBe(buttons[2])
    buttons[2].dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }))
    expect(document.activeElement).toBe(buttons[0])
    host.querySelector('input')!.focus()
    host.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    expect(document.activeElement).toBe(host.querySelector('input'))
    act(() => root.unmount()); host.remove()
  })
  it('never treats a seeded variable as a captured parameter and masks explicit captures', () => {
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    const script = { name: 'example', config: {}, protocol: 'http', actions: [], parameters: [{ name: 'password', type: 'list', values: ['draft-secret'] }] } as unknown as ScriptDefinition
    const event = { phase: 'action', action_name: 'Login', iteration: 2 } as BrowserReplayEvent
    const edit = vi.fn()
    act(() => root.render(<DebugParameterInspector script={script} name="password" values={{ password: 'seeded-secret' }} event={event} matchesDraft={false} onEdit={edit} />))
    expect(host.textContent).toContain('Not captured')
    expect(host.textContent).toContain('Draft changed since replay')
    expect(host.textContent).not.toContain('seeded-secret')
    expect(host.textContent).not.toContain('draft-secret')
    act(() => root.render(<DebugParameterInspector script={script} name="password" values={{ 'param.password': 'captured-secret' }} event={event} matchesDraft onEdit={edit} />))
    expect(host.querySelector('output')?.textContent).toBe('••••••••')
    act(() => Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Reveal captured value')!.click())
    expect(host.querySelector('output')?.textContent).toBe('captured-secret')
    act(() => host.querySelector('button')!.click()); expect(edit).toHaveBeenCalledWith('password')
    act(() => root.unmount()); host.remove()
  })
})
