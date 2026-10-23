// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TelemetryPolicySelect from './TelemetryPolicySelect'
import { TELEMETRY_POLICY_PROJECT_DEFAULT, TELEMETRY_POLICY_SAVE_READ_TIMEOUT_MS, telemetryPolicyIssue, telemetryPolicySaveIssue, telemetryPolicyUnusableReason } from './telemetryAcceptanceChoice'
import type { AnalysisPolicyRecord } from '../hooks/api/analysisPoliciesApi'

const list = vi.hoisted(() => vi.fn())
vi.mock('../hooks/api/analysisPoliciesApi', () => ({ analysisPoliciesApi: { list } }))

const policy = (id: string, name: string, definition: unknown = { version: 1 }): AnalysisPolicyRecord =>
  ({ id, project_id: 'project-a', kind: 'telemetry_acceptance_policy', name, revision: 1, definition, deleted: false, created_at: '' })

const policies = [policy('default-gate', 'Default gate', { version: 1, apply_by_default: true }), policy('strict-gate', 'Strict gate')]

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  list.mockReset()
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
const select = () => container.querySelector<HTMLSelectElement>('[data-testid="telemetry-policy-select"] select')!
const optionLabels = () => Array.from(select().options).map(option => `${option.value}=${option.textContent}`)

describe('TelemetryPolicySelect (TEL-B)', () => {
  it("offers the project's live policies, naming its default, and reports the choice", async () => {
    list.mockResolvedValue(policies)
    const onChange = vi.fn()
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="scenario" value="" onChange={onChange} />))
    await flush()
    expect(list).toHaveBeenCalledWith('project-a', 'telemetry_acceptance_policy', expect.any(AbortSignal))
    expect(optionLabels()).toEqual(['=Project default (Default gate)', 'default-gate=Default gate (default-gate)', 'strict-gate=Strict gate (strict-gate)'])
    expect(select().getAttribute('aria-describedby')).toBeTruthy()
    expect(document.getElementById(select().getAttribute('aria-describedby')!)?.textContent).toContain('including scheduled runs')
    expect(container.textContent).toContain('including scheduled runs and execution plans')
    await act(async () => {
      select().value = 'strict-gate'
      select().dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith('strict-gate')
  })

  it('keeps a choice the project no longer has visible and names it as unavailable', async () => {
    list.mockResolvedValue(policies)
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="scenario" value="gone-gate" onChange={vi.fn()} />))
    await flush()
    expect(select().value).toBe('gone-gate')
    expect(optionLabels()).toContain('gone-gate=gone-gate (no longer available)')
    const error = container.querySelector('.form-error')!
    expect(error.textContent).toContain('"gone-gate" no longer exists in this project')
    // Described by the select; the page error announces a blocked save.
    expect(error.getAttribute('role')).toBeNull()
    expect(select().getAttribute('aria-invalid')).toBe('true')
    expect(select().getAttribute('aria-describedby')).toContain(error.id)
  })

  it('keeps the current choice when the policies cannot be loaded', async () => {
    list.mockRejectedValue(new Error('offline'))
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="run" value="strict-gate" onChange={vi.fn()} />))
    await flush()
    expect(select().value).toBe('strict-gate')
    expect(container.querySelector('[role="status"]')?.textContent).toContain('could not be loaded; the current choice is kept')
    expect(container.querySelector('.form-error')).toBeNull()
  })

  it("lets a revised copy keep its source run's choice or name the project default", async () => {
    list.mockResolvedValue(policies)
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="run" value="" inheritedId="strict-gate" onChange={vi.fn()} />))
    await flush()
    expect(optionLabels().slice(0, 2)).toEqual(["=Keep the source run's choice (Strict gate)", `${TELEMETRY_POLICY_PROJECT_DEFAULT}=Project default (Default gate)`])
  })

  it("marks a revised copy's kept choice the project no longer has", async () => {
    list.mockResolvedValue(policies)
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="run" value="" inheritedId="gone-gate" onChange={vi.fn()} />))
    await flush()
    expect(select().options[0].textContent).toBe("Keep the source run's choice (gone-gate (no longer available))")
    expect(container.querySelector('.form-error')?.textContent).toContain(`The source run's telemetry acceptance policy "gone-gate" no longer exists`)
  })

  it("warns that runs will be refused when the scenario's stored choice is gone, since a save keeping it succeeds", async () => {
    list.mockResolvedValue(policies)
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="scenario" value="gone-gate" storedId="gone-gate" onChange={vi.fn()} />))
    await flush()
    // A warning, not an error: a save that keeps it succeeds.
    expect(container.querySelector('.form-error')).toBeNull()
    expect(container.querySelector('.settings-inline-warning')?.textContent).toBe(`The scenario's telemetry acceptance policy "gone-gate" no longer exists in this project: runs created from it are refused until you choose another or the project default.`)
    expect(select().getAttribute('aria-invalid')).toBeNull()
    expect(select().getAttribute('aria-describedby')).toContain(container.querySelector('.settings-inline-warning')!.id)
  })

  it('reloads the policies when asked', async () => {
    list.mockResolvedValue(policies)
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="scenario" value="" refreshKey={0} onChange={vi.fn()} />))
    await flush()
    await act(async () => root.render(<TelemetryPolicySelect projectId="project-a" subject="scenario" value="" refreshKey={1} onChange={vi.fn()} />))
    await flush()
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('reads nothing and claims no failure without a project', async () => {
    await act(async () => root.render(<TelemetryPolicySelect subject="scenario" value="" onChange={vi.fn()} />))
    await flush()
    expect(list).not.toHaveBeenCalled()
    expect(container.querySelector('[role="status"]')).toBeNull()
  })

  it('judges a choice only once the policies are known', () => {
    expect(telemetryPolicyIssue('gone-gate', null)).toBe('')
    expect(telemetryPolicyIssue('', policies)).toBe('')
    expect(telemetryPolicyIssue(TELEMETRY_POLICY_PROJECT_DEFAULT, policies)).toBe('')
    expect(telemetryPolicyIssue('strict-gate', policies)).toBe('')
    expect(telemetryPolicyIssue('gone-gate', policies)).toContain('no longer exists')
    expect(telemetryPolicyIssue('', policies, 'strict-gate')).toBe('')
    expect(telemetryPolicyIssue('', policies, 'gone-gate')).toContain("source run's")
    expect(telemetryPolicyIssue(TELEMETRY_POLICY_PROJECT_DEFAULT, policies, 'gone-gate')).toBe('')
  })

  it('judges a save against a fresh read, and blocks nothing when the read fails', async () => {
    list.mockResolvedValue(policies)
    expect(await telemetryPolicySaveIssue('project-a', 'gone-gate')).toContain('no longer exists')
    expect(await telemetryPolicySaveIssue('project-a', 'strict-gate')).toBe('')
    expect(await telemetryPolicySaveIssue('project-a', '', 'gone-gate')).toContain("source run's")
    list.mockClear()
    expect(await telemetryPolicySaveIssue('project-a', '')).toBe('')
    expect(await telemetryPolicySaveIssue('project-a', TELEMETRY_POLICY_PROJECT_DEFAULT, 'gone-gate')).toBe('')
    expect(list).not.toHaveBeenCalled()
    list.mockRejectedValue(new Error('offline'))
    expect(await telemetryPolicySaveIssue('project-a', 'gone-gate')).toBe('')
    // The scenario's stored choice is not judged, as the controller does not.
    list.mockClear()
    expect(await telemetryPolicySaveIssue('project-a', 'gone-gate', undefined, 'gone-gate')).toBe('')
    expect(list).not.toHaveBeenCalled()
  })

  it('blocks nothing when the save-time read does not answer in time', async () => {
    vi.useFakeTimers()
    try {
      list.mockImplementation((_project: string, _kind: string, signal?: AbortSignal) => new Promise((_resolve, reject) => {
        signal?.addEventListener('abort', () => reject(new Error('aborted')))
      }))
      const issue = telemetryPolicySaveIssue('project-a', 'gone-gate')
      await vi.advanceTimersByTimeAsync(TELEMETRY_POLICY_SAVE_READ_TIMEOUT_MS)
      expect(await issue).toBe('')
      // A closed screen ends the read at once.
      const closed = new AbortController()
      const ended = telemetryPolicySaveIssue('project-a', 'gone-gate', undefined, undefined, closed.signal)
      closed.abort()
      expect(await ended).toBe('')
    } finally {
      vi.useRealTimers()
    }
  })
})

