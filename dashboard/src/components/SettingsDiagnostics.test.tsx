// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import NotificationDiagnostics from './NotificationDiagnostics'
import ConnectionDiagnostics from './ConnectionDiagnostics'
import RedactionPreview from './RedactionPreview'
import MetricPackPicker from './MetricPackPicker'
const { request, packs } = vi.hoisted(() => ({ request: vi.fn(), packs: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('../hooks/api/useSystemApi', () => ({ useSystemApi: () => ({ getInfrastructureMonitorPacks: packs }) }))
let host: HTMLDivElement; let root: Root
beforeEach(() => { host = document.createElement('div'); document.body.append(host); root = createRoot(host); vi.clearAllMocks() })
afterEach(async () => { await act(async () => root.unmount()); host.remove() })
async function click(text: string) { const button = Array.from(host.querySelectorAll('button')).find(node => node.textContent === text)!; await act(async () => button.click()) }
describe('settings diagnostics', () => {
  it('previews drafts without delivery and requires saved revision before a test', async () => {
    request.mockResolvedValue({ payload: { text: 'sample' } })
    await act(async () => root.render(<NotificationDiagnostics index={0} format="slack" saved={false} revision="r1" />))
    expect(host.querySelectorAll('button')[1].disabled).toBe(true)
    await click('Preview sample payload')
    expect(request).toHaveBeenCalledWith('/system/settings/notifications/preview', expect.objectContaining({ body: JSON.stringify({ format: 'slack', event_type: 'test_complete' }) }))
    expect(host.textContent).toContain('sample')
    await act(async () => root.render(<NotificationDiagnostics index={0} format="teams" saved revision="r1" />))
    expect(host.querySelector('pre')).toBeNull()
    request.mockResolvedValue({ message: 'Test accepted' })
    await click('Send test to saved destination')
    expect(request).toHaveBeenLastCalledWith('/system/settings/notifications/test', expect.objectContaining({ body: JSON.stringify({ index: 0, expected_revision: 'r1' }) }))
  })
  it('does not show an old connection result after its revision changes', async () => {
    request.mockResolvedValue({ message: 'Connected', checked_at: '2026-09-08T12:00:00Z' })
    await act(async () => root.render(<ConnectionDiagnostics provider="grafana" saved revision="r1" />))
    await click('Test saved connection'); expect(host.textContent).toContain('Connected')
    await act(async () => root.render(<ConnectionDiagnostics provider="grafana" saved revision="r2" />))
    expect(host.textContent).not.toContain('Connected')
  })
  it('validates draft redaction through the backend and invalidates stale previews', async () => {
    request.mockResolvedValue({ preview: 'CUSTOMER' })
    await act(async () => root.render(<RedactionPreview rules={[{ pattern: 'customer-[0-9]+' }]} />))
    await click('Validate and preview'); expect(host.querySelector('pre')?.textContent).toBe('CUSTOMER')
    await act(async () => root.render(<RedactionPreview rules={[{ pattern: '[' }]} />))
    expect(host.querySelector('pre')).toBeNull()
    request.mockRejectedValue(new Error('Invalid pattern')); await click('Validate and preview')
    expect(host.querySelector('[role="alert"]')?.textContent).toBe('Invalid pattern')
  })
  it('preserves custom pack permissions when selecting catalog packs and exposes failed discovery', async () => {
    packs.mockResolvedValue({ packs: [{ id: 'linux-hosts', name: 'Linux hosts', status: 'supported', signals: ['cpu'] }] })
    const change = vi.fn()
    await act(async () => root.render(<MetricPackPicker value="custom-pack" onChange={change} />))
    await act(async () => (host.querySelector('input[type="checkbox"]') as HTMLInputElement).click())
    expect(change).toHaveBeenCalledWith('custom-pack, linux-hosts')
    await act(async () => root.unmount()); root = createRoot(host)
    packs.mockRejectedValue(new Error('Denied'))
    await act(async () => root.render(<MetricPackPicker value="custom-pack" onChange={change} />))
    expect(host.textContent).toContain('Pack catalog unavailable')
    expect((host.querySelector('input') as HTMLInputElement).value).toBe('custom-pack')
  })
})
