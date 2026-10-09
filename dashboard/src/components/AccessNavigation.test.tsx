// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AccessNavigation from './AccessNavigation'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const EXPECTED = ['Users', 'Service accounts', 'Activity', 'Authentication', 'Domain feature policy', 'Import credential policy', 'Project redaction rules', 'Project secrets', 'Governance audit']

describe('AccessNavigation', () => {
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

  const entries = () => Array.from(container.querySelectorAll('nav[aria-label="Access administration"] :is(a, button)'))
  const labels = () => entries().map(entry => entry.querySelector('span')?.textContent)
  const current = () => entries().filter(entry => entry.getAttribute('aria-current') === 'page').map(entry => entry.querySelector('span')?.textContent)

  it('keeps the same entries in the same order whichever section is current', async () => {
    await act(async () => root.render(<AccessNavigation currentView="users" />))
    expect(labels()).toEqual(EXPECTED)
    expect(current()).toEqual(['Users'])

    await act(async () => root.render(<AccessNavigation currentSetting="audit" />))
    expect(labels()).toEqual(EXPECTED)
    expect(current()).toEqual(['Governance audit'])

    await act(async () => root.render(<AccessNavigation currentView="create-account" />))
    expect(labels()).toEqual(EXPECTED)
    expect(current()).toEqual(['Service accounts'])

    await act(async () => root.render(<AccessNavigation currentView="review" />))
    expect(current()).toEqual(['Activity'])
  })

  it('links identity views from policy pages and switches them in place on the Access page', async () => {
    await act(async () => root.render(<AccessNavigation currentSetting="redaction" />))
    const users = entries().find(entry => entry.textContent?.startsWith('Users'))
    expect(users?.tagName).toBe('A')
    expect(users?.getAttribute('href')).toContain('accessView=users')

    const onSelectView = vi.fn()
    await act(async () => root.render(<AccessNavigation currentView="users" onSelectView={onSelectView} counts={{ users: 2, accounts: 1, reviewAttention: 3 }} />))
    // Access review lives under Activity; its attention count shows on the Activity entry.
    const activity = entries().find(entry => entry.textContent?.startsWith('Activity'))
    expect(activity?.tagName).toBe('BUTTON')
    const badge = activity?.querySelector('.access-attention-badge')
    expect(badge?.querySelector('[aria-hidden="true"]')?.textContent).toBe('3')
    expect(badge?.querySelector('.sr-only')?.textContent).toBe('3 access review items need attention')
    // Activity shows no row count: loaded pages are not a total.
    expect(activity?.querySelector('strong')).toBeNull()
    await act(async () => activity?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onSelectView).toHaveBeenCalledWith('events')
    expect(entries().find(entry => entry.textContent?.startsWith('Authentication'))?.tagName).toBe('A')
  })
})
