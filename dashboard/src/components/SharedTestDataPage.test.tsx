// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Project } from '../types'
import SharedTestDataPage from './SharedTestDataPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({ listDataPools: vi.fn(), getDataPool: vi.fn(), createDataPool: vi.fn(), exportDataPoolRows: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

const project = { id: 'project-1', name: 'Retail' } as Project
const pool = { id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['email'], created_at: '', updated_at: '2026-10-01T00:00:00Z' }

function setInput(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('SharedTestDataPage', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    vi.clearAllMocks()
    window.location.hash = '#/repository?setting=test-data'
    apiMock.listDataPools.mockResolvedValue({ data_pools: [pool], count: 1 })
    apiMock.getDataPool.mockResolvedValue({ data_pool: pool, rows: [{ id: 'r1', pool_id: 'pool-1', data: { email: 'a@example.test' }, version: 1, status: 'active', created_at: '', updated_at: '' }], total: 1 })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const render = async (canManage: boolean, activeProject: Project | null = project) => {
    await act(async () => root.render(<SharedTestDataPage activeProject={activeProject} canManage={canManage} />))
    for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve() })
  }
  const button = (text: string) => [...container.querySelectorAll('button')].find(item => item.textContent?.trim() === text)

  it('lets an editor create a pool and change its rows', async () => {
    apiMock.createDataPool.mockResolvedValue({ ...pool, id: 'pool-2', name: 'Orders' })
    await render(true)
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')
    expect(container.textContent).toContain('Rows of Customers')
    expect(button('Import CSV…')).toBeDefined()
    expect(button('Delete')).toBeDefined()
    expect(container.querySelector('[aria-label="Delete row 1"]')).not.toBeNull()
    expect(button('Add column')).toBeDefined()
    setInput(container.querySelector<HTMLInputElement>('input[placeholder="Pool name, e.g. Customer Accounts"]')!, 'Orders')
    await act(async () => { await Promise.resolve() })
    await act(async () => button('Add pool')!.click())
    expect(apiMock.createDataPool).toHaveBeenCalledWith({ name: 'Orders', description: '', project_id: 'project-1' })
  })

  it('shows a viewer the pools and rows to read and export, with no way to change them', async () => {
    await render(false)
    expect(container.textContent).toContain('You can view and export these pools. Editors and project admins can create and change them.')
    expect(container.querySelector('input[placeholder="Pool name, e.g. Customer Accounts"]')).toBeNull()
    expect(button('Delete')).toBeUndefined()
    expect(button('Import CSV…')).toBeUndefined()
    expect(container.querySelector('[aria-label="Delete row 1"]')).toBeNull()
    expect(button('Add column')).toBeUndefined()
    expect(container.querySelector('.data-grid input:not([type="checkbox"])')).toBeNull()
    expect(container.textContent).toContain('a@example.test')
    expect(button('Export CSV')).toBeDefined()
  })

  it('opens the pool a link names', async () => {
    window.location.hash = '#/repository?setting=test-data&resourceId=pool-1'
    await render(false)
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1', true)
  })

  it('asks for a project when none is active', async () => {
    await render(true, null)
    expect(container.textContent).toContain('Select a project to see its shared test data.')
    expect(apiMock.listDataPools).not.toHaveBeenCalled()
  })
})
