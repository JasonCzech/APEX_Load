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

  it('lists review gates by what to fix and where, not as the raw gate sentences', () => {
    const gate = (index: number) => `init[${index}] "web_set_sockets_option_${index}": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved`
    const preflight = {
      valid: false,
      action_count: 3,
      expected_action_count: 3,
      data_source_count: 0,
      parameter_count: 0,
      // A static validation issue is not a review gate.
      issues: [gate(0), gate(2), 'actions[0]: target is required'],
      checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: `${gate(0)}; ${gate(2)}` }],
    }
    act(() => {
      root.render(<ReplayPreflightCard preflight={preflight} />)
    })
    const items = () => Array.from(container.querySelectorAll('.replay-preflight-gates li'))
    expect(items().map(item => [item.querySelector('strong')?.textContent, item.querySelector('small')?.textContent])).toEqual([
      ['Choose supported TLS settings', 'Init › web_set_sockets_option_0'],
      ['Choose supported TLS settings', 'Init › web_set_sockets_option_2'],
    ])
    expect(container.textContent).not.toContain('execution blocker legacy_tls_replacement')
    expect(container.querySelector('.replay-preflight-gates button')).toBeNull()

    const onSelectIssue = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={preflight} onSelectIssue={onSelectIssue} issueBreadcrumb={message => message.startsWith('init[2]') ? 'Init › Second socket option' : undefined} />)
    })
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.replay-preflight-gates button'))
    expect(buttons.map(button => button.textContent)).toEqual(['Choose supported TLS settings', 'Choose supported TLS settingsInit › Second socket option'])
    act(() => buttons[1].click())
    expect(onSelectIssue).toHaveBeenCalledWith(gate(2), { code: 'legacy_tls_replacement', message: gate(2) })
  })

  it('lists a gate with several codes once per code, as Author Problems and the start gate count it', () => {
    const twoCodes = 'actions[0] "submit order": execution blocker request_body_xml_json_content_type_mismatch, restored_body_required blocks replay until the imported behavior is resolved'
    // The generic review beside a required body restoration is that restoration's evidence.
    const bodyReview = 'actions[1] "upload": execution blocker import_review_required, restored_body_required blocks replay until the imported behavior is resolved'
    const preflight = {
      valid: false, action_count: 2, expected_action_count: 2, data_source_count: 0, parameter_count: 0,
      issues: [twoCodes, bodyReview],
      checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: `${twoCodes}; ${bodyReview}` }],
    }
    const onSelectIssue = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={preflight} onSelectIssue={onSelectIssue} />)
    })
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.replay-preflight-gates button'))
    expect(buttons.map(button => [button.querySelector('strong')?.textContent, button.querySelector('small')?.textContent])).toEqual([
      ['Body is XML but Content-Type declares JSON', 'Actions › submit order'],
      ['Restore protected request body', 'Actions › submit order'],
      ['Restore protected request body', 'Actions › upload'],
    ])
    // Each row opens its gate's step, on its own code.
    act(() => buttons[1].click())
    expect(onSelectIssue).toHaveBeenLastCalledWith(twoCodes, expect.objectContaining({ code: 'restored_body_required' }))
    act(() => buttons[0].click())
    expect(onSelectIssue).toHaveBeenLastCalledWith(twoCodes, expect.objectContaining({ code: 'request_body_xml_json_content_type_mismatch' }))
    act(() => buttons[2].click())
    expect(onSelectIssue).toHaveBeenLastCalledWith(bodyReview, expect.objectContaining({ code: 'restored_body_required' }))
  })

  it('lists a value that blocks replay once, as Author Problems does, however many gates name it', () => {
    const consumer = (path: string, name: string) => `${path} "${name}": execution blocker missing_producer:c_body blocks replay until the imported behavior is resolved`
    const first = consumer('actions[0]', 'eval body')
    const nested = consumer('actions[1].control.steps[0]', 'eval again')
    const unproduced = 'actions[2] "store": execution blocker missing_producer:c_store blocks replay until the imported behavior is resolved'
    const preflight = {
      valid: false, action_count: 3, expected_action_count: 3, data_source_count: 0, parameter_count: 0,
      issues: [first, nested, unproduced],
      // c_store only sends placeholder text, so its gate stays a row of its own.
      blocking_variables: ['c_body'], placeholder_variables: ['c_store'], missing_variables: ['c_body', 'c_store'],
      checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: `${first}; ${nested}; ${unproduced}` }],
    }
    const onSelectIssue = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={preflight} onSelectIssue={onSelectIssue} />)
    })
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.replay-preflight-gates button'))
    expect(buttons.map(button => [button.querySelector('strong')?.textContent, button.querySelector('small')?.textContent])).toEqual([
      ['Unresolved value: c_body', 'Actions › eval body'],
      ['Missing value: c_store', 'Actions › store'],
    ])
    // The value's row opens the first gate that names it, as that value's problem.
    act(() => buttons[0].click())
    expect(onSelectIssue).toHaveBeenCalledWith(first, { code: 'missing_producer:c_body', message: first })

    // Titled by why it blocks, as Author Problems titles it.
    act(() => {
      root.render(<ReplayPreflightCard preflight={{ ...preflight, config_blocking_variables: ['c_body'], param_blocking_variables: [] }} />)
    })
    expect(Array.from(container.querySelectorAll('.replay-preflight-gates strong')).map(item => item.textContent)).toEqual(['Replace configuration reference: c_body', 'Missing value: c_store'])
  })

  it('lists the review codes for a value the draft now supplies once, as the save that re-evaluates them', () => {
    const consumer = (path: string, name: string) => `${path} "${name}": execution blocker missing_producer:sid blocks replay until the imported behavior is resolved`
    const first = consumer('actions[0]', 'confirm')
    const nested = consumer('actions[1].control.steps[0]', 'poll')
    const preflight = {
      valid: false, action_count: 2, expected_action_count: 2, data_source_count: 0, parameter_count: 0,
      issues: [first, nested],
      // Nothing is missing any more: the draft supplies sid.
      blocking_variables: [], placeholder_variables: [], missing_variables: [],
      checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: `${first}; ${nested}` }],
    }
    const onSelectIssue = vi.fn()
    act(() => {
      root.render(<ReplayPreflightCard preflight={preflight} onSelectIssue={onSelectIssue} />)
    })
    const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>('.replay-preflight-gates button'))
    expect(buttons.map(button => [button.querySelector('strong')?.textContent, button.querySelector('small')?.textContent])).toEqual([
      ['Save to update the import review for sid', 'Actions › confirm'],
    ])
    act(() => buttons[0].click())
    expect(onSelectIssue).toHaveBeenCalledWith(first, { code: 'missing_producer:sid', message: first })
  })

  it('keeps the review gates detail when no gate in the issues can be read', () => {
    act(() => {
      root.render(<ReplayPreflightCard preflight={{
        valid: false, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0,
        issues: ['actions[0].control.steps: action nesting exceeds 64 levels'],
        checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: 'actions[0].control.steps: action nesting exceeds 64 levels' }],
      }} />)
    })
    expect(container.querySelector('.replay-preflight-gates')).toBeNull()
    expect(container.textContent).toContain('action nesting exceeds 64 levels')
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
