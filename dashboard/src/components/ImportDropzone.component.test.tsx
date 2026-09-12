// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ImportDropzone from './ImportDropzone'
import type { WailsDesktopBridge } from '../desktop/desktopBridge'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

describe('ImportDropzone', () => {
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
    delete (window as BridgeWindow).go
  })

  function render(props: Partial<Parameters<typeof ImportDropzone>[0]> = {}) {
    const onFile = vi.fn()
    act(() => {
      root.render(
        <ImportDropzone
          accept=".json"
          file={props.file ?? null}
          onFile={props.onFile ?? onFile}
          dropHint={props.dropHint ?? 'Drop a file here'}
          validate={props.validate}
          onError={props.onError}
          disabled={props.disabled}
        />,
      )
    })
    return onFile
  }

  it('shows the drop hint when empty', () => {
    render()
    expect(container.textContent).toContain('Drop a file here')
    expect(container.textContent).toContain('or click to browse')
  })

  it('shows file details and a change button once selected', () => {
    const file = new File(['{}'], 'spec.json', { type: 'application/json' })
    render({ file })
    expect(container.textContent).toContain('spec.json')
    expect(container.textContent).toContain('Change')
  })

  it('removes the file only when Remove is clicked', () => {
    const file = new File(['{}'], 'spec.json', { type: 'application/json' })
    const onFile = vi.fn()
    render({ file, onFile })
    const change = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Remove')
    act(() => {
      change!.click()
    })
    expect(onFile).toHaveBeenCalledWith(null)
  })

  it('opens Change without discarding the selected file and prevents nested button semantics', () => {
    const file = new File(['{}'], 'spec.json')
    const onFile = render({ file })
    const input = container.querySelector('input')!
    const browse = vi.spyOn(input, 'click')
    expect(container.querySelector('.import-dropzone')?.getAttribute('role')).toBe('group')
    act(() => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Change')!.click())
    expect(browse).toHaveBeenCalledTimes(1)
    expect(onFile).not.toHaveBeenCalled()
  })

  it('prevents space from scrolling when opening the keyboard picker', () => {
    render()
    const browse = vi.spyOn(container.querySelector('input')!, 'click')
    const event = new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true })
    act(() => container.querySelector('.import-dropzone')!.dispatchEvent(event))
    expect(event.defaultPrevented).toBe(true)
    expect(browse).toHaveBeenCalledTimes(1)
  })

  it('clears a previous rejection after a valid selection', () => {
    const onError = vi.fn()
    render({ onError })
    const event = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'dataTransfer', { value: { files: [new File(['{}'], 'valid.json')] } })
    act(() => container.querySelector('.import-dropzone')!.dispatchEvent(event))
    expect(onError).toHaveBeenCalledWith('')
  })

  it('accepts a dropped file', () => {
    const onFile = vi.fn()
    render({ onFile })
    const zone = container.querySelector('.import-dropzone')!
    const file = new File(['{}'], 'dropped.json', { type: 'application/json' })
    const event = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'dataTransfer', { value: { files: [file] } })
    act(() => {
      zone.dispatchEvent(event)
    })
    expect(onFile).toHaveBeenCalledWith(file)
  })

  it('rejects files failing validation', () => {
    const onFile = vi.fn()
    const onError = vi.fn()
    render({
      onFile,
      onError,
      validate: candidate => (candidate.name.endsWith('.json') ? null : 'JSON only'),
    })
    const zone = container.querySelector('.import-dropzone')!
    const file = new File(['x'], 'bad.txt', { type: 'text/plain' })
    const event = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'dataTransfer', { value: { files: [file] } })
    act(() => {
      zone.dispatchEvent(event)
    })
    expect(onFile).not.toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith('JSON only')
  })

  it('surfaces native bridge limits and switches subsequent clicks to the browser picker', async () => {
    const open = vi.fn().mockResolvedValue('/tmp/large.json')
    const read = vi.fn().mockRejectedValue(new Error('file is too large for the bridge; use the upload flow'))
    ;(window as BridgeWindow).go = { main: { App: { OpenFileDialogNative: open, ReadFileBase64: read } } }
    const onError = vi.fn()
    render({ onError })
    const zone = container.querySelector('.import-dropzone') as HTMLElement

    await act(async () => { zone.click() })
    expect(onError).toHaveBeenCalledWith(expect.stringContaining('Use the browser file chooser'))
    expect(open).toHaveBeenCalledTimes(1)

    await act(async () => { zone.click() })
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('rejects a native picker result when the dropzone becomes disabled while the picker is open', async () => {
    let resolveOpen!: (path: string) => void
    const open = vi.fn(() => new Promise<string>(resolve => { resolveOpen = resolve }))
    const read = vi.fn().mockResolvedValue('e30=')
    ;(window as BridgeWindow).go = { main: { App: { OpenFileDialogNative: open, ReadFileBase64: read } } }
    const onFile = vi.fn()

    render({ onFile })
    const zone = container.querySelector<HTMLElement>('.import-dropzone')!
    act(() => zone.click())
    expect(open).toHaveBeenCalledTimes(1)

    act(() => {
      root.render(
        <ImportDropzone
          accept=".json"
          file={null}
          onFile={onFile}
          dropHint="Drop a file here"
          disabled
        />,
      )
    })
    await act(async () => {
      resolveOpen('/tmp/late.json')
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(read).toHaveBeenCalledWith('/tmp/late.json')
    expect(onFile).not.toHaveBeenCalled()
    expect(zone.getAttribute('aria-disabled')).toBe('true')
  })
})
