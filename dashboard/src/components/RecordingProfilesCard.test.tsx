// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RecordingProfilesCard from './RecordingProfilesCard'
import { NavigationGuardRegisterContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getRecordingProfiles: vi.fn(),
  saveRecordingProfiles: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

const rules = [
  { id: 'json.csrf_token', family: 'json', title: 'CSRF token', description: '', base_confidence: 0.9 },
  { id: 'cookie.session', family: 'cookie', title: 'Session cookie', description: '', base_confidence: 0.8 },
]

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setValue(element: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
  element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}

function button(container: HTMLElement, text: string) {
  return Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent?.trim() === text)!
}

describe('RecordingProfilesCard', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getRecordingProfiles.mockReset()
    apiMock.saveRecordingProfiles.mockReset()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('renders nothing without a project', async () => {
    await act(async () => root.render(<RecordingProfilesCard selectedId="" onSelect={vi.fn()} />))
    expect(container.innerHTML).toBe('')
    expect(apiMock.getRecordingProfiles).not.toHaveBeenCalled()
  })

  it('creates a profile grouped like VuGen and saves it with the optimistic token', async () => {
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [], rules, updated_at: '2026-10-09T01:00:00Z' })
    apiMock.saveRecordingProfiles.mockImplementation(async (_project: string, profiles: unknown[]) => ({ profiles, rules, updated_at: '2026-10-09T02:00:00Z' }))
    const onSelect = vi.fn()
    await act(async () => root.render(<RecordingProfilesCard projectId="proj-1" selectedId="" onSelect={onSelect} />))
    await flush()

    await act(async () => button(container, 'Manage profiles').click())
    await act(async () => button(container, 'New profile').click())
    const legends = Array.from(container.querySelectorAll('legend')).map(legend => legend.textContent)
    expect(legends).toEqual(['General', 'Code generation', 'HTTP properties: headers', 'Recording filters', 'Correlations: configuration', 'Correlations: project rules'])

    await act(async () => setValue(container.querySelector('input[aria-label="Profile name"]')!, 'Checkout'))
    await act(async () => setValue(container.querySelector('select[aria-label="Profile HTTP script level"]')!, 'html'))
    await act(async () => setValue(container.querySelector('input[aria-label="Think-time threshold (ms)"]')!, '1500'))
    await act(async () => setValue(container.querySelector('input[aria-label="Never record headers"]')!, 'X-Debug, X-Trace'))
    await act(async () => setValue(container.querySelector('input[aria-label="Exclude content types"]')!, 'image/*'))
    const cookieFamily = Array.from(container.querySelectorAll('[aria-label="Automatic rule families"] label')).find(label => label.textContent === 'cookie')!.querySelector('input')!
    await act(async () => cookieFamily.click())
    await act(async () => button(container, 'Add rule').click())
    await act(async () => setValue(container.querySelector('input[aria-label="Rule 1 parameter name"]')!, 'OrderID'))
    await act(async () => setValue(container.querySelector('input[aria-label="Rule 1 left boundary"]')!, 'order='))
    await act(async () => setValue(container.querySelector('input[aria-label="Rule 1 right boundary"]')!, '&'))
    await act(async () => button(container, 'Save profiles').click())
    await flush()

    expect(apiMock.saveRecordingProfiles).toHaveBeenCalledWith('proj-1', [{
      id: 'profile-1',
      name: 'Checkout',
      http_script_level: 'html',
      think_time_threshold_ms: 1500,
      drop_headers: ['X-Debug', 'X-Trace'],
      exclude_content_types: ['image/*'],
      correlation: { disabled_rules: ['cookie'], user_rules: [{ name: 'OrderID', type: 'boundary', left: 'order=', right: '&' }] },
    }], '2026-10-09T01:00:00Z')
    expect(container.querySelector('[data-testid="recording-profile-editor"]')).toBeNull()
    expect((container.querySelector('select[aria-label="Recording profile"]') as HTMLSelectElement).textContent).toContain('Checkout')
  })

  it('sets the background request paths, or keeps every request in line', async () => {
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [], rules, updated_at: '2026-10-09T01:00:00Z' })
    apiMock.saveRecordingProfiles.mockImplementation(async (_project: string, profiles: unknown[]) => ({ profiles, rules, updated_at: '2026-10-09T02:00:00Z' }))
    await act(async () => root.render(<RecordingProfilesCard projectId="proj-1" selectedId="" onSelect={vi.fn()} />))
    await flush()
    await act(async () => button(container, 'Manage profiles').click())
    await act(async () => button(container, 'New profile').click())
    await act(async () => setValue(container.querySelector('input[aria-label="Profile name"]')!, 'Mobile'))

    const paths = container.querySelector('input[aria-label="Background request paths"]') as HTMLInputElement
    expect(paths.placeholder).toContain('nebulaLogger')
    await act(async () => setValue(paths, 'telemetry/events, nebulaLogger'))
    await act(async () => button(container, 'Save profiles').click())
    await flush()
    expect(apiMock.saveRecordingProfiles.mock.calls[0][1][0].background_requests).toEqual(['telemetry/events', 'nebulaLogger'])

    await act(async () => button(container, 'Manage profiles').click())
    const toggle = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('as background requests'))!.querySelector('input') as HTMLInputElement
    expect(toggle.checked).toBe(true)
    await act(async () => toggle.click())
    expect(container.querySelector('input[aria-label="Background request paths"]')).toBeNull()
    await act(async () => button(container, 'Save profiles').click())
    await flush()
    expect(apiMock.saveRecordingProfiles.mock.calls[1][1][0].background_requests).toEqual([])
  })

  it('selects a profile and shows a rejected save', async () => {
    const profile = { id: 'default', name: 'Default', http_script_level: 'url' as const }
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [profile], rules, updated_at: '2026-10-09T01:00:00Z' })
    apiMock.saveRecordingProfiles.mockRejectedValue(new Error('recording_profiles[0].name must be 1-128 characters'))
    const onSelect = vi.fn()
    await act(async () => root.render(<RecordingProfilesCard projectId="proj-1" selectedId="" onSelect={onSelect} />))
    await flush()
    await act(async () => setValue(container.querySelector('select[aria-label="Recording profile"]')!, 'default'))
    expect(onSelect).toHaveBeenLastCalledWith('default', profile)

    await act(async () => button(container, 'Manage profiles').click())
    await act(async () => setValue(container.querySelector('input[aria-label="Profile name"]')!, ''))
    await act(async () => button(container, 'Save profiles').click())
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('name must be')
    expect(container.querySelector('[data-testid="recording-profile-editor"]')).not.toBeNull()
  })

  it('asks before leaving unsaved profile edits, and can save them from the prompt', async () => {
    const profile = { id: 'default', name: 'Default' }
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [profile, { id: 'api', name: 'API' }], rules, updated_at: '2026-10-09T01:00:00Z' })
    apiMock.saveRecordingProfiles.mockImplementation(async (_project: string, profiles: unknown[]) => ({ profiles, rules, updated_at: '2026-10-09T02:00:00Z' }))
    const registrations: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const register = (guard: NavigationGuard, dirty: boolean) => {
      registrations.push({ guard, dirty })
      return () => undefined
    }
    await act(async () => root.render(
      <NavigationGuardRegisterContext.Provider value={register}>
        <RecordingProfilesCard projectId="proj-1" selectedId="" onSelect={vi.fn()} />
      </NavigationGuardRegisterContext.Provider>,
    ))
    await flush()
    await act(async () => button(container, 'Manage profiles').click())
    // Opening the editor changes nothing yet.
    expect(registrations[registrations.length - 1]?.dirty).toBe(false)
    // The profiles are a segmented control; the selected one is pressed.
    const tabs = container.querySelector('[role="group"][aria-label="Recording profiles"]')!
    expect(Array.from(tabs.querySelectorAll('button')).map(tab => [tab.textContent, tab.getAttribute('aria-pressed')])).toEqual([['Default', 'true'], ['API', 'false']])

    await act(async () => setValue(container.querySelector('input[aria-label="Profile name"]')!, 'Checkout'))
    const latest = registrations[registrations.length - 1]!
    expect(latest.dirty).toBe(true)
    expect(latest.guard.isDirty()).toBe(true)
    let saved = false
    await act(async () => { saved = await latest.guard.save() })
    expect(saved).toBe(true)
    expect(apiMock.saveRecordingProfiles).toHaveBeenCalledWith('proj-1', [{ id: 'default', name: 'Checkout' }, { id: 'api', name: 'API' }], '2026-10-09T01:00:00Z')
    expect(registrations[registrations.length - 1]?.dirty).toBe(false)
  })

  it('clears a selection the project no longer has', async () => {
    apiMock.getRecordingProfiles.mockResolvedValue({ profiles: [], rules, updated_at: '' })
    const onSelect = vi.fn()
    await act(async () => root.render(<RecordingProfilesCard projectId="proj-1" selectedId="gone" onSelect={onSelect} />))
    await flush()
    expect(onSelect).toHaveBeenCalledWith('', undefined)
  })
})
