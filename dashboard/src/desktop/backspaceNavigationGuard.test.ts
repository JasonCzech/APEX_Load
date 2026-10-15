// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import { installBackspaceNavigationGuard } from './backspaceNavigationGuard'

function press(target: EventTarget, init: KeyboardEventInit = {}): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key: 'Backspace', bubbles: true, cancelable: true, ...init })
  target.dispatchEvent(event)
  return event
}

describe('backspace navigation guard', () => {
  let uninstall: (() => void) | undefined
  afterEach(() => {
    uninstall?.()
    uninstall = undefined
    document.body.innerHTML = ''
  })

  it('cancels Backspace outside an editable field and leaves text editing alone', () => {
    uninstall = installBackspaceNavigationGuard(window)
    document.body.innerHTML = '<div id="page"><button id="button">Run</button><input id="text"><input id="box" type="checkbox"><textarea id="area"></textarea><input id="locked" readonly><select id="pick"><option>a</option></select><div contenteditable="true"><span id="rich">x</span></div></div>'
    const byId = (id: string) => document.getElementById(id)!

    expect(press(byId('page')).defaultPrevented).toBe(true)
    expect(press(byId('button')).defaultPrevented).toBe(true)
    expect(press(byId('box')).defaultPrevented).toBe(true)
    expect(press(byId('locked')).defaultPrevented).toBe(true)
    expect(press(byId('pick')).defaultPrevented).toBe(true)
    expect(press(byId('text')).defaultPrevented).toBe(false)
    expect(press(byId('area')).defaultPrevented).toBe(false)
    expect(press(byId('rich')).defaultPrevented).toBe(false)
    // Modified Backspace does not navigate, and other keys pass through.
    expect(press(byId('page'), { metaKey: true }).defaultPrevented).toBe(false)
    expect(press(byId('page'), { key: 'Delete' }).defaultPrevented).toBe(false)
  })

  it('stops guarding once uninstalled', () => {
    installBackspaceNavigationGuard(window)()
    document.body.innerHTML = '<div id="page"></div>'
    expect(press(document.getElementById('page')!).defaultPrevented).toBe(false)
  })
})
