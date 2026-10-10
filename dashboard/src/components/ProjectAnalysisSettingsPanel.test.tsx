// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ProjectAnalysisSettingsResponse } from '../types/rca'
import ProjectAnalysisSettingsPanel from './ProjectAnalysisSettingsPanel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request: mocks.request }))

const off = { root_cause: { enabled: false, present: false, auto_analyze: false, auto_enrich: false }, business_keys: { enabled: false } }
const response = (changes: Partial<ProjectAnalysisSettingsResponse> = {}): ProjectAnalysisSettingsResponse => ({
  project_id: 'p-1', settings: { ...off, revision: 0 }, forced_by_env: off, effective: off, ...changes,
})

describe('ProjectAnalysisSettingsPanel', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.request.mockReset()
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })
  const checkbox = (label: string) => Array.from(container.querySelectorAll('label')).find(l => l.textContent?.includes(label))!.querySelector('input') as HTMLInputElement
  const button = (name: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent === name) as HTMLButtonElement | undefined

  it('saves the changed flags against the revision it read', async () => {
    mocks.request.mockResolvedValueOnce(response())
    const onSaved = vi.fn()
    await act(async () => root.render(<ProjectAnalysisSettingsPanel projectId="p-1" projectName="Checkout" canEdit onSaved={onSaved} />))
    expect(mocks.request).toHaveBeenCalledWith('/projects/p-1/analysis-settings', expect.objectContaining({ signal: expect.anything() }))
    expect(container.textContent).toContain('Not changed yet')
    // Automatic analysis waits until analyses are computed.
    expect(checkbox('Analyze every archived run automatically').disabled).toBe(true)
    expect(button('Save analysis settings')!.disabled).toBe(true)
    await act(async () => checkbox('Compute root-cause analyses').click())
    expect(checkbox('Analyze every archived run automatically').disabled).toBe(false)
    await act(async () => checkbox('Analyze every archived run automatically').click())
    const saved = { ...off, root_cause: { ...off.root_cause, enabled: true, auto_analyze: true }, revision: 1, updated_by: 'pat', updated_at: '2026-10-09T12:00:00Z' }
    mocks.request.mockResolvedValueOnce(response({ settings: saved, effective: saved }))
    await act(async () => button('Save analysis settings')!.click())
    expect(mocks.request).toHaveBeenLastCalledWith('/projects/p-1/analysis-settings', { method: 'PUT', body: JSON.stringify({ expected_revision: 0, root_cause: saved.root_cause, business_keys: off.business_keys }) })
    expect(container.querySelector('[role="status"]')!.textContent).toBe('Saved as revision 1.')
    expect(container.textContent).toContain('Revision 1 by pat')
    // The page's copy of the project takes the saved settings.
    expect(onSaved).toHaveBeenCalledWith(saved)
  })

  it('explains a save that lost to a newer one', async () => {
    mocks.request.mockResolvedValueOnce(response())
    await act(async () => root.render(<ProjectAnalysisSettingsPanel projectId="p-1" canEdit />))
    await act(async () => checkbox('Allow business-key lookups').click())
    mocks.request.mockRejectedValueOnce(Object.assign(new Error('conflict'), { status: 409 }))
    await act(async () => button('Save analysis settings')!.click())
    expect(container.querySelector('[role="alert"]')!.textContent).toContain('These settings changed since you opened them')
  })

  it('shows deployment-forced flags as on and read-only, and everything read-only to others', async () => {
    const forced = { ...off, root_cause: { ...off.root_cause, present: true } }
    mocks.request.mockResolvedValueOnce(response({ forced_by_env: forced, effective: forced }))
    await act(async () => root.render(<ProjectAnalysisSettingsPanel projectId="p-1" canEdit={false} />))
    const present = checkbox('Show root cause in summaries and reports')
    expect(present.checked).toBe(true)
    expect(present.disabled).toBe(true)
    expect(container.textContent).toContain('On for this deployment')
    expect(checkbox('Compute root-cause analyses').disabled).toBe(true)
    expect(button('Save analysis settings')).toBeUndefined()
    expect(container.textContent).toContain('A project administrator changes these settings.')
  })
})
