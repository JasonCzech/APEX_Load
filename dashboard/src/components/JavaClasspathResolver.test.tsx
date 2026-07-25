// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { JavaClasspathResolver } from './JavaClasspathResolver'
import type { JavaClasspathAsset } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('JavaClasspathResolver', () => {
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

  const render = (props: Parameters<typeof JavaClasspathResolver>[0]) => {
    act(() => {
      root.render(<JavaClasspathResolver {...props} />)
    })
  }

  it('renders one row per classpath entry with an attach control when unattached', () => {
    render({ classpath: ['a.jar', 'b.jar'], assets: [], onChange: vi.fn(), onUpload: vi.fn() })
    expect(container.textContent).toContain('a.jar')
    expect(container.textContent).toContain('b.jar')
    const attachButtons = container.querySelectorAll('.entry-asset.unattached .row-add')
    expect(attachButtons.length).toBe(2)
  })

  it('renders an attached chip and removes it on click', () => {
    const onChange = vi.fn()
    const asset: JavaClasspathAsset = { entry: 'a.jar', file_name: 'a.jar', sha256: 'deadbeefcafebabe0000' }
    render({ classpath: ['a.jar', 'b.jar'], assets: [asset], onChange, onUpload: vi.fn() })

    // a.jar is attached (chip + short sha), b.jar still shows the attach button.
    expect(container.querySelectorAll('.entry-asset.attached').length).toBe(1)
    expect(container.querySelectorAll('.entry-asset.unattached .row-add').length).toBe(1)
    expect(container.textContent).toContain('deadbeef')

    const removeButton = container.querySelector<HTMLButtonElement>('.entry-asset.attached .chip-remove')
    expect(removeButton).not.toBeNull()
    act(() => {
      removeButton!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('shows a friendly message when there are no classpath entries', () => {
    render({ classpath: [], assets: [], onChange: vi.fn(), onUpload: vi.fn() })
    expect(container.textContent).toContain('no dependencies to attach')
  })

  const setFile = (input: HTMLInputElement, file: File) => {
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    act(() => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  it('uploads an attached file and reports the new binding via onChange', async () => {
    const onChange = vi.fn()
    const onUpload = vi.fn().mockResolvedValue({ entry: 'a.jar', file_name: 'new.jar', sha256: 'newsha11' })
    render({ classpath: ['a.jar', 'b.jar'], assets: [], onChange, onUpload })

    const input = container.querySelector<HTMLInputElement>('.entry-asset.unattached input[type="file"]')
    expect(input).not.toBeNull()
    const file = new File(['bytes'], 'new.jar')
    setFile(input!, file)
    await act(async () => { await Promise.resolve() })

    // onUpload is called for the first (a.jar) row's entry, and the returned
    // binding is merged in (replace-by-entry: prior assets minus a.jar, plus new).
    expect(onUpload).toHaveBeenCalledWith(file, 'a.jar')
    expect(onChange).toHaveBeenCalledWith([{ entry: 'a.jar', file_name: 'new.jar', sha256: 'newsha11' }])
  })

  it('serializes uploads: every attach control is disabled while one is in flight', async () => {
    // A second attach starting before the first resolves would merge from a
    // stale assets closure and silently drop the earlier binding.
    let resolveUpload: (asset: JavaClasspathAsset) => void = () => {}
    const onUpload = vi.fn().mockImplementation(() => new Promise<JavaClasspathAsset>(resolve => { resolveUpload = resolve }))
    const onChange = vi.fn()
    render({ classpath: ['a.jar', 'b.jar'], assets: [], onChange, onUpload })

    const inputs = container.querySelectorAll<HTMLInputElement>('.entry-asset.unattached input[type="file"]')
    setFile(inputs[0], new File(['x'], 'a.jar'))
    await act(async () => { await Promise.resolve() })

    // While a.jar uploads, b.jar's attach button must also be disabled.
    const buttons = container.querySelectorAll<HTMLButtonElement>('.entry-asset.unattached .row-add')
    expect(Array.from(buttons).every(b => b.disabled)).toBe(true)

    await act(async () => {
      resolveUpload({ entry: 'a.jar', file_name: 'a.jar', sha256: 'sha000aa' })
      await Promise.resolve()
    })
    expect(onChange).toHaveBeenCalledWith([{ entry: 'a.jar', file_name: 'a.jar', sha256: 'sha000aa' }])
  })

  it('surfaces an upload error via role=alert and does not call onChange', async () => {
    const onChange = vi.fn()
    const onUpload = vi.fn().mockRejectedValue(new Error('boom: upload rejected'))
    render({ classpath: ['a.jar'], assets: [], onChange, onUpload })

    const input = container.querySelector<HTMLInputElement>('.entry-asset.unattached input[type="file"]')!
    setFile(input, new File(['x'], 'a.jar'))
    await act(async () => { await Promise.resolve() })

    expect(onChange).not.toHaveBeenCalled()
    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('boom: upload rejected')
  })
})
