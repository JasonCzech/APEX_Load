// @vitest-environment jsdom
// The shared data pool editor, as Shared assets › Test data shows it.

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DataPoolsManager from './DataPoolsManager'
import type { Project } from '../types'
import { ApiError } from '../hooks/apiClient'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { downloadBlobMock } = vi.hoisted(() => ({
  downloadBlobMock: vi.fn(),
}))

const apiMock = {
  listDataPools: vi.fn(),
  getDataPool: vi.fn(),
  createDataPool: vi.fn(),
  updateDataPool: vi.fn(),
  deleteDataPool: vi.fn(),
  createDataPoolRow: vi.fn(),
  updateDataPoolRow: vi.fn(),
  deleteDataPoolRow: vi.fn(),
  importDataPoolRows: vi.fn(),
  exportDataPoolRows: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
}))
// What uses each asset is read by its own hook; tests set it directly.
const sharedAssetUsage = vi.hoisted(() => ({ value: null as import('../types/sharedAssets').SharedAssetUsage | null, refresh: vi.fn() }))
vi.mock('./useSharedAssetUsage', () => ({ useSharedAssetUsage: () => ({ usage: sharedAssetUsage.value, error: '', refresh: sharedAssetUsage.refresh }) }))

// The workspace the page renders the manager for.
const projectContextMock = {
  projects: [{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }] as Project[],
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' } as Project | null,
}

function seedProjectCatalog(projects: Project[]) {
  projectContextMock.projects = projects
}

// The section id the page gives it, so a test can scope its queries.
function renderManager(testRoot: Root, options: { preferredPoolId?: string; canManage?: boolean } = {}) {
  testRoot.render(
    <div id="settings-section-data-pools">
      <DataPoolsManager activeProject={projectContextMock.activeProject} projects={projectContextMock.projects} preferredPoolId={options.preferredPoolId} canManage={options.canManage ?? true} />
    </div>,
  )
}

