import type { RecorderImportResult } from '../../types/scriptImports'
import { request } from '../apiClient'
import { bridgeFetch, bridgeRequest, type BrowserRecorderBridgeConnection } from './browserRecorderBridge'

// A network interface of the Recording Agent's workstation that a phone or
// tablet on the same network can reach.
export type DeviceCaptureInterface = {
  name: string
  address: string
}

// The device capture the Recording Agent runs: its recording proxy listens on
// host:port for the device, which signs in with username and password, and
// downloads the agent's certificate from ca_url (setup_url shows how).
export type DeviceCapture = {
  session_id: string
  name: string
  status: 'recording' | 'paused' | 'stopped'
  host: string
  port: number
  username: string
  password: string
  setup_url: string
  ca_url: string
  started_at: string
  stopped_at?: string
  // Requests recorded from the device, and the ones the proxy refused or
  // the host filters left out.
  request_count: number
  rejected_requests?: number
  last_rejected_host?: string
  filtered_requests?: number
  last_filtered_host?: string
  last_host?: string
  open_transactions?: string[]
  // The capture's warnings, such as a transaction the stop closed.
  warnings?: string[]
}

export type DeviceCaptureState = {
  interfaces: DeviceCaptureInterface[]
  capture: DeviceCapture | null
}

export type DeviceCaptureStartRequest = {
  name: string
  address: string
  port?: number
  include_hosts?: string[]
  exclude_hosts?: string[]
}

// The device capture calls go to the paired Recording Agent; saving a
// reviewed script goes to this dashboard's controller.
export function deviceCaptureApi(connection: BrowserRecorderBridgeConnection) {
  const json = (body: unknown): RequestInit => ({ method: 'POST', body: JSON.stringify(body) })
  return {
    getState: () => bridgeRequest<DeviceCaptureState>(connection, '/device-capture'),
    start: (req: DeviceCaptureStartRequest) =>
      bridgeRequest<{ capture: DeviceCapture }>(connection, '/device-capture', json(req)),
    stop: () => bridgeRequest<{ capture: DeviceCapture }>(connection, '/device-capture/stop', json({})),
    // The QR code of the setup page, as an SVG the caller shows as an image.
    qrCode: async (): Promise<Blob> => {
      const res = await bridgeFetch(connection, '/device-capture/qr.svg')
      if (!res.ok) throw new Error(`The Recording Agent did not return the QR code (${res.status})`)
      return res.blob()
    },
    startTransaction: (sessionId: string, name: string) =>
      bridgeRequest(connection, `/recorder/${encodeURIComponent(sessionId)}/transactions/start`, json({ name })),
    endTransaction: (sessionId: string, name: string) =>
      bridgeRequest(connection, `/recorder/${encodeURIComponent(sessionId)}/transactions/end`, json({ name })),
    addNote: (sessionId: string, text: string) =>
      bridgeRequest(connection, `/recorder/${encodeURIComponent(sessionId)}/notes`, json({ text })),
    previewConversion: (sessionId: string, name: string) =>
      bridgeRequest<RecorderImportResult>(connection, `/recorder/${encodeURIComponent(sessionId)}/convert/preview`, json({ name })),
    exportHAR: (sessionId: string) => bridgeFetch(connection, `/recorder/${encodeURIComponent(sessionId)}/export`),
  }
}

// attestDeviceCaptureImport hands a draft the agent converted to this
// dashboard's controller, which issues its own attestation for the save.
export function attestDeviceCaptureImport(scripts: RecorderImportResult['scripts'], projectId?: string) {
  return request<RecorderImportResult>('/scripts/import/browser-recorder/attest', {
    method: 'POST',
    body: JSON.stringify({ scripts, project_id: projectId, origin: 'device' }),
  })
}
