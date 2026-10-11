// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ObjectHistory, { ActivityTable } from './ObjectHistory'
import ActivityWarning from './ActivityWarning'
import ObjectArchiveControls from './ObjectArchiveControls'
import ActivityPage from './ActivityPage'
import { request } from '../hooks/apiClient'
import type { ObjectActivityEvent } from '../types/objectActivity'
vi.mock('../hooks/apiClient', () => ({ request: vi.fn() }))
const event: ObjectActivityEvent = { id: 'one', project_id: 'p', resource_type: 'script', resource_id: 's', resource_name: 'Checkout', actor_type: 'user', actor_subject: 'user:alice', actor_display_name: 'Alice', action: 'delete', status: 'success', created_at: '2026-01-01T00:00:00Z', deleted: true }
let container: HTMLDivElement; let root: Root
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
describe('object history', () => {
  it('keeps deleted identity inspectable without linking to a missing object', async () => {
    await act(async () => root.render(<ActivityTable events={[event]} />))
    expect(container.textContent).toContain('Checkout (deleted)'); expect(container.textContent).toContain('Alice'); expect(container.querySelector('a')).toBeNull()
  })
  it('loads scoped history on demand and displays retained attribution', async () => {
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: false, activity: { creator: { ...event, action: 'create' } } })
    await act(async () => root.render(<ObjectHistory projectId="p" resourceType="script" resourceId="s" />))
    expect(container.textContent).toContain('Created by Unknown'); expect(request).not.toHaveBeenCalled()
    await act(async () => { const details = container.querySelector('details')!; details.open = true; details.dispatchEvent(new Event('toggle')) })
    expect(request).toHaveBeenCalledWith('/projects/p/activity?resource_type=script&resource_id=s&limit=25&offset=0')
    expect(container.textContent).toContain('Created by Alice'); expect(container.textContent).toContain('Checkout (deleted)')
  })
  it('shows history at once, with no disclosure, when expanded', async () => {
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: false, activity: { creator: { ...event, action: 'create' } } })
    await act(async () => root.render(<ObjectHistory projectId="p" resourceType="script" resourceId="s" expanded />))
    expect(request).toHaveBeenCalledWith('/projects/p/activity?resource_type=script&resource_id=s&limit=25&offset=0')
    expect(container.querySelector('details')).toBeNull()
    expect(container.textContent).toContain('Checkout (deleted)')
  })
  it('refreshes attribution after a saved change while history is closed', async () => {
    const creator = { ...event, action: 'create' }
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: false, activity: { creator } })
    await act(async () => root.render(<ObjectHistory projectId="p" resourceType="script" resourceId="s" activity={{ creator }} />))
    await act(async () => { const details = container.querySelector('details')!; details.open = true; details.dispatchEvent(new Event('toggle')) })
    await act(async () => { const details = container.querySelector('details')!; details.open = false; details.dispatchEvent(new Event('toggle')) })
    const modifier = { ...event, action: 'modify', actor_display_name: 'Bob' }
    await act(async () => { root.render(<ObjectHistory projectId="p" resourceType="script" resourceId="s" activity={{ creator, last_modifier: modifier }} />); window.dispatchEvent(new Event('apexload:activity-changed')) })
    expect(container.textContent).toContain('Last modified by Bob')
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('starts at the first history page when the object changes', async () => {
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: true })
    await act(async () => root.render(<ObjectHistory projectId="p" resourceType="script" resourceId="s" />))
    await act(async () => { const details = container.querySelector('details')!; details.open = true; details.dispatchEvent(new Event('toggle')) })
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Older history')!.click())
    expect(vi.mocked(request).mock.calls.slice(-1)[0]?.[0]).toContain('offset=25')
    await act(async () => root.render(<ObjectHistory projectId="p" resourceType="scenario" resourceId="other" />))
    expect(vi.mocked(request).mock.calls.slice(-1)[0]?.[0]).toBe('/projects/p/activity?resource_type=scenario&resource_id=other&limit=25&offset=0')
    expect(container.textContent).toContain('Showing 1–1 · more available')
  })

  it('starts at the first activity page when the project changes', async () => {
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: true })
    await act(async () => root.render(<ActivityPage projectId="p" />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Older events')!.click())
    expect(vi.mocked(request).mock.calls.slice(-1)[0]?.[0]).toContain('offset=50')
    await act(async () => root.render(<ActivityPage projectId="other" />))
    expect(vi.mocked(request).mock.calls.slice(-1)[0]?.[0]).toBe('/projects/other/activity?limit=50&offset=0')
    expect(container.textContent).toContain('Showing 1–1 · more available')
  })

  it('archives with the source version and refreshes after success', async () => {
    vi.mocked(request).mockResolvedValue({}); const changed = vi.fn()
    await act(async () => root.render(<ObjectArchiveControls resourceType="script" resourceId="s" updatedAt="version" onChanged={changed} canManage />))
    await act(async () => container.querySelector('button')!.click())
    expect(request).toHaveBeenCalledWith('/scripts/s/archive', { method: 'POST', body: JSON.stringify({ updated_at: 'version' }) }); expect(changed).toHaveBeenCalledOnce()
  })
  it('keeps archive controls unavailable to readers and missing versions', async () => {
    await act(async () => root.render(<ObjectArchiveControls resourceType="scenario" resourceId="s" onChanged={vi.fn()} canManage={false} />))
    expect(container.querySelector('button')).toBeNull()
    await act(async () => root.render(<ObjectArchiveControls resourceType="scenario" resourceId="s" onChanged={vi.fn()} canManage />))
    expect(container.querySelector('button')!.disabled).toBe(true)
  })
  it('reports audit failure as a saved change', async () => {
    await act(async () => root.render(<ActivityWarning />))
    await act(async () => window.dispatchEvent(new Event('apexload:activity-warning')))
    expect(container.textContent).toContain('Change saved; activity history could not be recorded.')
    await act(async () => container.querySelector('button')!.click()); expect(container.textContent).toBe('')
  })
})

describe('activity location', () => {
  it('restores filters and page from its query and reports changes back', async () => {
    vi.mocked(request).mockResolvedValue({ events: [event], has_more: false })
    const onQueryChange = vi.fn()
    await act(async () => root.render(<ActivityPage projectId="p" query="action=stop&q=checkout&page=3" onQueryChange={onQueryChange} />))
    const url = vi.mocked(request).mock.calls.slice(-1)[0]?.[0] as string
    expect(url).toContain('offset=100')
    expect(url).toContain('action=stop')
    expect(url).toContain('q=checkout')
    expect(container.textContent).toContain('Showing 101–101')
    expect(Array.from(container.querySelectorAll('option')).map(option => option.textContent)).toContain('Stopped')
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Newer events')!.click())
    expect(onQueryChange).toHaveBeenLastCalledWith('q=checkout&action=stop&page=2')
  })
})
