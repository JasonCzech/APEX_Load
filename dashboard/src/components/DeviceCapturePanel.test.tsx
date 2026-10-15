// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { DeviceCapture } from '../hooks/api/deviceCaptureApi'
import DeviceCapturePanel from './DeviceCapturePanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const agent = vi.hoisted(() => ({
  getState: vi.fn(),
  start: vi.fn(),
  stop: vi.fn(),
  qrCode: vi.fn(),
  startTransaction: vi.fn(),
  endTransaction: vi.fn(),
  addNote: vi.fn(),
  previewConversion: vi.fn(),
  exportHAR: vi.fn(),
  attest: vi.fn(),
}))

vi.mock('../hooks/api/deviceCaptureApi', () => ({
  deviceCaptureApi: () => agent,
  attestDeviceCaptureImport: (...args: unknown[]) => agent.attest(...args),
}))

const connection = { origin: 'http://127.0.0.1:27777', token: 't', session_id: 's', paired_at: '' }

function capture(overrides: Partial<DeviceCapture> = {}): DeviceCapture {
  return {
    session_id: 'cap-1', name: 'iPad', status: 'recording', host: '192.168.1.20', port: 8888,
    username: 'apexload', password: 'abcd-efgh-jkmn-pqrs', setup_url: 'http://192.168.1.20:8888/',
    ca_url: 'http://192.168.1.20:8888/apexload-ca.crt', started_at: '2026-10-15T00:00:00Z',
    request_count: 3, last_host: 'acme.my.salesforce.com', filtered_requests: 2, last_filtered_host: 'apple.com',
    open_transactions: [], ...overrides,
  }
}

const script = {
  name: 'iPad', version: '1', protocol: 'http',
  actions: [{ name: 'limits', type: 'request', method: 'GET', target: '/services/data/v66.0/limits' }],
}

