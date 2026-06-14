// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ConfirmDestructiveModal from './ConfirmDestructiveModal'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ConfirmDestructiveModal', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('requires reason and exact target confirmation before submit', async () => {
    const onConfirm = vi.fn()

    await act(async () => {
      root.render(
        <ConfirmDestructiveModal
          title="Restart Pod"
          description="Delete one pod"
          target="apexload-generator-0"
          allowForce
          forceDescription="Can interrupt work."
          onCancel={() => {}}
          onConfirm={onConfirm}
        />,
      )
    })

    const submit = button('Confirm')
    expect(submit.disabled).toBe(true)

    await changeInput('Operational reason', 'stuck generator')
    expect(submit.disabled).toBe(true)

    await changeInput('apexload-generator-0', 'wrong')
    expect(submit.disabled).toBe(true)

    await changeInput('apexload-generator-0', 'apexload-generator-0')
    expect(submit.disabled).toBe(false)

    const force = container.querySelector<HTMLInputElement>('input[type="checkbox"]')
    expect(force).not.toBeNull()
    await act(async () => {
      force?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await act(async () => {
      submit.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onConfirm).toHaveBeenCalledWith({
      reason: 'stuck generator',
      confirmation: 'apexload-generator-0',
      force: true,
    })
  })

  it('renders blocked error and force warning text', async () => {
    await act(async () => {
      root.render(
        <ConfirmDestructiveModal
          title="Restart Controller"
          description="Roll the Deployment"
          target="apexload-controller"
          allowForce
          forceDescription="Active runs will be failed."
          warning="Any in-flight runs will be marked FAILED."
          error="controller restart would fail active runs"
          onCancel={() => {}}
          onConfirm={() => {}}
        />,
      )
    })

    expect(container.textContent).toContain('Any in-flight runs will be marked FAILED.')
    expect(container.textContent).toContain('controller restart would fail active runs')
    expect(container.textContent).toContain('Active runs will be failed.')
  })

  function button(label: string) {
    const found = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(found).not.toBeUndefined()
    return found as HTMLButtonElement
  }

  async function changeInput(placeholder: string, value: string) {
    const input = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(candidate => candidate.placeholder === placeholder)
    expect(input).not.toBeUndefined()
    await act(async () => {
      setInputValue(input!, value)
      input!.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  function setInputValue(input: HTMLInputElement, value: string) {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
  }
})
