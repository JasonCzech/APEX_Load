// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { SharedAssetUsageKind } from '../types/sharedAssets'
import { useSharedAssetUsage } from './useSharedAssetUsage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { api } = vi.hoisted(() => ({ api: { getSharedAssetUsage: vi.fn() } }))
vi.mock('../hooks/api/useProjectApi', () => ({ useProjectApi: () => api }))

let latest: ReturnType<typeof useSharedAssetUsage> | null = null
function Probe({ projectId, kind }: { projectId?: string; kind: SharedAssetUsageKind }) {
  latest = useSharedAssetUsage(projectId, kind)
  return null
}

describe('useSharedAssetUsage', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    api.getSharedAssetUsage.mockReset()
    container = document.createElement('div')
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    latest = null
  })

  it('asks only for the kind a section shows, and again on refresh', async () => {
    api.getSharedAssetUsage.mockResolvedValue({ project_id: 'p1', data_pools: { pool: [] } })
    await act(async () => root.render(<Probe projectId="p1" kind="data_pools" />))
    expect(api.getSharedAssetUsage).toHaveBeenCalledWith('p1', ['data_pools'], expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(latest?.usage).toEqual({ project_id: 'p1', data_pools: { pool: [] } })
    await act(async () => latest!.refresh())
    expect(api.getSharedAssetUsage).toHaveBeenCalledTimes(2)
  })

  it('never returns another project’s usage while the new project’s is loading', async () => {
    api.getSharedAssetUsage.mockResolvedValueOnce({ project_id: 'p1', object_repositories: {} })
    await act(async () => root.render(<Probe projectId="p1" kind="object_repositories" />))
    api.getSharedAssetUsage.mockReturnValueOnce(new Promise(() => {}))
    await act(async () => root.render(<Probe projectId="p2" kind="object_repositories" />))
    expect(latest?.usage).toBeNull()
  })

  it('says when what uses the assets could not be checked', async () => {
    api.getSharedAssetUsage.mockRejectedValue(new Error('failed to list scripts'))
    await act(async () => root.render(<Probe projectId="p1" kind="object_repositories" />))
    expect(latest?.usage).toBeNull()
    expect(latest?.error).toBe('Could not check what uses these. failed to list scripts')
  })
})
