// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../hooks/apiClient'
import type { DashboardPowerBIPublish } from '../../types/settings'
import RunReportPowerBIPublish from './RunReportPowerBIPublish'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const completePublish: DashboardPowerBIPublish = {
  id: 'pub-1',
  test_id: 'test-1',
  project_id: 'proj-1',
  workspace_id: 'ws-1',
  schema_version: 1,
  status: 'complete',
  run_rows_pushed: 1,
  transaction_rows_pushed: 8,
  transaction_rows_total: 8,
  published_at: '2026-07-20T12:00:00Z',
  created_at: '2026-07-20T12:00:00Z',
  updated_at: '2026-07-20T12:00:00Z',
}

describe('RunReportPowerBIPublish', () => {
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

  it('renders nothing when the integration is disabled', async () => {
    const getStatus = vi.fn().mockResolvedValue({ enabled: false, publishes: [] })
    await act(async () => {
      root.render(<RunReportPowerBIPublish testId="test-1" publish={vi.fn()} getStatus={getStatus} />)
    })
    expect(container.querySelector('[data-testid="powerbi-publish-panel"]')).toBeNull()
  })

  it('publishes and shows the resulting publish record', async () => {
    const getStatus = vi.fn().mockResolvedValue({ enabled: true, publishes: [] })
    const publish = vi.fn().mockResolvedValue({ publish: completePublish })
    await act(async () => {
      root.render(<RunReportPowerBIPublish testId="test-1" publish={publish} getStatus={getStatus} />)
    })

    const button = container.querySelector('button') as HTMLButtonElement
    expect(button.textContent).toContain('Publish to Power BI')
    await act(async () => {
      button.click()
    })

    expect(publish).toHaveBeenCalledWith('test-1', { force: false })
    expect(container.querySelector('[data-testid="powerbi-last-publish"]')?.textContent).toContain('8 transaction rows')
  })

  it('offers a force re-publish when the backend returns a 409 conflict', async () => {
    const getStatus = vi.fn().mockResolvedValue({ enabled: true, publishes: [completePublish] })
    const publish = vi.fn()
      // Shaped exactly like the backend contract: a 409 ApiError whose payload
      // carries the prior publish record.
      .mockRejectedValueOnce(new ApiError('this run was already published', 409, { error: 'this run was already published', publish: completePublish }))
      .mockResolvedValueOnce({ publish: { ...completePublish, id: 'pub-2' } })
    await act(async () => {
      root.render(<RunReportPowerBIPublish testId="test-1" publish={publish} getStatus={getStatus} />)
    })

    await act(async () => {
      ;(container.querySelector('button') as HTMLButtonElement).click()
    })
    expect(container.textContent).toContain('duplicate rows')

    const forceButton = Array.from(container.querySelectorAll('button')).find(candidate =>
      candidate.textContent?.includes('Publish again'),
    ) as HTMLButtonElement
    await act(async () => {
      forceButton.click()
    })
    expect(publish).toHaveBeenLastCalledWith('test-1', { force: true })
  })

  it('shows a 409 without a publish record (schema drift) as an error, not the force confirm', async () => {
    const getStatus = vi.fn().mockResolvedValue({ enabled: true, publishes: [] })
    const publish = vi.fn().mockRejectedValue(
      new ApiError('power bi publish failed: dataset schema drift', 409, { error: 'dataset schema drift' }),
    )
    await act(async () => {
      root.render(<RunReportPowerBIPublish testId="test-1" publish={publish} getStatus={getStatus} />)
    })
    await act(async () => {
      ;(container.querySelector('button') as HTMLButtonElement).click()
    })
    expect(container.textContent).not.toContain('Publish again')
    expect(container.querySelector('.run-report-error')?.textContent).toContain('schema drift')
  })

  it('surfaces publish failures', async () => {
    const getStatus = vi.fn().mockResolvedValue({ enabled: true, publishes: [] })
    const publish = vi.fn().mockRejectedValue(new Error('power bi publish failed: token error'))
    await act(async () => {
      root.render(<RunReportPowerBIPublish testId="test-1" publish={publish} getStatus={getStatus} />)
    })
    await act(async () => {
      ;(container.querySelector('button') as HTMLButtonElement).click()
    })
    expect(container.querySelector('.run-report-error')?.textContent).toContain('token error')
  })
})