const budget = (id: string, name: string, definition: unknown = { version: 1, sustained_window_seconds: 15 }): AnalysisPolicyRecord =>
  ({ id, project_id: 'project-a', kind: 'telemetry_policy', name, revision: 1, definition, deleted: false, created_at: '' })

// A project's default budget policy is pinned only: it never sets a run's budgets.
const budgets = [budget('house-budgets', 'House budgets', { version: 1, apply_by_default: true }), budget('tight-budgets', 'Tight budgets'), budget('slow-window', 'Slow window', { version: 1, sustained_window_seconds: 30 })]
const budgetSelect = () => container.querySelector<HTMLSelectElement>('[data-testid="telemetry-budget-policy-select"] select')!
const budgetLabels = () => Array.from(budgetSelect().options).map(option => `${option.value}=${option.textContent}${option.disabled ? ' [disabled]' : ''}`)

describe('TelemetryPolicySelect budget (TEL-B)', () => {
  it("offers the project's budget policies beside the system threshold settings, never a project default", async () => {
    list.mockResolvedValue(budgets)
    const onChange = vi.fn()
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="scenario" value="" onChange={onChange} />))
    await flush()
    expect(list).toHaveBeenCalledWith('project-a', 'telemetry_policy', expect.any(AbortSignal))
    expect(container.textContent).toContain('Telemetry budget policy')
    expect(budgetLabels()).toEqual([
      '=System threshold settings',
      'house-budgets=House budgets (house-budgets)',
      'tight-budgets=Tight budgets (tight-budgets)',
      "slow-window=Slow window (slow-window) (unusable: it sets a 30 s sustained window, and a run's live classification uses 15 s) [disabled]",
    ])
    expect(document.getElementById(budgetSelect().getAttribute('aria-describedby')!)?.textContent).toContain('takes its live telemetry budgets from this policy')
    await act(async () => {
      budgetSelect().value = 'tight-budgets'
      budgetSelect().dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith('tight-budgets')
  })

  it('names a chosen budget policy the project cannot use, or no longer has', async () => {
    list.mockResolvedValue(budgets)
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="run" value="slow-window" onChange={vi.fn()} />))
    await flush()
    expect(budgetSelect().value).toBe('slow-window')
    // The choice itself stays selectable so the issue can name it.
    expect(budgetSelect().selectedOptions[0].disabled).toBe(false)
    expect(container.querySelector('.form-error')?.textContent).toBe(`The chosen telemetry budget policy "slow-window" cannot set a run's budgets: it sets a 30 s sustained window, and a run's live classification uses 15 s; choose another or the system threshold settings.`)
    expect(budgetSelect().getAttribute('aria-invalid')).toBe('true')
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="run" value="gone-budgets" onChange={vi.fn()} />))
    await flush()
    expect(budgetLabels()).toContain('gone-budgets=gone-budgets (no longer available)')
    expect(container.querySelector('.form-error')?.textContent).toContain('"gone-budgets" no longer exists in this project; choose another or the system threshold settings.')
  })

  it("lets a revised copy keep its source run's budget choice or drop it for the system threshold settings", async () => {
    list.mockResolvedValue(budgets)
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="run" value="" inheritedId="gone-budgets" onChange={vi.fn()} />))
    await flush()
    expect(budgetLabels().slice(0, 2)).toEqual(["=Keep the source run's choice (gone-budgets (no longer available))", `${TELEMETRY_POLICY_PROJECT_DEFAULT}=System threshold settings`])
    expect(container.querySelector('.form-error')?.textContent).toContain(`The source run's telemetry budget policy "gone-budgets" no longer exists`)
  })

  it("warns that runs will be refused when the scenario's stored budget choice is gone", async () => {
    list.mockResolvedValue(budgets)
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="scenario" value="gone-budgets" storedId="gone-budgets" onChange={vi.fn()} />))
    await flush()
    expect(container.querySelector('.form-error')).toBeNull()
    expect(container.querySelector('.settings-inline-warning')?.textContent).toBe(`The scenario's telemetry budget policy "gone-budgets" no longer exists in this project: runs created from it are refused until you choose another or the system threshold settings.`)
    expect(budgetSelect().getAttribute('aria-invalid')).toBeNull()
  })

  it('keeps the current budget choice when the budget policies cannot be loaded', async () => {
    list.mockRejectedValue(new Error('offline'))
    await act(async () => root.render(<TelemetryPolicySelect kind="budget" projectId="project-a" subject="run" value="tight-budgets" onChange={vi.fn()} />))
    await flush()
    expect(budgetSelect().value).toBe('tight-budgets')
    expect(container.querySelector('[role="status"]')?.textContent).toBe("The project's telemetry budget policies could not be loaded; the current choice is kept.")
  })

  it('judges a budget choice by kind, including the sustained window', async () => {
    expect(telemetryPolicyUnusableReason(budgets[2], 'budget')).toContain('30 s sustained window')
    expect(telemetryPolicyUnusableReason(budgets[2])).toBe('')
    expect(telemetryPolicyUnusableReason(budgets[0], 'budget')).toBe('')
    expect(telemetryPolicyIssue('tight-budgets', budgets, undefined, 'budget')).toBe('')
    expect(telemetryPolicyIssue('slow-window', budgets, undefined, 'budget')).toContain('cannot set a run\'s budgets')
    expect(telemetryPolicyIssue('', budgets, 'slow-window', 'budget')).toContain("source run's telemetry budget policy")
    list.mockResolvedValue(budgets)
    expect(await telemetryPolicySaveIssue('project-a', 'slow-window', undefined, undefined, undefined, 'budget')).toContain('cannot set a run')
    expect(list).toHaveBeenLastCalledWith('project-a', 'telemetry_policy', expect.any(AbortSignal))
    list.mockClear()
    expect(await telemetryPolicySaveIssue('project-a', 'slow-window', undefined, 'slow-window', undefined, 'budget')).toBe('')
    expect(await telemetryPolicySaveIssue('project-a', TELEMETRY_POLICY_PROJECT_DEFAULT, 'slow-window', undefined, undefined, 'budget')).toBe('')
    expect(list).not.toHaveBeenCalled()
  })
})