describe('DeviceCapturePanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.useFakeTimers()
    Object.values(agent).forEach(fn => fn.mockReset())
    URL.createObjectURL = vi.fn(() => 'blob:qr')
    URL.revokeObjectURL = vi.fn()
    agent.qrCode.mockResolvedValue(new Blob(['<svg/>'], { type: 'image/svg+xml' }))
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    vi.useRealTimers()
  })

  const button = (label: string) =>
    Array.from(container.querySelectorAll('button')).find(b => b.textContent === label) as HTMLButtonElement

  it('starts a capture, shows the device setup and counts, then converts and saves', async () => {
    agent.getState.mockResolvedValue({ interfaces: [{ name: 'en0', address: '192.168.1.20' }], capture: null })
    agent.start.mockResolvedValue({ capture: capture() })
    const commit = vi.fn()
    const onImported = vi.fn()
    await act(async () => root.render(
      <DeviceCapturePanel connection={connection} projectId="p1" folderId="f1" commitGenericImportBatch={commit} onImported={onImported} />,
    ))
    expect((container.querySelector('select') as HTMLSelectElement).value).toBe('192.168.1.20')

    await act(async () => button('Start device capture').click())
    expect(agent.start).toHaveBeenCalledWith({ name: 'Device capture', address: '192.168.1.20', include_hosts: [] })
    expect(container.textContent).toContain('abcd-efgh-jkmn-pqrs')
    expect(container.querySelector('img')?.getAttribute('src')).toBe('blob:qr')
    expect(container.querySelector('[data-testid="device-capture-count"]')?.textContent)
      .toBe('3 requests recorded from the device (latest: acme.my.salesforce.com); 2 left out by the host filter (latest: apple.com)')

    // The counts refresh while the capture runs.
    agent.getState.mockResolvedValue({ interfaces: [], capture: capture({ request_count: 5 }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(container.querySelector('[data-testid="device-capture-count"]')?.textContent).toContain('5 requests')

    agent.stop.mockResolvedValue({ capture: capture({ status: 'stopped', password: '', request_count: 5, warnings: ['Transaction "Login" was closed automatically'] }) })
    await act(async () => button('Stop capture').click())
    expect(container.textContent).toContain('set Configure Proxy back to Off')
    expect(container.textContent).toContain('Transaction "Login" was closed automatically')
    expect(container.querySelector('img')).toBeNull()

    agent.previewConversion.mockResolvedValue({ scripts: [script], warnings: ['The Salesforce sign-in runs in init (13 steps)'] })
    await act(async () => button('Convert to script').click())
    expect(agent.previewConversion).toHaveBeenCalledWith('cap-1', 'iPad')
    expect(container.querySelector('[data-testid="device-capture-review"]')?.textContent).toContain('/services/data/v66.0/limits')

    // A save whose response is lost is retried with the same batch id.
    agent.attest.mockResolvedValue({ scripts: [{ ...script, folder_id: 'f1' }] })
    commit.mockRejectedValueOnce(new Error('the batch save failed'))
    await act(async () => button('Save to script library').click())
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('the batch save failed')
    commit.mockResolvedValueOnce({ scripts: [{ id: 's1', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(agent.attest).toHaveBeenCalledWith([script], 'p1')
    expect(commit).toHaveBeenCalledTimes(2)
    const [first, second] = commit.mock.calls.map(call => call[0])
    expect(second).toMatchObject({ folder_id: 'f1', source_profile: 'browser_recorder' })
    expect(second.operation_id).toBe(first.operation_id)
    expect(onImported).toHaveBeenCalledWith({ scripts: [{ id: 's1', name: 'iPad' }] })
    expect(button('Saved').disabled).toBe(true)
  })

  it('takes the recording profile selected in the Studio to the agent', async () => {
    agent.getState.mockResolvedValue({ interfaces: [{ name: 'en0', address: '192.168.1.20' }], capture: null })
    const profile = { id: 'mobile', name: 'Mobile app', background_requests: ['telemetry/upload'] }
    await act(async () => root.render(
      <DeviceCapturePanel connection={connection} commitGenericImportBatch={vi.fn()} recordingProfile={profile} />,
    ))
    expect(container.querySelector('[data-testid="device-capture-profile"]')?.textContent).toContain('Mobile app')
    agent.start.mockResolvedValue({ capture: capture() })
    await act(async () => button('Start device capture').click())
    expect(agent.start).toHaveBeenLastCalledWith(expect.objectContaining({ recording_profile: profile }))
  })

  it('follows the network, says when the agent goes quiet, and notes kept credentials', async () => {
    const a = { name: 'en0', address: '192.168.1.20' }
    const b = { name: 'en1', address: '10.0.0.5' }
    agent.getState.mockResolvedValue({ interfaces: [a, b], capture: null })
    await act(async () => root.render(
      <DeviceCapturePanel connection={connection} commitGenericImportBatch={vi.fn()} />,
    ))
    const select = container.querySelector('select') as HTMLSelectElement
    await act(async () => {
      select.value = b.address
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    agent.start.mockResolvedValue({ capture: capture({ host: b.address }) })
    await act(async () => button('Start device capture').click())
    expect(agent.start).toHaveBeenLastCalledWith(expect.objectContaining({ address: b.address }))
    expect(agent.start.mock.lastCall?.[0]).not.toHaveProperty('recording_profile')

    // The agent stops answering mid-capture.
    agent.getState.mockRejectedValue(new Error('Failed to fetch'))
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('stopped answering')

    // It answers again after the workstation left the en1 network.
    agent.getState.mockResolvedValue({ interfaces: [a], capture: capture({ host: b.address, status: 'stopped', password: '' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect((container.querySelector('select') as HTMLSelectElement).value).toBe(a.address)
    await act(async () => button('Start device capture').click())
    expect(agent.start).toHaveBeenLastCalledWith(expect.objectContaining({ address: a.address }))

    // That capture ends; the conversion keeps the credentials it found.
    agent.getState.mockResolvedValue({ interfaces: [a], capture: capture({ status: 'stopped', password: '' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    agent.previewConversion.mockResolvedValue({
      scripts: [script], warnings: [],
      secret_binding_requirements: [{ label: 'Authorization request header' }, { label: 'Request body' }, { label: 'Request body' }],
    })
    await act(async () => button('Convert to script').click())
    expect(container.querySelector('[data-testid="device-capture-credentials"]')?.textContent)
      .toContain('3 recorded credentials as recorded (Authorization request header, Request body)')
  })
})
