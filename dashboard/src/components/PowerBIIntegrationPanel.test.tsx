// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { DashboardPowerBISettings } from '../types/settings'
import PowerBIIntegrationPanel from './PowerBIIntegrationPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const mocks = vi.hoisted(() => ({
  testPowerBIConnection: vi.fn(),
  recreatePowerBIDataset: vi.fn(),
}))

vi.mock('../hooks/api/useSystemApi', () => ({
  useSystemApi: () => mocks,
}))

const baseSettings: DashboardPowerBISettings = {
  enabled: true,
  tenant_id: 'tenant-1',
  client_id: 'client-1',
  client_secret_env_var: 'APEXLOAD_POWERBI_CLIENT_SECRET',
  workspace_id: '11111111-2222-3333-4444-555555555555',
  dataset_name: 'APEX Load Performance Runs',
}

describe('PowerBIIntegrationPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.testPowerBIConnection.mockReset()
    mocks.recreatePowerBIDataset.mockReset()
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('edits settings fields through onChange', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<PowerBIIntegrationPanel value={baseSettings} onChange={onChange} />)
    })

    const tenantInput = container.querySelector('input[aria-label="Power BI tenant ID"]') as HTMLInputElement
    expect(tenantInput.value).toBe('tenant-1')

    const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    await act(async () => {
      nativeSetter?.call(tenantInput, 'tenant-2')
      tenantInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ tenant_id: 'tenant-2' }))
  })

  it('runs the connection test and reports a found dataset', async () => {
    mocks.testPowerBIConnection.mockResolvedValue({
      ok: true,
      dataset_exists: true,
      dataset_name: 'APEX Load Performance Runs',
    })
    await act(async () => {
      root.render(<PowerBIIntegrationPanel value={baseSettings} onChange={vi.fn()} />)
    })

    const testButton = Array.from(container.querySelectorAll('button')).find(candidate =>
      candidate.textContent?.includes('Test connection'),
    ) as HTMLButtonElement
    await act(async () => {
      testButton.click()
    })

    expect(mocks.testPowerBIConnection).toHaveBeenCalledTimes(1)
    const result = container.querySelector('[data-testid="powerbi-test-result"]')
    expect(result?.textContent).toContain('Connection OK')
    expect(result?.textContent).toContain('found in the workspace')
  })

  it('requires an explicit confirm before recreating the dataset', async () => {
    mocks.recreatePowerBIDataset.mockResolvedValue({
      dataset_id: 'ds-2',
      dataset_name: 'APEX Load Performance Runs',
      schema_version: 1,
    })
    await act(async () => {
      root.render(<PowerBIIntegrationPanel value={baseSettings} onChange={vi.fn()} />)
    })

    const recreateButton = Array.from(container.querySelectorAll('button')).find(candidate =>
      candidate.textContent?.includes('Recreate dataset'),
    ) as HTMLButtonElement
    await act(async () => {
      recreateButton.click()
    })
    expect(mocks.recreatePowerBIDataset).not.toHaveBeenCalled()

    const confirmButton = Array.from(container.querySelectorAll('button')).find(candidate =>
      candidate.textContent?.includes('Confirm: delete Power BI rows'),
    ) as HTMLButtonElement
    await act(async () => {
      confirmButton.click()
    })
    expect(mocks.recreatePowerBIDataset).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('recreated (schema v1)')
  })
})
