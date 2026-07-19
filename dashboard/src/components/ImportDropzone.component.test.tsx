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

  it('clears the file when change is clicked', () => {
    const file = new File(['{}'], 'spec.json', { type: 'application/json' })
    const onFile = vi.fn()
    render({ file, onFile })
    const change = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Change')
    act(() => {
      change!.click()
    })
    expect(onFile).toHaveBeenCalledWith(null)
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
})
