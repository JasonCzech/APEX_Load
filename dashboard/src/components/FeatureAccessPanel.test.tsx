// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import FeatureAccessPanel from './FeatureAccessPanel'
import type { DomainFeaturePolicy } from '../types/domainFeatures'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const repositoryApiMock = {
  getDomainFeatures: vi.fn(),
  updateDomainFeatures: vi.fn(),
}

vi.mock('../hooks/api/useRepositoryApi', () => ({
  useRepositoryApi: () => repositoryApiMock,
}))

const policy: DomainFeaturePolicy = {
  domain_id: 'domain-a',
  catalog_version: '2026-08-27.1',
  revision: 'revision-1',
  disabled_feature_ids: [],
  categories: [
    { id: 'script_builder', name: 'Script builder Workflows', description: 'Workflows' },
    { id: 'script_import', name: 'Imported Collections & Scripts', description: 'Sources' },
  ],
  features: [
    { id: 'script_builder.imports', name: 'Imported Collections & Scripts', category: 'script_builder', configured_enabled: true, effective_enabled: true },
    { id: 'script_import.postman', name: 'Postman', category: 'script_import', parent_id: 'script_builder.imports', configured_enabled: true, effective_enabled: true },
  ],
}

let container: HTMLElement
let root: Root

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

beforeEach(() => {
  repositoryApiMock.getDomainFeatures.mockReset().mockResolvedValue(policy)
  repositoryApiMock.updateDomainFeatures.mockReset().mockImplementation(async (_domainId, disabledIds) => ({
    ...policy,
    revision: 'revision-2',
    disabled_feature_ids: disabledIds,
  }))
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
})

describe('FeatureAccessPanel', () => {

  it('selects and loads a domain that arrives after mount', async () => {
    await act(async () => {
      root.render(<FeatureAccessPanel domains={[]} />)
    })
    expect(repositoryApiMock.getDomainFeatures).not.toHaveBeenCalled()

    await act(async () => {
      root.render(<FeatureAccessPanel domains={[{ id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }]} />)
    })
    await flush()

    expect(repositoryApiMock.getDomainFeatures).toHaveBeenCalledWith('domain-a')
    expect(container.textContent).toContain('Postman')
  })

  it('remembers child configuration when a parent is disabled and saves with CAS', async () => {
    await act(async () => {
      root.render(<FeatureAccessPanel domains={[{ id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }]} />)
    })
    await flush()

    const toggles = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    expect(toggles).toHaveLength(2)
    await act(async () => toggles[0].click())

    expect(toggles[0].checked).toBe(false)
    expect(toggles[1].checked).toBe(true)
    expect(container.textContent).toContain('Disabled by parent')

    const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save domain feature policy')
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flush()

    expect(repositoryApiMock.updateDomainFeatures).toHaveBeenCalledWith(
      'domain-a',
      ['script_builder.imports'],
      'revision-1',
    )
  })

  it('supports search and effective-state filtering', async () => {
    repositoryApiMock.getDomainFeatures.mockResolvedValue({
      ...policy,
      disabled_feature_ids: ['script_import.postman'],
      features: policy.features.map(feature => feature.id === 'script_import.postman'
        ? { ...feature, configured_enabled: false, effective_enabled: false }
        : feature),
    })
    await act(async () => {
      root.render(<FeatureAccessPanel domains={[{ id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }]} />)
    })
    await flush()

    const search = container.querySelector<HTMLInputElement>('input[aria-label="Search domain features"]')
    await act(async () => {
      if (search) {
        // Set through the native setter so React's value tracker sees the change.
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(search, 'postman')
        search.dispatchEvent(new Event('input', { bubbles: true }))
      }
    })
    const featureRows = () => Array.from(container.querySelectorAll('table.access-data-table tbody tr')).map(row => row.querySelector('.feature-access-name')?.textContent)
    expect(featureRows()).toEqual(['Postman'])

    const filter = container.querySelector<HTMLSelectElement>('select[aria-label="Filter domain features"]')
    await act(async () => {
      if (filter) {
        filter.value = 'enabled'
        filter.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(featureRows()).toEqual([])
    expect(container.textContent).toContain('No features match these filters')
  })

  it('presents features as one audit-style table and filters by category', async () => {
    await act(async () => {
      root.render(<FeatureAccessPanel domains={[{ id: 'domain-a', name: 'Domain A', created_at: '', updated_at: '' }]} />)
    })
    await flush()

    const table = container.querySelector('.access-table-wrap > table.access-data-table')
    expect(table).not.toBeNull()
    expect(Array.from(table!.querySelectorAll('thead th')).map(th => th.textContent)).toEqual(['Feature', 'Category', 'Key', 'Depends on', 'Status', 'Allow'])
    const postman = Array.from(table!.querySelectorAll('tbody tr')).find(row => row.textContent?.includes('script_import.postman'))
    expect(Array.from(postman!.querySelectorAll('td')).map(cell => cell.textContent)).toEqual(['Postman', 'Imported Collections & Scripts', 'script_import.postman', 'Imported Collections & Scripts', 'Enabled', ''])
    expect(postman!.querySelector('input[type="checkbox"]')?.getAttribute('aria-label')).toBe('Allow Postman')

    const category = container.querySelector<HTMLSelectElement>('select[aria-label="Filter feature category"]')
    await act(async () => {
      if (category) {
        category.value = 'script_builder'
        category.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(Array.from(table!.querySelectorAll('tbody tr')).map(row => row.querySelector('.feature-access-name')?.textContent)).toEqual(['Imported Collections & Scripts'])
  })
})