describe('DataPoolsManager', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.sessionStorage.clear()
    projectContextMock.projects = [{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }]
    projectContextMock.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    apiMock.listDataPools.mockResolvedValue({ data_pools: [] })
    apiMock.getDataPool.mockResolvedValue({ rows: [] })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    downloadBlobMock.mockReset()
    window.sessionStorage.clear()
  })

  it('shows a viewer the pools and rows to read and export, with no way to change them', async () => {
    apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['email'], created_at: '', updated_at: '' }], count: 1 })
    apiMock.getDataPool.mockResolvedValue({ data_pool: { id: 'pool-1', name: 'Customers', columns: ['email'] }, rows: [{ id: 'r1', pool_id: 'pool-1', data: { email: 'a@example.test' }, version: 1, status: 'active', created_at: '', updated_at: '' }], total: 1 })
    await act(async () => renderManager(root, { canManage: false }))
    await flushPromises()
    await flushPromises()
    const section = container.querySelector('#settings-section-data-pools')
    expect(section?.textContent).toContain('You can view and export these pools. Editors and project admins can create and change them.')
    expect(section?.querySelector('input[placeholder="Pool name, e.g. Customer Accounts"]')).toBeNull()
    const buttons = Array.from(section?.querySelectorAll('button') || []).map(button => button.textContent?.trim())
    expect(buttons).not.toContain('Delete')
    expect(buttons).not.toContain('Import CSV…')
    expect(buttons).not.toContain('Add column')
    expect(buttons).toContain('Export CSV')
    expect(section?.querySelector('[aria-label="Delete row 1"]')).toBeNull()
    expect(section?.querySelector('.data-grid input:not([type="checkbox"])')).toBeNull()
    expect(section?.textContent).toContain('a@example.test')
  })

  it('surfaces a data-pool breadcrumb left by a create that preceded a remount', async () => {
    seedProjectCatalog([{ id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }
    window.sessionStorage.setItem('apexload.settings.dataPoolNotice', JSON.stringify({
      at: Date.now(),
      value: 'Created data pool "Orders" in project "Customer Portal". Switch to that project to manage it.',
    }))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()

    expect(container.querySelector('#settings-section-data-pools')?.textContent)
      .toContain('Created data pool "Orders" in project "Customer Portal". Switch to that project to manage it.')
    // The crumb is peeked, not consumed: the remount chain a scope switch
    // causes must be able to re-read it until the freshness bound retires it.
    expect(window.sessionStorage.getItem('apexload.settings.dataPoolNotice')).not.toBeNull()
  })


  it('asks for a project instead of offering to create one when the domain has projects but none is active', async () => {
    seedProjectCatalog([{ id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = null

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    const poolSection = container.querySelector('#settings-section-data-pools')

    expect(poolSection?.textContent).toContain('Select a project to create data pools.')
    expect(poolSection?.textContent).not.toContain('No projects in')
  })


  it('names the project a new data pool lands in', async () => {
    seedProjectCatalog([{ id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = { id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    const poolSection = container.querySelector('#settings-section-data-pools')

    expect(poolSection?.textContent).toContain('Creates in project "Customer Portal".')
  })


  it('imports CSV rows through the dry-run preview and exports the pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockResolvedValue({ data_pool: { id: 'pool-1', name: 'Customers' }, rows: [], total: 0 })
    apiMock.importDataPoolRows
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id', 'region'],
        row_count: 2,
        sample_rows: [{ customer_id: 'C-1', region: 'east' }, { customer_id: 'C-2', region: 'west' }],
        warnings: [],
      })
      .mockResolvedValueOnce({ mode: 'append', columns: ['customer_id', 'region'], imported: 2, total: 2, warnings: [] })
    const exportBlob = new Blob(['customer_id,region\nC-1,east\n'], { type: 'text/csv' })
    apiMock.exportDataPoolRows.mockResolvedValue({ blob: exportBlob, filename: 'Customers.csv' })
    downloadBlobMock.mockResolvedValue('browser_started')

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section).toBeTruthy()
    if (!section) return
    const fileInput = section.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return

    const file = new File(['customer_id,region\nC-1,east\nC-2,west\n'], 'customers.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { dryRun: true })
    expect(section.textContent).toContain('Import preview')
    expect(section.textContent).toContain('customers.csv')

    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [
        { id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1', region: 'east' }, version: 1, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' },
        { id: 'row-2', pool_id: 'pool-1', data: { customer_id: 'C-2', region: 'west' }, version: 1, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' },
      ],
      total: 2,
    })

    const confirmButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent?.includes('Append 2 row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return
    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { mode: 'append' })
    expect(section.textContent).toContain('Imported 2 row(s)')
    expect(section.textContent).toContain('2 rows')

    const exportButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Export CSV')
    expect(exportButton).toBeTruthy()
    if (!exportButton) return
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.exportDataPoolRows).toHaveBeenCalledWith('pool-1')
    expect(downloadBlobMock).toHaveBeenCalledWith(exportBlob, 'Customers.csv')
    expect(section.textContent).toContain('Exported Customers.csv.')
  })


  it('starts a replace-only empty APEX backup preview in replace mode', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [{ id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1' }, version: 1, created_at: '', updated_at: '' }],
      total: 1,
    })
    apiMock.importDataPoolRows.mockReset()
    apiMock.importDataPoolRows
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: [],
        row_count: 0,
        sample_rows: [],
        warnings: [],
        append_warning: 'an empty APEX data pool export can only be imported with mode=replace',
      })
      .mockResolvedValueOnce({
        mode: 'replace',
        columns: [],
        imported: 0,
        deleted: 1,
        total: 0,
        warnings: [],
      })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['# APEXLOAD-DATA-POOL-EMPTY\n'], 'empty.apexload.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const modeSelect = section?.querySelector('select[aria-label="Import mode"]') as HTMLSelectElement | null
    expect(modeSelect?.value).toBe('replace')
    expect(section?.textContent).toContain('can only be imported with mode=replace')
    const confirmButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Replace with 0 row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return

    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenNthCalledWith(1, 'pool-1', file, { dryRun: true })
    expect(apiMock.importDataPoolRows).toHaveBeenNthCalledWith(2, 'pool-1', file, { mode: 'replace' })
  })


  it('reports a created data pool as committed when only the list reconciliation fails', async () => {
    apiMock.listDataPools.mockReset()
    apiMock.listDataPools
      .mockResolvedValueOnce({ data_pools: [], count: 0 })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.createDataPool.mockReset()
    apiMock.createDataPool.mockResolvedValue({
      id: 'pool-1',
      name: 'Customers',
      description: '',
      project_id: 'project-1',
      created_at: '',
      updated_at: '',
    })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const nameInput = section?.querySelector('input[placeholder^="Pool name"]') as HTMLInputElement | null
    expect(nameInput).toBeTruthy()
    if (!nameInput) return
    await setFieldValue(nameInput, 'Customers')
    const addPool = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add pool')
    expect(addPool).toBeTruthy()
    if (!addPool) return
    await act(async () => {
      addPool.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      addPool.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.createDataPool).toHaveBeenCalledWith({
      name: 'Customers',
      description: '',
      project_id: 'project-1',
    })
    expect(apiMock.createDataPool).toHaveBeenCalledTimes(1)
    expect(section?.textContent).toContain('The create succeeded—do not retry.')
    expect(section?.textContent).not.toContain('Unable to create data pool')
  })


  it('serializes a pool delete and reports it as committed when list reconciliation fails', async () => {
    apiMock.listDataPools.mockReset()
    apiMock.listDataPools
      .mockResolvedValueOnce({
        data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
        count: 1,
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [],
      total: 0,
    })
    apiMock.deleteDataPool.mockReset()
    apiMock.deleteDataPool.mockResolvedValue({ deleted: 'pool-1' })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('.settings-release-row > .settings-inline-actions button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete data pool')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.deleteDataPool).toHaveBeenCalledWith('pool-1')
    expect(apiMock.deleteDataPool).toHaveBeenCalledTimes(1)
    expect(section?.textContent).toContain('The delete succeeded—do not retry.')
    expect(section?.textContent).not.toContain('Unable to delete data pool')
  })


  it('says which scenarios bind each pool, links them, and lists them before a delete they block', async () => {
    sharedAssetUsage.value = { project_id: 'project-1', data_pools: { 'pool-1': [{ id: 'scenario-1', name: 'Checkout run', alias: 'customers' }] }, live_tables: {}, object_repositories: {}, correlation_rulesets: {} }
    try {
      apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }, { id: 'pool-2', name: 'Idle', project_id: 'project-1', created_at: '', updated_at: '' }], count: 2 })
      apiMock.getDataPool.mockResolvedValue({ data_pool: { id: 'pool-1', name: 'Customers' }, rows: [], total: 0 })
      await act(async () => renderManager(root))
      await flushPromises()
      await flushPromises()
      const section = container.querySelector('#settings-section-data-pools')!
      const rows = Array.from(section.querySelectorAll('.settings-release-row'))
      expect(rows.map(row => row.querySelector('small')?.textContent)).toEqual([expect.stringContaining('Used by 1 scenario'), expect.stringContaining('Not bound by a scenario')])
      // The open pool links its scenario at the Data step, where the binding is changed.
      const link = section.querySelector<HTMLAnchorElement>('.shared-asset-used-by a')
      expect(link?.textContent).toBe('Checkout run')
      expect(link?.getAttribute('href')).toBe('#/tests?view=builder&scenarioId=scenario-1&step=data&projectId=project-1')

      const deleteFor = (row: Element) => Array.from(row.querySelectorAll('.settings-inline-actions button')).find(button => button.textContent?.trim() === 'Delete')!
      sharedAssetUsage.refresh.mockClear()
      await act(async () => deleteFor(rows[0]).dispatchEvent(new MouseEvent('click', { bubbles: true })))
      // A fresh read, so the list is current when it is shown.
      expect(sharedAssetUsage.refresh).toHaveBeenCalled()
      let dialog = container.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('cannot be deleted while scenarios bind it')
      expect(dialog.querySelector('.shared-asset-used-by a')?.textContent).toBe('Checkout run')
      const confirm = () => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent === 'Delete data pool')!
      expect(confirm().disabled).toBe(true)
      await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent === 'Cancel')!.click())

      await act(async () => deleteFor(rows[1]).dispatchEvent(new MouseEvent('click', { bubbles: true })))
      dialog = container.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('No scenario binds it.')
      expect(confirm().disabled).toBe(false)
    } finally {
      sharedAssetUsage.value = null
    }
  })

  it('opens a repository-linked pool and never silently selects a different pool for an unavailable link', async () => {
    apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'pool-linked', name: 'Linked pool', project_id: 'project-1', created_at: '', updated_at: '' }, { id: 'pool-other', name: 'Other pool', project_id: 'project-1', created_at: '', updated_at: '' }], count: 2 })
    const render = (resourceId: string) => {
      window.location.hash = `#/repository?setting=test-data&resourceId=${resourceId}`
      renderManager(root, { preferredPoolId: resourceId })
    }
    await act(async () => render('pool-linked'))
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1', true)
    expect(container.querySelector('#settings-section-data-pools .settings-release-row.active')?.textContent).toContain('Linked pool')
    await act(async () => render('missing'))
    await flushPromises()
    expect(container.querySelector('#settings-section-data-pools .settings-release-row.active')).toBeNull()
    expect(container.textContent).toContain('The requested data pool is not available in this project')
    window.location.hash = ''
  })


  it('does not let a slow data-pool response from the previous project overwrite the active project', async () => {
    type DataPoolListResponse = { data_pools: Array<{ id: string; name: string; project_id: string; created_at: string; updated_at: string }>; count: number }
    let resolveProjectOne: ((response: DataPoolListResponse) => void) | undefined
    let resolveProjectTwo: ((response: DataPoolListResponse) => void) | undefined
    apiMock.listDataPools.mockImplementation((projectId?: string) => new Promise<DataPoolListResponse>(resolve => {
      if (projectId === 'project-1') resolveProjectOne = resolve
      if (projectId === 'project-2') resolveProjectTwo = resolve
    }))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')

    projectContextMock.activeProject = { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }
    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-2')

    await act(async () => {
      resolveProjectTwo?.({
        data_pools: [{ id: 'pool-2', name: 'Payment Accounts', project_id: 'project-2', created_at: '', updated_at: '' }],
        count: 1,
      })
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section?.textContent).toContain('Payment Accounts')
    expect(section?.textContent).not.toContain('Checkout Customers')

    await act(async () => {
      resolveProjectOne?.({
        data_pools: [{ id: 'pool-1', name: 'Checkout Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
        count: 1,
      })
    })
    await flushPromises()

    expect(section?.textContent).toContain('Payment Accounts')
    expect(section?.textContent).not.toContain('Checkout Customers')
  })


  it('loads data-pool rows beyond the first bounded page', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      id: `row-${index + 1}`,
      pool_id: 'pool-1',
      data: { customer_id: `C-${index + 1}` },
      version: 1,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }))
    apiMock.getDataPool.mockImplementation((_poolId: string, page?: { limit?: number; cursor?: string }) => {
      if (page?.cursor === 'cursor-after-row-100') {
        return Promise.resolve({
          data_pool: { id: 'pool-1', name: 'Customers' },
          rows: [{
            id: 'row-101',
            pool_id: 'pool-1',
            data: { customer_id: 'C-101' },
            version: 1,
            created_at: '2026-05-18T12:00:00Z',
            updated_at: '2026-05-18T12:00:00Z',
          }],
          total: 101,
          limit: 100,
          offset: 100,
          revision: 'revision-1',
          has_more: false,
          next_cursor: '',
        })
      }
      return Promise.resolve({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: firstPage,
        total: 101,
        limit: 100,
        offset: 0,
        revision: 'revision-1',
        has_more: true,
        next_cursor: 'cursor-after-row-100',
      })
    })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section?.textContent).toContain('Showing 100 of 101 rows')
    const loadMore = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Load more rows (1 remaining)'))
    expect(loadMore).toBeTruthy()
    if (!loadMore) return

    await act(async () => {
      loadMore.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getDataPool).toHaveBeenCalledWith('pool-1', { limit: 100, cursor: 'cursor-after-row-100' })
    expect(dataPoolGridText(section)).toContain('C-101')
    expect(section?.textContent).toContain('101 rows')
    expect(section?.textContent).not.toContain('Load more rows')
  })


  it('restarts data-pool pagination when the revision-bound cursor becomes stale', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-stale',
          pool_id: 'pool-1',
          data: { customer_id: 'C-stale' },
          version: 1,
          created_at: '2026-05-18T12:00:00Z',
          updated_at: '2026-05-18T12:00:00Z',
        }],
        total: 2,
        limit: 100,
        offset: 0,
        revision: 'revision-1',
        has_more: true,
        next_cursor: 'cursor-after-stale-row',
      })
      .mockRejectedValueOnce(new ApiError('data pool rows changed while pages were being loaded', 409))
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-fresh',
          pool_id: 'pool-1',
          data: { customer_id: 'C-fresh' },
          version: 1,
          created_at: '2026-05-18T12:01:00Z',
          updated_at: '2026-05-18T12:01:00Z',
        }],
        total: 1,
        limit: 100,
        offset: 0,
        revision: 'revision-2',
        has_more: false,
        next_cursor: '',
      })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const loadMore = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Load more rows'))
    expect(loadMore).toBeTruthy()
    if (!loadMore) return

    await act(async () => {
      loadMore.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.getDataPool).toHaveBeenNthCalledWith(2, 'pool-1', { limit: 100, cursor: 'cursor-after-stale-row' })
    expect(apiMock.getDataPool).toHaveBeenNthCalledWith(3, 'pool-1', { limit: 100, cursor: '' })
    expect(dataPoolGridText(section)).toContain('C-fresh')
    expect(dataPoolGridText(section)).not.toContain('C-stale')
    expect(section?.textContent).toContain('refreshed from the beginning')
  })


  it('ignores a stale initial row-load failure after a newer mutation refresh succeeds', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['customer_id'], created_at: '', updated_at: '' }],
      count: 1,
    })
    let rejectInitialLoad: ((reason?: unknown) => void) | undefined
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool
      .mockImplementationOnce(() => new Promise((_resolve, reject) => {
        rejectInitialLoad = reject
      }))
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-1',
          pool_id: 'pool-1',
          data: { customer_id: 'C-10001' },
          version: 1,
          created_at: '',
          updated_at: '',
        }],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockResolvedValue({
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-10001' },
      version: 1,
      created_at: '',
      updated_at: '',
    })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const newCell = dataPoolCell(section, 'customer_id, new row')
    expect(newCell).toBeTruthy()
    if (!newCell) return
    await setFieldValue(newCell, 'C-10001')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add row')
    expect(addButton).toBeTruthy()
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()
    expect(dataPoolGridText(section)).toContain('C-10001')

    await act(async () => {
      rejectInitialLoad?.(new Error('stale initial row read failed'))
    })
    await flushPromises()

    expect(dataPoolGridText(section)).toContain('C-10001')
    expect(section?.textContent).not.toContain('stale initial row read failed')
    expect(section?.textContent).not.toContain('Unable to load data pool rows')
  })


  it('shows pool rows one column per parameter in the pool\'s column order', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['zip', 'customer_id'], created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockReset().mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers', columns: ['zip', 'customer_id'] },
      rows: [
        { id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1', zip: '10001', email: 'a@example.test' }, version: 1, created_at: '', updated_at: '2026-05-18T12:00:00Z' },
        { id: 'row-2', pool_id: 'pool-1', status: 'consumed', data: { customer_id: 'C-2' }, version: 2, created_at: '', updated_at: '2026-05-18T12:00:00Z' },
      ],
      total: 2,
      has_more: false,
      next_cursor: '',
    })
    apiMock.createDataPoolRow.mockReset()
    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()
    const section = container.querySelector('#settings-section-data-pools')!
    const headers = Array.from(section.querySelectorAll('table[aria-label="Rows of Customers"] thead th')).map(cell => cell.textContent)
    // Stored order first, then columns only rows use; no JSON column.
    expect(headers.slice(1, 4)).toEqual(['zip', 'customer_id', 'email'])
    expect(section.textContent).not.toContain('{"')
    expect(dataPoolCell(section, 'zip, row 1')?.value).toBe('10001')
    expect(dataPoolCell(section, 'zip, row 2')?.placeholder).toBe('—')
    // Inactive rows say so; status and version sit behind the details toggle.
    expect(section.textContent).toContain('consumed, skipped by runs')
    expect(section.textContent).not.toContain('version 2')
    const toggle = Array.from(section.querySelectorAll('label')).find(label => label.textContent === 'Show row details')?.querySelector('input')
    await act(async () => { toggle?.click() })
    expect(section.textContent).toContain('consumed · version 2')

    // An empty new row is not sent.
    const addButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Add row')!
    await act(async () => addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(section.textContent).toContain('Type at least one value before adding the row.')
    expect(apiMock.createDataPoolRow).not.toHaveBeenCalled()
  })


  it('adds a column to the pool\'s stored order with its edit token', async () => {
    const pool = { id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['customer_id'], created_at: '', updated_at: '2026-05-18T12:00:00Z' }
    apiMock.listDataPools.mockResolvedValue({ data_pools: [pool], count: 1 })
    apiMock.getDataPool.mockReset().mockResolvedValue({ data_pool: pool, rows: [], total: 0, has_more: false, next_cursor: '' })
    apiMock.updateDataPool.mockReset().mockResolvedValue({ ...pool, columns: ['customer_id', 'region'], updated_at: '2026-05-18T12:01:00Z' })
    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()
    const section = container.querySelector('#settings-section-data-pools')!
    const addColumn = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Add column')!
    await act(async () => addColumn.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const nameInput = Array.from(section.querySelectorAll('label')).find(label => label.textContent?.startsWith('Column name'))?.querySelector('input') as HTMLInputElement
    await setFieldValue(nameInput, 'customer_id')
    const submit = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Add')!
    await act(async () => submit.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(section.textContent).toContain('There is already a column named "customer_id".')
    expect(apiMock.updateDataPool).not.toHaveBeenCalled()

    await setFieldValue(nameInput, 'region')
    await act(async () => submit.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    expect(apiMock.updateDataPool).toHaveBeenCalledWith('pool-1', { name: 'Customers', columns: ['customer_id', 'region'], updated_at: '2026-05-18T12:00:00Z' })
    expect(dataPoolCell(section, 'region, new row')).toBeTruthy()

    // A column no row sets can be removed again.
    const emptyColumns = section.querySelector('.data-grid-empty-columns[role="group"]')!
    expect([...emptyColumns.querySelectorAll('button')].map(button => button.textContent)).toEqual(['Remove customer_id', 'Remove region'])
    apiMock.updateDataPool.mockResolvedValue({ ...pool, columns: ['customer_id'], updated_at: '2026-05-18T12:02:00Z' })
    await act(async () => [...emptyColumns.querySelectorAll('button')].find(button => button.textContent === 'Remove region')!.click())
    await flushPromises()
    expect(apiMock.updateDataPool).toHaveBeenLastCalledWith('pool-1', { name: 'Customers', columns: ['customer_id'], updated_at: '2026-05-18T12:01:00Z' })
    expect(dataPoolCell(section, 'region, new row')).toBeNull()
  })


  it('appends pasted spreadsheet rows as one import matched by their header', async () => {
    const pool = { id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['customer_id', 'region'], created_at: '', updated_at: '' }
    apiMock.listDataPools.mockResolvedValue({ data_pools: [pool], count: 1 })
    apiMock.getDataPool.mockReset().mockResolvedValue({ data_pool: pool, rows: [], total: 0, has_more: false, next_cursor: '' })
    apiMock.importDataPoolRows.mockReset().mockResolvedValue({ mode: 'append', columns: ['region', 'customer_id'], imported: 2, total: 2 })
    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()
    const section = container.querySelector('#settings-section-data-pools')!
    const cell = dataPoolCell(section, 'customer_id, new row')!
    const paste = new Event('paste', { bubbles: true, cancelable: true }) as Event & { clipboardData: { getData: (type: string) => string } }
    paste.clipboardData = { getData: () => 'region\tcustomer_id\neast\tC-1\nwest\tC-2\n' }
    await act(async () => { cell.dispatchEvent(paste) })
    await flushPromises()
    await flushPromises()
    expect(apiMock.importDataPoolRows).toHaveBeenCalledTimes(1)
    const [poolId, file, options] = apiMock.importDataPoolRows.mock.calls[0] as [string, File, unknown]
    expect(poolId).toBe('pool-1')
    expect(options).toEqual({ mode: 'append', delimiter: 'comma' })
    expect(await file.text()).toBe('customer_id,region\r\nC-1,east\r\nC-2,west\r\n')
    expect(container.textContent).toContain('2 rows added.')
  })


  it('reports a created data-pool row as saved when only the reconciliation read fails', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', columns: ['customer_id'], created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockResolvedValue({
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-10001' },
      version: 1,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const newCell = dataPoolCell(section, 'customer_id, new row')
    expect(newCell).toBeTruthy()
    if (!newCell) return
    await setFieldValue(newCell, 'C-10001')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add row')
    expect(addButton).toBeTruthy()
    if (!addButton) return

    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.createDataPoolRow).toHaveBeenCalledWith('pool-1', { data: { customer_id: 'C-10001' } })
    expect(container.textContent).toContain('The save succeeded—do not add it again.')
    expect(container.textContent).not.toContain('Unable to save data row')
  })


  it('saves an edited cell as one row update with the version the row was loaded at', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    const row = {
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-1', region: 'east' },
      version: 3,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }
    apiMock.getDataPool.mockReset().mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [row],
      total: 1,
      has_more: false,
      next_cursor: '',
    })
    apiMock.updateDataPoolRow.mockReset()
      .mockResolvedValueOnce({ ...row, data: { customer_id: 'C-2', region: 'east' }, version: 4 })
      .mockResolvedValueOnce({ ...row, data: { customer_id: 'C-2', region: 'west' }, version: 5 })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const idCell = dataPoolCell(section, 'customer_id, row 1')!
    const regionCell = dataPoolCell(section, 'region, row 1')!
    await setFieldValue(idCell, 'C-2')
    await setFieldValue(regionCell, 'west')
    // Two edits in a row: the second waits for the first and sends its version.
    await act(async () => {
      idCell.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
      regionCell.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.updateDataPoolRow).toHaveBeenNthCalledWith(1, 'pool-1', 'row-1', { data: { customer_id: 'C-2', region: 'east' }, version: 3 })
    expect(apiMock.updateDataPoolRow).toHaveBeenNthCalledWith(2, 'pool-1', 'row-1', { data: { customer_id: 'C-2', region: 'west' }, version: 4 })
    // Saved in place: no reload of the row list.
    expect(apiMock.getDataPool).toHaveBeenCalledTimes(1)
    expect(dataPoolCell(section, 'region, row 1')?.value).toBe('west')
    expect(container.textContent).not.toContain('Unable to save')
  })


  it('reports a deleted data-pool row as committed when only the reconciliation read fails', async () => {
    const row = {
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-1' },
      version: 3,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [row],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.deleteDataPoolRow.mockReset()
    apiMock.deleteDataPoolRow.mockResolvedValue({ deleted: 'row-1' })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('.data-grid-actions button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete row')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledWith('pool-1', 'row-1')
    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('The delete succeeded—do not retry.')
    expect(container.textContent).not.toContain('Unable to delete data row')
  })


  it('reports an imported data-pool file as committed when only the reconciliation read fails', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.importDataPoolRows
      .mockReset()
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id'],
        row_count: 2,
        sample_rows: [{ customer_id: 'C-1' }, { customer_id: 'C-2' }],
        warnings: [],
      })
      .mockResolvedValueOnce({
        mode: 'append',
        columns: ['customer_id'],
        imported: 2,
        total: 2,
        warnings: [],
      })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['customer_id\nC-1\nC-2\n'], 'customers.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const confirmButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Append 2 row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return
    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { mode: 'append' })
    expect(container.textContent).toContain('The import succeeded—do not import the file again.')
    expect(container.textContent).not.toContain('Unable to import rows')
  })


  it('keeps a confirmed row delete bound to its row when another pool becomes selected', async () => {
    const rowA = {
      id: 'row-a',
      pool_id: 'pool-a',
      data: { customer_id: 'A-visible' },
      version: 1,
      created_at: '',
      updated_at: '',
    }
    const rowB = {
      id: 'row-b',
      pool_id: 'pool-b',
      data: { customer_id: 'B-visible' },
      version: 1,
      created_at: '',
      updated_at: '',
    }
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: poolId === 'pool-a' ? [rowA] : [rowB],
      total: 1,
      has_more: false,
      next_cursor: '',
    }))
    apiMock.deleteDataPoolRow.mockReset()
    apiMock.deleteDataPoolRow.mockResolvedValue({ deleted: 'row-a' })

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('.data-grid-actions button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete row')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledWith('pool-a', 'row-a')
    expect(apiMock.getDataPool.mock.calls.filter(([poolId]) => poolId === 'pool-a')).toHaveLength(1)
    expect(dataPoolGridText(section)).toContain('B-visible')
    expect(section?.textContent).not.toContain('Row deleted.')
    expect(section?.textContent).not.toContain('Unable to delete data row')
  })


  it('does not let a late row mutation invalidate the newly selected pool or erase its draft', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', columns: ['customer_id'], created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', columns: ['customer_id'], created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    let resolvePoolB: ((value: {
      data_pool: { id: string; name: string }
      rows: Array<{
        id: string
        pool_id: string
        data: { customer_id: string }
        version: number
        created_at: string
        updated_at: string
      }>
      total: number
      has_more: boolean
      next_cursor: string
    }) => void) | undefined
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => {
      if (poolId === 'pool-b') {
        return new Promise(resolve => {
          resolvePoolB = resolve
        })
      }
      return Promise.resolve({
        data_pool: { id: 'pool-a', name: 'Primary Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
    })
    let resolveCreate: ((value: {
      id: string
      pool_id: string
      data: { customer_id: string }
      version: number
      created_at: string
      updated_at: string
    }) => void) | undefined
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockImplementation(() => new Promise(resolve => {
      resolveCreate = resolve
    }))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const poolACell = dataPoolCell(section, 'customer_id, new row')
    expect(poolACell).toBeTruthy()
    if (!poolACell) return
    await setFieldValue(poolACell, 'C-10001')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add row')
    expect(addButton).toBeTruthy()
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const rowEditor = dataPoolCell(section, 'customer_id, new row')
    expect(rowEditor).toBeTruthy()
    if (!rowEditor) return
    const poolBDraft = 'B-draft'
    await setFieldValue(rowEditor, poolBDraft)

    await act(async () => {
      resolveCreate?.({
        id: 'row-a',
        pool_id: 'pool-a',
        data: { customer_id: 'C-10001' },
        version: 1,
        created_at: '2026-05-18T12:00:00Z',
        updated_at: '2026-05-18T12:00:00Z',
      })
    })
    await flushPromises()
    expect(apiMock.getDataPool.mock.calls.filter(([poolId]) => poolId === 'pool-a')).toHaveLength(1)

    await act(async () => {
      resolvePoolB?.({
        data_pool: { id: 'pool-b', name: 'Secondary Customers' },
        rows: [{
          id: 'row-b',
          pool_id: 'pool-b',
          data: { customer_id: 'B-visible' },
          version: 1,
          created_at: '2026-05-18T12:01:00Z',
          updated_at: '2026-05-18T12:01:00Z',
        }],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
    })
    await flushPromises()

    expect(dataPoolGridText(section)).toContain('B-visible')
    expect(dataPoolCell(section, 'customer_id, new row')?.value).toBe(poolBDraft)
  })


  it('discards a late import preview after the user selects another data pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: [],
      total: 0,
      has_more: false,
      next_cursor: '',
    }))
    let resolvePreview: ((value: {
      dry_run: boolean
      mode: string
      columns: string[]
      row_count: number
      sample_rows: Array<{ customer_id: string }>
      warnings: string[]
    }) => void) | undefined
    apiMock.importDataPoolRows.mockReset()
    apiMock.importDataPoolRows.mockImplementation(() => new Promise(resolve => {
      resolvePreview = resolve
    }))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['customer_id\nC-1\n'], 'primary.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await act(async () => {
      resolvePreview?.({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id'],
        row_count: 1,
        sample_rows: [{ customer_id: 'C-1' }],
        warnings: [],
      })
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledTimes(1)
    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-a', file, { dryRun: true })
    expect(section?.textContent).not.toContain('Import preview')
    expect(section?.textContent).not.toContain('Append 1 row(s)')
  })


  it('does not surface a late export failure against a newly selected data pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: poolId === 'pool-a'
        ? [{
            id: 'row-a',
            pool_id: 'pool-a',
            data: { customer_id: 'C-1' },
            version: 1,
            created_at: '2026-05-18T12:00:00Z',
            updated_at: '2026-05-18T12:00:00Z',
          }]
        : [],
      total: poolId === 'pool-a' ? 1 : 0,
      has_more: false,
      next_cursor: '',
    }))
    let rejectExport: ((reason?: unknown) => void) | undefined
    apiMock.exportDataPoolRows.mockReset()
    apiMock.exportDataPoolRows.mockImplementation(() => new Promise((_resolve, reject) => {
      rejectExport = reject
    }))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const exportButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Export CSV')
    expect(exportButton).toBeTruthy()
    if (!exportButton) return
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await act(async () => {
      rejectExport?.(new Error('export backend unavailable'))
    })
    await flushPromises()

    expect(apiMock.exportDataPoolRows).toHaveBeenCalledWith('pool-a')
    expect(container.textContent).not.toContain('Unable to export data pool')
    expect(container.textContent).not.toContain('export backend unavailable')
  })


  it('keeps a conflicting cell value in its cell and reloads the row on a 409', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{ id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1' }, version: 3, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' }],
        total: 1,
      })
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{ id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-9' }, version: 4, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:01:00Z' }],
        total: 1,
      })
    apiMock.updateDataPoolRow.mockReset().mockRejectedValue(new ApiError('data pool row changed while the update was in flight; reload and retry', 409))

    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const cell = dataPoolCell(section, 'customer_id, row 1')!
    await setFieldValue(cell, 'C-2')
    await act(async () => { cell.dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    await flushPromises()
    await flushPromises()

    expect(apiMock.updateDataPoolRow).toHaveBeenCalledWith('pool-1', 'row-1', { data: { customer_id: 'C-2' }, version: 3 })
    expect(apiMock.getDataPool).toHaveBeenCalledTimes(2)
    // The typed value stays put, marked, over the reloaded row.
    expect(dataPoolCell(section, 'customer_id, row 1')?.value).toBe('C-2')
    expect(dataPoolCell(section, 'customer_id, row 1')?.getAttribute('aria-invalid')).toBe('true')
    // The reason is shown once, at the cell, not also as a page error.
    const description = document.getElementById(dataPoolCell(section, 'customer_id, row 1')?.getAttribute('aria-describedby') || '')
    expect(description?.textContent).toContain('customer_id in row 1 was not saved. The row changed while you were editing')
    expect(container.querySelector('#settings-section-data-pools .settings-error-inline')).toBeNull()
  })


  it('keeps rows from later pages, and the value in their cells, when a cell save conflicts', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    const row = (index: number, version = 1) => ({ id: `row-${index}`, pool_id: 'pool-1', data: { customer_id: `C-${index}` }, version, created_at: '', updated_at: '' })
    const firstPage = { data_pool: { id: 'pool-1', name: 'Customers' }, rows: Array.from({ length: 100 }, (_, index) => row(index + 1)), total: 101, has_more: true, next_cursor: 'after-100' }
    apiMock.getDataPool.mockReset()
      .mockResolvedValueOnce(firstPage)
      .mockResolvedValueOnce({ data_pool: { id: 'pool-1', name: 'Customers' }, rows: [row(101)], total: 101, has_more: false, next_cursor: '' })
      .mockResolvedValueOnce(firstPage)
      .mockResolvedValueOnce({ data_pool: { id: 'pool-1', name: 'Customers' }, rows: [{ ...row(101, 2), data: { customer_id: 'C-other' } }], total: 101, has_more: false, next_cursor: '' })
    apiMock.updateDataPoolRow.mockReset().mockRejectedValue(new ApiError('data pool row changed', 409))
    await act(async () => {
      renderManager(root)
    })
    await flushPromises()
    await flushPromises()
    const section = container.querySelector('#settings-section-data-pools')
    const loadMore = Array.from(section?.querySelectorAll('button') || []).find(button => button.textContent?.includes('Load more rows'))!
    await act(async () => loadMore.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    const cell = dataPoolCell(section, 'customer_id, row 101')!
    await setFieldValue(cell, 'C-mine')
    await act(async () => { cell.dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    for (let i = 0; i < 4; i++) await flushPromises()

    expect(apiMock.getDataPool).toHaveBeenNthCalledWith(4, 'pool-1', { limit: 100, cursor: 'after-100' })
    expect(dataPoolCell(section, 'customer_id, row 101')?.value).toBe('C-mine')
    expect(section?.textContent).toContain('101 rows')
  })
})

async function setFieldValue(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
  await act(async () => {
    const prototype = Object.getPrototypeOf(element)
    const descriptor = Object.getOwnPropertyDescriptor(prototype, 'value')
    descriptor?.set?.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

// Grid cells are inputs, so their values are not part of textContent.
function dataPoolGridText(section: Element | null | undefined): string {
  const values = Array.from(section?.querySelectorAll<HTMLInputElement>('input.data-grid-input') || []).map(input => input.value)
  return [section?.textContent || '', ...values].join(' ')
}

function dataPoolCell(section: Element | null | undefined, label: string): HTMLInputElement | null {
  return section?.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`) || null
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}
