// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserSettingsPage from './UserSettingsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
describe('UserSettingsPage', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { localStorage.clear(); host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); host.remove(); localStorage.clear() })
  it('falls back to appearance for administrative destinations and exposes only personal navigation', () => {
    const change = vi.fn()
    act(() => root.render(<UserSettingsPage theme="dark" onThemeChange={change} destinationId="authentication" onDestinationChange={vi.fn()} />))
    expect([...host.querySelectorAll('nav button')].map(button => button.textContent)).toEqual(['Appearance', 'Tables & editor', 'Notifications', 'Account'])
    const select = host.querySelector<HTMLSelectElement>('#personal-theme')!
    expect(select.labels?.[0]?.textContent).toBe('Dashboard Theme')
    act(() => { select.value = 'solarized-dark'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(change).toHaveBeenCalledWith('solarized-dark')
    expect(host.textContent).not.toContain('Authentication')
    expect(host.querySelector('[role=note]')?.textContent).toContain('requires administrator access')
    // Same non-collapsible section list as administrator Settings, and the top bar's theme names.
    expect([...host.querySelectorAll('button')].some(button => /Collapse|Settings navigation/.test(button.textContent || '') || /Settings navigation/.test(button.getAttribute('aria-label') || ''))).toBe(false)
    expect([...select.options].map(option => option.textContent)).toEqual(['System (Auto)', 'Dark Theme', 'Light Theme', 'Solarized Dark', 'Solarized Light', 'Monokai Dimmed'])
  })
  it('links sections and presents the signed-in account without editable identity fields', () => {
    const navigate = vi.fn()
    act(() => root.render(<UserSettingsPage theme="dark" onThemeChange={vi.fn()} destinationId="personal-account" onDestinationChange={navigate} authStatus={{ enabled: true, mode: 'local', authenticated: true, user: { subject: 'local:operator', display_name: 'Operator', email: 'operator@example.test', global_roles: [] } }} />))
    expect(host.querySelector('[aria-label="Your account"]')?.textContent).toContain('operator@example.test')
    expect(host.querySelector('input')).toBeNull()
    act(() => host.querySelector<HTMLButtonElement>('nav button')!.click())
    expect(navigate).toHaveBeenCalledWith('personal-appearance')
  })
})
