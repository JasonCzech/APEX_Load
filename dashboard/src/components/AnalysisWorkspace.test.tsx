// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisWorkspace from './AnalysisWorkspace'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })

it('reports the native capability and hides unsupported imports', async () => {
  request.mockResolvedValue({ native_parquet: false, enabled: true })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  expect(container.textContent).toContain('Native Parquet analysis is not enabled')
  expect(container.querySelector('input[type=file]')).toBeNull()
})

it('allows a viewer to inspect dataset state without upload or deletion controls', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { native_parquet: true, enabled: true } : [{ id: 'one', name: 'My dataset', state: 'cancelled', files: [], accepted: 0, rejected: 0 }])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" />))
  const item = [...container.querySelectorAll('button')].find(b => b.textContent?.includes('My dataset'))!
  await act(async () => item.click())
  expect(container.textContent).toContain('State: cancelled')
  expect(container.querySelector('input[type=file]')).toBeNull()
  expect(container.textContent).not.toContain('Delete dataset')
})
