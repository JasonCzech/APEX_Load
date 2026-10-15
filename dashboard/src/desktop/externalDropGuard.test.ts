// @vitest-environment jsdom

import { afterEach, describe, expect, it } from 'vitest'

import { installExternalDropGuard } from './externalDropGuard'

// jsdom has no DragEvent; a cancelable event carrying the drag's types and a
// writable dropEffect stands in for one.
function drag(target: EventTarget, type: string, types: string[]): Event & { dataTransfer: { types: string[]; dropEffect: string } } {
  const event = new Event(type, { bubbles: true, cancelable: true }) as Event & { dataTransfer: { types: string[]; dropEffect: string } }
  Object.defineProperty(event, 'dataTransfer', { value: { types, dropEffect: 'copy' } })
  target.dispatchEvent(event)
  return event
}

describe('external drop guard', () => {
  let uninstall: (() => void) | undefined
  afterEach(() => {
    uninstall?.()
    uninstall = undefined
    document.body.innerHTML = ''
  })

  it('refuses a dropped link or text outside a text field and leaves files to the runtime', () => {
    uninstall = installExternalDropGuard(window)
    document.body.innerHTML = '<div id="page"><input id="search"><input id="locked" readonly><textarea id="notes"></textarea></div>'
    const byId = (id: string) => document.getElementById(id)!

    const over = drag(byId('page'), 'dragover', ['text/uri-list', 'text/plain'])
    expect(over.defaultPrevented).toBe(true)
    expect(over.dataTransfer.dropEffect).toBe('none')
    expect(drag(byId('page'), 'dragenter', ['text/uri-list']).defaultPrevented).toBe(true)
    expect(drag(byId('page'), 'drop', ['text/uri-list']).defaultPrevented).toBe(true)
    expect(drag(byId('locked'), 'drop', ['text/plain']).defaultPrevented).toBe(true)

    // Text into a field drops as usual; files belong to Wails' drop runtime.
    expect(drag(byId('search'), 'dragover', ['text/plain']).defaultPrevented).toBe(false)
    expect(drag(byId('notes'), 'drop', ['text/plain']).defaultPrevented).toBe(false)
    expect(drag(byId('page'), 'dragover', ['Files']).defaultPrevented).toBe(false)
    expect(drag(byId('page'), 'drop', ['Files']).defaultPrevented).toBe(false)
  })

  it("leaves the app's own drag and drop alone", () => {
    uninstall = installExternalDropGuard(window)
    document.body.innerHTML = '<div id="page"><div id="card" draggable="true">Card</div></div>'
    const card = document.getElementById('card')!

    card.dispatchEvent(new Event('dragstart', { bubbles: true }))
    expect(drag(document.getElementById('page')!, 'dragover', ['text/plain']).defaultPrevented).toBe(false)
    card.dispatchEvent(new Event('dragend', { bubbles: true }))
    expect(drag(document.getElementById('page')!, 'dragover', ['text/plain']).defaultPrevented).toBe(true)

    // A drag whose source left the page never sends its dragend to the window;
    // the next mouse move (none fire during a drag) ends it all the same.
    card.dispatchEvent(new Event('dragstart', { bubbles: true }))
    card.remove()
    window.dispatchEvent(new MouseEvent('mousemove'))
    expect(drag(document.getElementById('page')!, 'dragover', ['text/uri-list']).defaultPrevented).toBe(true)
  })

  it('stops guarding once removed', () => {
    installExternalDropGuard(window)()
    document.body.innerHTML = '<div id="page"></div>'
    expect(drag(document.getElementById('page')!, 'drop', ['text/uri-list']).defaultPrevented).toBe(false)
  })
})
