// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { DeviceCapture } from '../hooks/api/deviceCaptureApi'
import DeviceCapturePanel from './DeviceCapturePanel'
import { ApiError } from '../hooks/apiClient'

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

  const prepare = async (commit = vi.fn(), onImported = vi.fn()) => {
    agent.getState.mockResolvedValue({ interfaces: [{ name: 'en0', address: '192.168.1.20' }], capture: capture({ status: 'stopped' }) })
    agent.previewConversion.mockResolvedValue({ scripts: [script] })
    agent.attest.mockImplementation(async () => ({ scripts: [{ ...script, project_id: 'p1', metadata: { import: { preserved: { server_import_attestation_v1: `token-${agent.attest.mock.calls.length}` } } } }] }))
    await act(async () => root.render(<DeviceCapturePanel connection={connection} projectId="p1" folderId="f1" commitGenericImportBatch={commit} onImported={onImported} />))
    await act(async () => button('Convert to script').click())
    return { commit, onImported }
  }

  it('retains the original folder, project and token after response loss, including a later validation error', async () => {
    const { commit } = await prepare()
    commit.mockRejectedValueOnce(new Error('response lost'))
    await act(async () => button('Save to script library').click())
    expect(button('Convert to script').disabled).toBe(true)
    expect(button('Start device capture').disabled).toBe(true)
    const original = JSON.parse(JSON.stringify(commit.mock.calls[0][0]))

    await act(async () => root.render(<DeviceCapturePanel connection={connection} projectId="p2" folderId="f2" commitGenericImportBatch={commit} />))
    expect(button('Save to script library').disabled).toBe(true)
    expect(container.textContent).toContain('A save is pending in project p1')
    await act(async () => root.render(<DeviceCapturePanel connection={connection} projectId="p1" folderId="f2" commitGenericImportBatch={commit} />))
    commit.mockRejectedValueOnce(new ApiError('folder not found', 404))
    await act(async () => button('Save to script library').click())
    expect(button('Convert to script').disabled).toBe(true)
    commit.mockResolvedValueOnce({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(commit.mock.calls.map(call => call[0])).toEqual([original, original, original])
    expect(agent.attest).toHaveBeenCalledTimes(1)
    expect(button('Saved').disabled).toBe(true)
  })

  it('retries the controller save after re-pairing the Recording Agent', async () => {
    const { commit } = await prepare()
    commit.mockRejectedValueOnce(new Error('response lost'))
    await act(async () => button('Save to script library').click())
    const original = commit.mock.calls[0][0]
    await act(async () => root.render(<DeviceCapturePanel connection={{ ...connection, session_id: 'new-pairing', token: 'new-token' }} projectId="p1" commitGenericImportBatch={commit} />))
    expect(button('Save to script library').disabled).toBe(false)
    commit.mockResolvedValueOnce({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(commit.mock.calls[1][0]).toEqual(original)
    expect(agent.attest).toHaveBeenCalledTimes(1)
  })

  it.each(['project', 'project round trip', 'agent'] as const)('discards an attestation after its %s changes before submission', async change => {
    const { commit } = await prepare()
    let resolve!: (value: unknown) => void
    agent.attest.mockImplementationOnce(() => new Promise(yes => { resolve = yes }))
    await act(async () => button('Save to script library').click())
    await act(async () => root.render(<DeviceCapturePanel connection={change === 'agent' ? { ...connection, token: 'rotated' } : connection} projectId={change.startsWith('project') ? 'p2' : 'p1'} commitGenericImportBatch={commit} />))
    if (change === 'project round trip') await act(async () => root.render(<DeviceCapturePanel connection={connection} projectId="p1" commitGenericImportBatch={commit} />))
    await act(async () => resolve({ scripts: [script] }))
    expect(commit).not.toHaveBeenCalled()
    expect(container.querySelector('[data-testid="device-capture-review"]')).toBeNull()
  })

  it('does not publish a successful pending save into a newly selected project', async () => {
    const { commit, onImported } = await prepare()
    let resolve!: (value: unknown) => void
    commit.mockImplementationOnce(() => new Promise(yes => { resolve = yes }))
    await act(async () => button('Save to script library').click())
    await act(async () => root.render(<DeviceCapturePanel connection={connection} projectId="p2" commitGenericImportBatch={commit} onImported={onImported} />))
    await act(async () => resolve({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 }))
    expect(onImported).not.toHaveBeenCalled()
    expect(container.textContent).toContain('saved in project p1')
    expect(container.querySelector('[data-testid="device-capture-review"]')).toBeNull()
  })

  it('retries an exact expired request to recover a committed receipt', async () => {
    const { commit } = await prepare()
    agent.attest.mockResolvedValueOnce({ scripts: [script], import_draft_expires_at: new Date(Date.now() + 1000).toISOString() })
    commit.mockRejectedValueOnce(new Error('response lost'))
    await act(async () => button('Save to script library').click())
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    commit.mockResolvedValueOnce({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(commit.mock.calls[1][0]).toEqual(commit.mock.calls[0][0])
    expect(agent.attest).toHaveBeenCalledTimes(1)
  })

  it.each(['expired', 'first validation'] as const)('requires an explicit new conversion after a definite %s rejection', async rejection => {
    const { commit } = await prepare()
    commit.mockRejectedValueOnce(new ApiError(rejection === 'expired'
      ? 'imported script 1 draft is invalid or expired; run the conversion again'
      : 'folder not found', rejection === 'expired' ? 409 : 404))
    await act(async () => button('Save to script library').click())
    const originalId = commit.mock.calls[0][0].operation_id
    expect(button('Save to script library').disabled).toBe(true)
    expect(button('Convert to script').disabled).toBe(false)
    await act(async () => button('Convert to script').click())
    commit.mockResolvedValueOnce({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(commit.mock.calls[1][0].operation_id).not.toBe(originalId)
    expect(agent.attest).toHaveBeenCalledTimes(2)
  })

  it('can retry a pre-submission attestation failure without creating a save operation', async () => {
    const { commit } = await prepare()
    agent.attest.mockRejectedValueOnce(new Error('attestation unavailable'))
    await act(async () => button('Save to script library').click())
    expect(commit).not.toHaveBeenCalled()
    expect(button('Convert to script').disabled).toBe(false)
    commit.mockResolvedValueOnce({ scripts: [{ id: 'stored', name: 'iPad' }], count: 1 })
    await act(async () => button('Save to script library').click())
    expect(commit).toHaveBeenCalledTimes(1)
  })

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
    agent.attest.mockImplementation(async () => ({ scripts: [{ ...script, folder_id: 'f1', metadata: { import: { preserved: { server_import_attestation_v1: `issued-${agent.attest.mock.calls.length}` } } } }] }))
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
    expect(second).toEqual(first)
    expect(agent.attest).toHaveBeenCalledTimes(1)
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
