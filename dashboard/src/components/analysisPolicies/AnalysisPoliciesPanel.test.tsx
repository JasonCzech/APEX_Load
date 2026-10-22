// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../hooks/apiClient'
import { NavigationGuardRegisterContext, NavigationGuardRunnerContext, type NavigationGuard } from '../../hooks/NavigationGuardProvider'
import AnalysisPoliciesPanel from './AnalysisPoliciesPanel'

const api = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn() }))
vi.mock('../../hooks/api/analysisPoliciesApi', () => ({ analysisPoliciesApi: api }))
const confirm = vi.hoisted(() => vi.fn())
vi.mock('../ux/confirmAction', () => ({ confirmAction: confirm }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const browserPolicy = {
  id: 'policy-1', project_id: 'p1', kind: 'browser_policy', name: 'Vitals', revision: 3, deleted: false, created_at: '2026-10-09T12:00:00Z',
  definition: { version: 1, apply_by_default: true, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }], baseline: { mode: 'latest_compatible' } },
}

let container: HTMLDivElement
let root: Root
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })
const button = (label: string) => Array.from(container.querySelectorAll('button')).find(item => item.textContent === label)
const click = async (label: string) => { await act(async () => { button(label)!.dispatchEvent(new MouseEvent('click', { bubbles: true })) }); await flush() }
const input = (label: string) => container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!
async function type(element: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

beforeEach(() => {
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
  for (const fn of Object.values(api)) fn.mockReset()
  confirm.mockReset()
  api.list.mockResolvedValue([browserPolicy])
})
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

describe('AnalysisPoliciesPanel (BI-4)', () => {
  it('lists a project\'s policies with their revision and default', async () => {
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit />))
    await flush()
    expect(api.list).toHaveBeenCalledWith('p1', 'browser_policy', expect.anything())
    const row = container.querySelector('tbody tr')!
    expect(row.textContent).toContain('Vitals')
    expect(row.textContent).toContain('3')
    expect(row.textContent).toContain('Project default')
  })

  it('saves an edit at the revision it was read at, and offers the current revision on a conflict', async () => {
    api.update.mockRejectedValueOnce(new ApiError('analysis policy revision changed', 409))
    api.get.mockResolvedValue({ ...browserPolicy, revision: 4, name: 'Vitals v4' })
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit />))
    await flush()
    await click('Edit')
    await type(input('LCP weight'), '7')
    await act(async () => { container.querySelector('form.analysis-policy-editor')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(api.update).toHaveBeenCalledWith('p1', 'policy-1', 3, expect.objectContaining({ kind: 'browser_policy', name: 'Vitals', definition: expect.objectContaining({ metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 7 }] }) }))
    expect(container.textContent).toContain('This policy changed since you opened it')
    await click('Load the current revision')
    expect(api.get).toHaveBeenCalledWith('p1', 'policy-1')
    expect(container.textContent).toContain('Edit Vitals v4 (revision 4)')
  })

  it('shows a refusal that is not a revision change as it is, on save and on delete', async () => {
    api.update.mockRejectedValueOnce(new ApiError('another browser_policy policy already applies by default (Other)', 409))
    api.remove.mockRejectedValueOnce(new ApiError('analysis policy limit reached', 409))
    api.get.mockResolvedValue(browserPolicy)
    confirm.mockResolvedValue(true)
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit />))
    await flush()
    await click('Edit')
    await act(async () => { container.querySelector('form.analysis-policy-editor')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(api.get).toHaveBeenCalledWith('p1', 'policy-1')
    expect(container.textContent).not.toContain('This policy changed since you opened it')
    expect(container.textContent).toContain('another browser_policy policy already applies by default (Other)')
    await click('Cancel')
    await click('Delete')
    expect(container.textContent).not.toContain('The policy changed since the list was loaded')
    expect(container.textContent).toContain('analysis policy limit reached')
  })

  it('guards unsaved edits: leaving, another policy or another kind asks first', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    const guarded = vi.fn()
    await act(async () => root.render(
      <NavigationGuardRegisterContext.Provider value={(guard, dirty) => { registration = { guard, dirty }; return () => undefined }}>
        <NavigationGuardRunnerContext.Provider value={{ runGuarded: (proceed: () => void) => { guarded(); if (!registration?.dirty) proceed() } } as never}>
          <AnalysisPoliciesPanel projectId="p1" canEdit />
        </NavigationGuardRunnerContext.Provider>
      </NavigationGuardRegisterContext.Provider>,
    ))
    await flush()
    await click('Edit')
    expect(registration?.dirty).toBe(false)
    await type(input('LCP weight'), '7')
    expect(registration?.dirty).toBe(true)
    // Switching kind goes through the guard, which keeps the edit while dirty.
    await click('Journey policies')
    expect(guarded).toHaveBeenCalled()
    expect(container.querySelector('form.analysis-policy-editor')).not.toBeNull()
    // Discarding from the guard closes the editor.
    await act(async () => { registration?.guard.discard() })
    expect(container.querySelector('form.analysis-policy-editor')).toBeNull()
  })

  it('creates a journey policy and refuses a draft the controller would refuse, before sending it', async () => {
    api.list.mockResolvedValue([])
    api.create.mockResolvedValue({})
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit />))
    await flush()
    await click('Journey policies')
    await click('New journey policy')
    await act(async () => { container.querySelector('form.analysis-policy-editor')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(container.textContent).toContain('Name the policy.')
    expect(api.create).not.toHaveBeenCalled()
    await type(container.querySelector<HTMLInputElement>('form.analysis-policy-editor input.settings-input')!, 'Shop')
    await type(input('Journey 1 name'), 'Checkout')
    await type(input('Journey 1 start'), 'Cart')
    await type(input('Journey 1 success'), 'Confirm')
    await act(async () => { container.querySelector('form.analysis-policy-editor')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(api.create).toHaveBeenCalledWith('p1', { kind: 'journey_policy', name: 'Shop', definition: { version: 1, journeys: [{ id: 'journey-1', name: 'Checkout', start: { transaction: 'Cart' }, success: { transaction: 'Confirm' }, weight: 1 }] } })
  })

  it('lets a viewer read past revisions but not edit or delete', async () => {
    api.get.mockResolvedValueOnce({ ...browserPolicy, revision: 1 })
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit={false} />))
    await flush()
    expect(button('Edit')).toBeUndefined()
    expect(button('Delete')).toBeUndefined()
    await click('View a revision')
    await type(container.querySelector<HTMLInputElement>('section[aria-label="Policy revision"] input')!, '1')
    await act(async () => { container.querySelector('section[aria-label="Policy revision"] form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(api.get).toHaveBeenCalledWith('p1', 'policy-1', 1)
    expect(container.textContent).toContain('Revision 1')
    expect(container.textContent).toContain('Read-only')
  })

  it('deletes at the listed revision only after confirmation', async () => {
    confirm.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    api.remove.mockResolvedValue({})
    await act(async () => root.render(<AnalysisPoliciesPanel projectId="p1" canEdit />))
    await flush()
    await click('Delete')
    expect(api.remove).not.toHaveBeenCalled()
    await click('Delete')
    expect(api.remove).toHaveBeenCalledWith('p1', 'policy-1', 3)
  })
})
