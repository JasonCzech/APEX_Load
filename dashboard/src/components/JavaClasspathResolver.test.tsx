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

  const dropFileOnRow = (row: Element, file: File) => {
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [file] } })
    act(() => {
      row.dispatchEvent(drop)
    })
  }

  const dragOverRow = (row: Element) => {
    const dataTransfer = { files: [] as File[], dropEffect: 'uninitialized' }
    const dragOver = new Event('dragover', { bubbles: true, cancelable: true })
    Object.defineProperty(dragOver, 'dataTransfer', { value: dataTransfer })
    act(() => {
      row.dispatchEvent(dragOver)
    })
    return { defaultPrevented: dragOver.defaultPrevented, dropEffect: dataTransfer.dropEffect }
  }

  it.each([
    ['accepting', [] as JavaClasspathAsset[], false, 'copy'],
    ['refusing', [{ entry: 'a.jar', file_name: 'a.jar', sha256: 'deadbeefcafebabe0000' }], true, 'none'],
  ])('claims the dragover even when %s the drop', (_label, assets, refused, wantEffect) => {
    // preventDefault must run in BOTH cases. Returning early when refusing would
    // leave the row a non-target, so the browser would handle the dropped file
    // itself and navigate away from the import, losing the in-progress review.
    render({ classpath: ['a.jar'], assets: assets as JavaClasspathAsset[], onChange: vi.fn(), onUpload: vi.fn() })

    const result = dragOverRow(container.querySelector('.java-classpath-resolver-row')!)
    expect(result.defaultPrevented).toBe(true)
    expect(result.dropEffect).toBe(wantEffect)
    expect(refused).toBe(wantEffect === 'none')
  })

  it('uploads a JAR dropped onto its classpath row', async () => {
    const onChange = vi.fn()
    const onUpload = vi.fn().mockResolvedValue({ entry: 'a.jar', file_name: 'drop.jar', sha256: 'dropsha1' })
    render({ classpath: ['a.jar'], assets: [], onChange, onUpload })

    const file = new File(['bytes'], 'drop.jar', { type: 'application/java-archive' })
    dropFileOnRow(container.querySelector('.java-classpath-resolver-row')!, file)
    await act(async () => { await Promise.resolve() })

    expect(onUpload).toHaveBeenCalledWith(file, 'a.jar')
    expect(onChange).toHaveBeenCalledWith([{ entry: 'a.jar', file_name: 'drop.jar', sha256: 'dropsha1' }])
  })

  it('refuses a drop on a row that already has an attached JAR', async () => {
    // Dropping must honor the same refusal conditions as the Attach button, or
    // the serialized-upload invariant that keeps bindings from being lost breaks.
    const onUpload = vi.fn()
    const asset: JavaClasspathAsset = { entry: 'a.jar', file_name: 'a.jar', sha256: 'deadbeefcafebabe0000' }
    render({ classpath: ['a.jar'], assets: [asset], onChange: vi.fn(), onUpload })

    dropFileOnRow(container.querySelector('.java-classpath-resolver-row')!, new File(['bytes'], 'drop.jar'))
    await act(async () => { await Promise.resolve() })

    expect(onUpload).not.toHaveBeenCalled()
  })

  it('turns the fail-closed server gate into remediation instead of a bare error', async () => {
    // The upload endpoint answers 404 with "java classpath upload is not
    // enabled" when the operator has not opted in. Surfaced raw, the panel
    // reads as broken rather than switched off.
    const onUpload = vi.fn().mockRejectedValue(new Error('java classpath upload is not enabled'))
    render({ classpath: ['a.jar'], assets: [], onChange: vi.fn(), onUpload })

    const input = container.querySelector<HTMLInputElement>('.entry-asset.unattached input[type="file"]')
    setFile(input!, new File(['bytes'], 'new.jar'))
    await act(async () => { await Promise.resolve() })

    const error = container.querySelector('.cell-error')
    expect(error?.textContent).toContain('APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED')
    expect(error?.textContent).toContain('provision this dependency on the runners')
  })

  it('keeps the original message for other upload failures', async () => {
    const onUpload = vi.fn().mockRejectedValue(new Error('jar exceeds the maximum allowed size'))
    render({ classpath: ['a.jar'], assets: [], onChange: vi.fn(), onUpload })

    const input = container.querySelector<HTMLInputElement>('.entry-asset.unattached input[type="file"]')
    setFile(input!, new File(['bytes'], 'new.jar'))
    await act(async () => { await Promise.resolve() })

    expect(container.querySelector('.cell-error')?.textContent).toContain('jar exceeds the maximum allowed size')
  })

  it('refuses a drop while the resolver is disabled', async () => {
    const onUpload = vi.fn()
    render({ classpath: ['a.jar'], assets: [], onChange: vi.fn(), onUpload, disabled: true })

    dropFileOnRow(container.querySelector('.java-classpath-resolver-row')!, new File(['bytes'], 'drop.jar'))
    await act(async () => { await Promise.resolve() })

    expect(onUpload).not.toHaveBeenCalled()
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

  it('disables both removal and file input while the parent saves the draft', () => {
    const onChange = vi.fn()
    const onUpload = vi.fn()
    render({ classpath: ['a.jar', 'b.jar'], assets: [{ entry: 'a.jar', sha256: 'a'.repeat(64) }], onChange, onUpload, disabled: true })
    const remove = container.querySelector<HTMLButtonElement>('.chip-remove')!
    expect(remove.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[type="file"]')?.disabled).toBe(true)
    act(() => remove.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onChange).not.toHaveBeenCalled()
  })

  it('ignores a pending upload after the review resolver is unmounted', async () => {
    let finishUpload: (asset: JavaClasspathAsset) => void = () => {}
    const onUpload = vi.fn().mockImplementation(() => new Promise<JavaClasspathAsset>(resolve => { finishUpload = resolve }))
    const onChange = vi.fn()
    render({ classpath: ['a.jar'], assets: [], onChange, onUpload })
    setFile(container.querySelector<HTMLInputElement>('input[type="file"]')!, new File(['bytes'], 'a.jar'))
    act(() => root.render(<div>Another review</div>))
    await act(async () => finishUpload({ entry: 'a.jar', sha256: 'a'.repeat(64) }))
    expect(onChange).not.toHaveBeenCalled()
    expect(container.textContent).toBe('Another review')
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
