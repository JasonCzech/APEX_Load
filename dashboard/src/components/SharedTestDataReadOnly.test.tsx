// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SharedTestDataReadOnly from './SharedTestDataReadOnly'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({ listDataPools: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

describe('SharedTestDataReadOnly', () => {
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

  it('lists the project data pools read-only and explains who can manage them', async () => {
    apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'p1', name: 'Customers', schema: { email: 'string' }, created_at: '', updated_at: '' }, { id: 'p2', name: 'Old', created_at: '', updated_at: '', archived_at: '2026-01-01T00:00:00Z' }], count: 2 })
    await act(async () => root.render(<SharedTestDataReadOnly projectId="project-1" projectName="Retail" />))
    await act(async () => { await Promise.resolve() })
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')
    expect(container.textContent).toContain('requires the project admin role')
    expect(container.textContent).toContain('Customers')
    expect(container.textContent).toContain('email')
    expect(container.textContent).not.toContain('Old')
    expect(container.textContent).not.toContain('Administrator settings')
  })

  it('treats a project whose pools are all archived as having none', async () => {
    apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'p2', name: 'Old', created_at: '', updated_at: '', archived_at: '2026-01-01T00:00:00Z' }], count: 1 })
    await act(async () => root.render(<SharedTestDataReadOnly projectId="project-1" />))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('No shared data pools exist in this project yet.')
    expect(container.querySelector('table')).toBeNull()
  })

  it('shows a load failure as an alert', async () => {
    apiMock.listDataPools.mockRejectedValue(new Error('backend unavailable'))
    await act(async () => root.render(<SharedTestDataReadOnly projectId="project-1" />))
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[role="alert"]')?.textContent).toBeTruthy()
  })
})
