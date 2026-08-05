// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AuthoringSession } from '../types'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

describe('AuthoringSessionsPanel correlation repair', () => {
  it('submits the operator-selected producer and consumer actions', () => {
    const session = {
      id: 'session-1',
      name: 'Checkout authoring',
      status: 'draft',
      source: 'manual',
      updated_at: '2026-08-04T12:00:00Z',
      draft: {
        name: 'Checkout',
        protocol: 'http',
        config: {},
        actions: [
          { name: 'Login', type: 'GET', target: '/login' },
          { name: 'Create cart', type: 'POST', target: '/cart' },
          { name: 'Checkout', type: 'POST', target: '/checkout' },
        ],
      },
    } as AuthoringSession
    const onPreviewCorrelation = vi.fn()
    act(() => root.render(
      <AuthoringSessionsPanel
        sessions={[session]}
        onResume={() => {}}
        onApprove={() => {}}
        onDiscard={() => {}}
        onPreviewCorrelation={onPreviewCorrelation}
        onApplyCorrelation={() => {}}
      />,
    ))

    const producer = container.querySelector('select[aria-label="Correlation producer for Checkout authoring"]') as HTMLSelectElement
    const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Repair')!
    expect(preview.disabled).toBe(true)
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!
      setter.call(producer, '2')
      producer.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const consumerLabels = Array.from(container.querySelectorAll('.authoring-correlation-consumers label'))
    expect(consumerLabels).toHaveLength(1)
    expect(consumerLabels[0].textContent).toContain('3. Checkout')
    expect(container.querySelector('.authoring-correlation-consumers')?.textContent).not.toContain('1. Login')
    expect(preview.disabled).toBe(false)
    act(() => preview.click())

    expect(onPreviewCorrelation).toHaveBeenCalledWith(session, expect.objectContaining({
      expected_updated_at: '2026-08-04T12:00:00Z',
      found_action_index: 2,
      used_action_indexes: [3],
    }))
  })

  it('resets stale action choices when the same session receives a changed draft', () => {
    const session = buildSession()
    const onPreviewCorrelation = vi.fn()
    const render = (next: AuthoringSession) => {
      act(() => root.render(
        <AuthoringSessionsPanel
          sessions={[next]}
          onResume={() => {}}
          onApprove={() => {}}
          onDiscard={() => {}}
          onPreviewCorrelation={onPreviewCorrelation}
          onApplyCorrelation={() => {}}
        />,
      ))
    }
    render(session)

    let producer = container.querySelector('select[aria-label="Correlation producer for Checkout authoring"]') as HTMLSelectElement
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!
      setter.call(producer, '3')
      producer.dispatchEvent(new Event('change', { bubbles: true }))
    })
    let preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Repair')!
    expect(preview.disabled).toBe(false)

    // Keep updated_at unchanged to prove the action fingerprint itself fences a
    // corrected/shrunk draft delivered for the same durable session.
    const shrunk = {
      ...session,
      draft: {
        ...session.draft!,
        actions: session.draft!.actions!.slice(0, 2),
      },
    }
    render(shrunk)

    producer = container.querySelector('select[aria-label="Correlation producer for Checkout authoring"]') as HTMLSelectElement
    preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Repair')!
    expect(producer.value).toBe('')
    expect(preview.disabled).toBe(true)
    act(() => preview.click())
    expect(onPreviewCorrelation).not.toHaveBeenCalled()
  })

  it('fingerprints a large action graph only once per session render', () => {
    const session = buildSession()
    session.draft!.actions = Array.from({ length: 24 }, (_, index) => ({
      name: `Action ${index + 1}`,
      type: 'GET',
      target: `/action-${index + 1}`,
    }))
    const actions = session.draft!.actions
    const stringify = vi.spyOn(JSON, 'stringify')
    try {
      act(() => root.render(
        <AuthoringSessionsPanel
          sessions={[session]}
          onResume={() => {}}
          onApprove={() => {}}
          onDiscard={() => {}}
          onPreviewCorrelation={() => {}}
          onApplyCorrelation={() => {}}
        />,
      ))

      expect(stringify.mock.calls.filter(([value]) => value === actions)).toHaveLength(1)
    } finally {
      stringify.mockRestore()
    }
  })
})

function buildSession(): AuthoringSession {
  return {
    id: 'session-1',
    name: 'Checkout authoring',
    status: 'draft',
    source: 'manual',
    updated_at: '2026-08-04T12:00:00Z',
    draft: {
      name: 'Checkout',
      protocol: 'http',
      config: {},
      actions: [
        { name: 'Login', type: 'GET', target: '/login' },
        { name: 'Create cart', type: 'POST', target: '/cart' },
        { name: 'Checkout', type: 'POST', target: '/checkout' },
      ],
    },
  } as AuthoringSession
}
