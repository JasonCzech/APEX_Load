// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { JavaEnvSettingsFields, type JavaEnvSettingsValue } from './JavaEnvSettingsFields'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('JavaEnvSettingsFields JVM sharing', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  async function render(value: JavaEnvSettingsValue, onChange: (next: JavaEnvSettingsValue) => void, listOverrideInherits = false) {
    await act(async () => {
      root.render(<JavaEnvSettingsFields idPrefix="test" value={value} onChange={onChange} listOverrideInherits={listOverrideInherits} />)
    })
  }

  function radio(label: string): HTMLInputElement {
    const match = Array.from(container.querySelectorAll('[role="radiogroup"][aria-label="JVM sharing"] label')).find(node => node.textContent === label)
    const input = match?.querySelector('input')
    if (!input) throw new Error(`radio ${label} not found`)
    return input
  }

  it('defaults scripts to one JVM per VUser and reveals the per-JVM count for shared JVMs', async () => {
    const onChange = vi.fn()
    await render({}, onChange)
    expect(radio('One JVM per VUser').checked).toBe(true)
    expect(radio('Shared JVMs').checked).toBe(false)
    expect(container.querySelector('[aria-label="VUsers per JVM"]')).toBeNull()
    expect(container.textContent).not.toContain("Use the script's setting")
    expect(container.textContent).toContain('A scenario group can choose JVM sharing for its runs without one.')

    await act(async () => radio('Shared JVMs').click())
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ jvm_sharing: 'shared' }))

    await render({ jvm_sharing: 'shared', vusers_per_jvm: 50 }, onChange)
    const count = container.querySelector<HTMLInputElement>('[aria-label="VUsers per JVM"]')!
    expect(count.value).toBe('50')
    expect(count.min).toBe('2')
    expect(count.max).toBe('1024')

    await act(async () => radio('One JVM per VUser').click())
    // The script default is stored as no value, and the count goes with it.
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ jvm_sharing: undefined, vusers_per_jvm: undefined }))
  })

  it('lets a group inherit, force dedicated, or choose shared JVMs', async () => {
    const onChange = vi.fn()
    await render({}, onChange, true)
    expect(radio("Use the script's setting").checked).toBe(true)
    expect(container.textContent).not.toContain('needs a new authoring approval')

    await act(async () => radio('One JVM per VUser').click())
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ jvm_sharing: 'dedicated', vusers_per_jvm: undefined }))

    await render({ jvm_sharing: 'dedicated' }, onChange, true)
    expect(radio('One JVM per VUser').checked).toBe(true)
    await act(async () => radio("Use the script's setting").click())
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ jvm_sharing: undefined, vusers_per_jvm: undefined }))
  })
})
