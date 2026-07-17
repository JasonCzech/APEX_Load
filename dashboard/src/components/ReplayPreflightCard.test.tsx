// @vitest-environment jsdom

import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ReplayPreflightCard from './ReplayPreflightCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ReplayPreflightCard', () => {
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

  it('surfaces expected replay action count when control flow expands fixed loops', () => {
    act(() => {
      root.render(<ReplayPreflightCard preflight={{
        valid: true,
        action_count: 4,
        expected_action_count: 5,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: true,
        checks: [],
      }} />)
    })

    expect(container.textContent).toContain('4 actions')
    expect(container.textContent).toContain('5 expected')
    expect(container.textContent).toContain('Browser runtime')
  })

  it('offers project secret configuration when replay is missing credentials', () => {
    const onConfigureSecrets = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={{
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: false,
        missing_secrets: ['ORDER_API_TOKEN'],
        checks: [],
      }} onConfigureSecrets={onConfigureSecrets} />)
    })

    expect(container.textContent).toContain('Missing secret: ORDER_API_TOKEN')
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent === 'Configure secrets')
    act(() => button?.click())
    expect(onConfigureSecrets).toHaveBeenCalledOnce()
  })

  it('shows unavailable credential status as blocked with configure and retry actions', () => {
    const onConfigureSecrets = vi.fn()
    const onRetrySecretStatus = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={{
        valid: true,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: false,
        required_secrets: ['ORDER_API_TOKEN'],
        checks: [],
      }} secretStatusState="unavailable" onConfigureSecrets={onConfigureSecrets} onRetrySecretStatus={onRetrySecretStatus} />)
    })

    expect(container.querySelector('.replay-preflight-card')?.classList.contains('attention')).toBe(true)
    expect(container.textContent).toContain('Credential status unavailable')
    expect(container.textContent).toContain('Replay remains blocked')
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    act(() => buttons.find(button => button.textContent === 'Configure secrets')?.click())
    act(() => buttons.find(button => button.textContent === 'Retry credential check')?.click())
    expect(onConfigureSecrets).toHaveBeenCalledOnce()
    expect(onRetrySecretStatus).toHaveBeenCalledOnce()
  })
})
