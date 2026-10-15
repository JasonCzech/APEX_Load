import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { BrowserRecorderBridgeConnection } from '../hooks/api/browserRecorderBridge'
import {
  attestDeviceCaptureImport,
  deviceCaptureApi,
  type DeviceCapture,
  type DeviceCaptureInterface,
} from '../hooks/api/deviceCaptureApi'
import type { StoredScript } from '../types'
import type { CommitGenericImportBatchRequest, CommitGenericImportBatchResponse, RecorderImportResult } from '../types/scriptImports'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { downloadBlob } from '../utils/fileUtils'
import RecordingMarkerControls from './RecordingMarkerControls'
import { RECORDING_REVIEW_STEP_LIMIT, recordedScriptSteps } from './recordingReviewModel'
import { createReplaySessionId } from './scriptEditorUtils'

// How often a running capture's counts refresh.
export const DEVICE_CAPTURE_POLL_MS = 2000

interface DeviceCapturePanelProps {
  connection: BrowserRecorderBridgeConnection
  projectId?: string
  folderId?: string | null
  commitGenericImportBatch: (payload: CommitGenericImportBatchRequest) => Promise<CommitGenericImportBatchResponse>
  onImported?: (result: { scripts: StoredScript[] }) => void
}

function hostList(value: string): string[] {
  return value.split(/[\s,]+/).map(host => host.trim()).filter(Boolean)
}

/**
 * Captures a phone or tablet through the Recording Agent: the agent's proxy
 * listens on the workstation's network address with a fresh password, the
 * device scans the QR code to install the certificate and sets its Wi-Fi
 * proxy, and every request it sends is counted here. The capture converts on
 * the agent and is saved to this dashboard's library.
 */
export default function DeviceCapturePanel({
  connection,
  projectId,
  folderId,
  commitGenericImportBatch,
  onImported,
}: DeviceCapturePanelProps) {
  const api = useMemo(() => deviceCaptureApi(connection), [connection])
  const [interfaces, setInterfaces] = useState<DeviceCaptureInterface[]>([])
  const [capture, setCapture] = useState<DeviceCapture | null>(null)
  const [address, setAddress] = useState('')
  const [name, setName] = useState('Device capture')
  const [includeHosts, setIncludeHosts] = useState('')
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState<{ text: string; tone: 'error' | 'success' } | null>(null)
  const [qrURL, setQrURL] = useState('')
  const [preview, setPreview] = useState<RecorderImportResult | null>(null)
  const [saved, setSaved] = useState<StoredScript[] | null>(null)
  // A running capture's last refresh failed: the agent may have stopped.
  const [agentSilent, setAgentSilent] = useState(false)
  // The save's batch id is kept across a retry of the same preview, so a
  // save whose response was lost does not store the scripts twice.
  const saveOperation = useRef<{ preview: RecorderImportResult; id: string } | null>(null)

  const refresh = useCallback(async () => {
    const state = await api.getState()
    setInterfaces(state.interfaces)
    setCapture(state.capture)
    setAgentSilent(false)
    // A network change can take the chosen address away.
    const listed = (candidate?: string) => !!candidate && state.interfaces.some(iface => iface.address === candidate)
    setAddress(current => listed(current)
      ? current
      : listed(state.capture?.host) ? state.capture!.host : state.interfaces[0]?.address || '')
  }, [api])

  useEffect(() => {
    refresh().catch((error: unknown) => setMessage({ text: getErrorMessage(error, 'The Recording Agent did not answer'), tone: 'error' }))
  }, [refresh])

  const recording = capture?.status === 'recording' || capture?.status === 'paused'
  useEffect(() => {
    if (!recording) return
    const timer = window.setInterval(() => {
      refresh().catch(() => setAgentSilent(true))
    }, DEVICE_CAPTURE_POLL_MS)
    return () => window.clearInterval(timer)
  }, [recording, refresh])

  const setupURL = recording ? capture?.setup_url : ''
  useEffect(() => {
    if (!setupURL) {
      setQrURL('')
      return
    }
    let objectURL = ''
    let current = true
    api.qrCode().then(blob => {
      if (!current) return
      objectURL = URL.createObjectURL(blob)
      setQrURL(objectURL)
    }).catch(() => {
      if (current) setQrURL('')
    })
    return () => {
      current = false
      if (objectURL) URL.revokeObjectURL(objectURL)
    }
  }, [api, setupURL])

  const run = async (action: () => Promise<void>, failure: string) => {
    setBusy(true)
    setMessage(null)
    try {
      await action()
    } catch (error) {
      setMessage({ text: getErrorMessage(error, failure), tone: 'error' })
    } finally {
      setBusy(false)
    }
  }

  const start = () => run(async () => {
    const res = await api.start({ name: name.trim() || 'Device capture', address, include_hosts: hostList(includeHosts) })
    setCapture(res.capture)
    setPreview(null)
    setSaved(null)
    saveOperation.current = null
  }, 'The device capture did not start')

  const stop = () => run(async () => {
    const res = await api.stop()
    setCapture(res.capture)
    setMessage({ text: 'Capture stopped. On the device, set Configure Proxy back to Off.', tone: 'success' })
  }, 'The device capture did not stop')

  const convert = () => run(async () => {
    if (!capture) return
    setSaved(null)
    setPreview(await api.previewConversion(capture.session_id, capture.name))
    saveOperation.current = null
  }, 'The capture did not convert')

  const save = () => run(async () => {
    if (!preview) return
    const scripts = preview.scripts?.length ? preview.scripts : preview.script ? [preview.script] : []
    if (scripts.length === 0) throw new Error('The conversion produced no script to save')
    const attested = await attestDeviceCaptureImport(scripts, projectId)
    const remote = attested.scripts?.length ? attested.scripts : attested.script ? [attested.script] : []
    if (remote.length !== scripts.length) throw new Error('The controller did not accept every converted script')
    if (saveOperation.current?.preview !== preview) {
      saveOperation.current = { preview, id: `device-capture-${createReplaySessionId()}` }
    }
    const response = await commitGenericImportBatch({
      scripts: remote,
      folder_id: remote[0]?.folder_id ?? folderId ?? null,
      operation_id: saveOperation.current.id,
      source_profile: 'browser_recorder',
    })
    saveOperation.current = null
    setSaved(response.scripts)
    onImported?.({ scripts: response.scripts })
    setMessage({
      text: response.scripts.length === 1
        ? `Script "${response.scripts[0].name}" saved to the script library`
        : `Saved ${response.scripts.length} scripts to the script library`,
      tone: 'success',
    })
  }, 'The script was not saved')

  const exportHAR = () => run(async () => {
    if (!capture) return
    const res = await api.exportHAR(capture.session_id)
    if (!res.ok) throw new Error(`The Recording Agent did not export the capture (${res.status})`)
    await downloadBlob(await res.blob(), `${capture.name || 'device-capture'}.har`)
  }, 'The capture did not export')

  const copy = async (value: string) => {
    if (!(await copyTextToClipboard(value))) {
      setMessage({ text: 'Copy did not work in this browser; select the password and copy it.', tone: 'error' })
    }
  }

  const marker = async (send: () => Promise<unknown>) => {
    try {
      await send()
      await refresh()
      return true
    } catch (error) {
      setMessage({ text: getErrorMessage(error, 'The marker was not added'), tone: 'error' })
      return false
    }
  }

  const steps = useMemo(() => {
    const script = preview?.scripts?.[0] ?? preview?.script
    return recordedScriptSteps(script)
  }, [preview])
  // Credentials the conversion found keep their recorded values.
  const credentials = preview?.secret_binding_requirements || []

  return (
    <div className="rec-controls-card rec-device-capture" data-testid="device-capture-panel">
      <div className="rec-option-heading">
        <h3 className="rec-section-title">Capture a phone or tablet</h3>
        <p>
          The Recording Agent records the device's apps through a proxy on this workstation.
          The device must be on the same network.
        </p>
      </div>

      {message && <div className={`rec-feedback ${message.tone}`} role={message.tone === 'error' ? 'alert' : 'status'}>{message.text}</div>}

      {!recording && (
        <div className="rec-start-form">
          <label className="rec-form-row">
            <span className="rec-label">Network address</span>
            <select className="rec-input" value={address} onChange={e => setAddress(e.target.value)} aria-label="Network address">
              {interfaces.length === 0 && <option value="">No network address found</option>}
              {interfaces.map(iface => (
                <option key={`${iface.name}-${iface.address}`} value={iface.address}>{iface.address} ({iface.name})</option>
              ))}
            </select>
          </label>
          <label className="rec-form-row">
            <span className="rec-label">Capture name</span>
            <input className="rec-input" value={name} onChange={e => setName(e.target.value)} aria-label="Capture name" />
          </label>
          <label className="rec-form-row">
            <span className="rec-label">Record only these hosts</span>
            <input
              className="rec-input"
              value={includeHosts}
              onChange={e => setIncludeHosts(e.target.value)}
              placeholder="salesforce.com, force.com (optional)"
              aria-label="Record only these hosts"
            />
          </label>
          <button type="button" className="btn btn-primary" onClick={() => void start()} disabled={busy || !address}>
            Start device capture
          </button>
        </div>
      )}

      {recording && capture && (
        <div className="rec-device-live">
          <div className="rec-device-setup">
            {qrURL && <img className="rec-device-qr" src={qrURL} alt={`QR code for ${capture.setup_url}`} width={180} height={180} />}
            <dl className="rec-device-settings">
              <dt>Setup page</dt><dd><code>{capture.setup_url}</code></dd>
              <dt>Server</dt><dd><code>{capture.host}</code></dd>
              <dt>Port</dt><dd><code>{capture.port}</code></dd>
              <dt>Username</dt><dd><code>{capture.username}</code></dd>
              <dt>Password</dt>
              <dd>
                <code>{capture.password}</code>{' '}
                <button type="button" className="rec-inline-action" onClick={() => void copy(capture.password)}>Copy</button>
              </dd>
            </dl>
          </div>
          <ol className="rec-device-steps">
            <li>On the device, scan the QR code (or open the setup page) and tap <strong>Download the certificate</strong>, then <strong>Allow</strong>.</li>
            <li>Open <strong>Settings › General › VPN &amp; Device Management</strong> and install <strong>APEX Load Recording Proxy CA</strong>.</li>
            <li>Open <strong>Settings › General › About › Certificate Trust Settings</strong> and turn on full trust for it.</li>
            <li>Open <strong>Settings › Wi-Fi</strong>, tap <strong>ⓘ</strong> beside the network, then <strong>Configure Proxy › Manual</strong>: the server and port above, <strong>Authentication</strong> on, and the username and password above.</li>
            <li>Open the app and use it. Name each step below as you perform it.</li>
          </ol>
          {agentSilent && (
            <p className="rec-feedback error" role="alert">
              The Recording Agent stopped answering; these counts may be out of date. Check that the agent is still running.
            </p>
          )}
          <p className="rec-device-count" role="status" aria-live="polite" data-testid="device-capture-count">
            <strong>{capture.request_count}</strong> {capture.request_count === 1 ? 'request' : 'requests'} recorded from the device
            {capture.last_host ? ` (latest: ${capture.last_host})` : ''}
            {(capture.filtered_requests || 0) > 0 && `; ${capture.filtered_requests} left out by the host filter (latest: ${capture.last_filtered_host})`}
            {(capture.rejected_requests || 0) > 0 && `; ${capture.rejected_requests} refused for a private address (latest: ${capture.last_rejected_host})`}
          </p>
          <RecordingMarkerControls
            openTransactions={capture.open_transactions || []}
            onStartTransaction={transaction => marker(() => api.startTransaction(capture.session_id, transaction))}
            onEndTransaction={transaction => marker(() => api.endTransaction(capture.session_id, transaction))}
            onAddNote={text => marker(() => api.addNote(capture.session_id, text))}
            onInvalid={text => setMessage({ text, tone: 'error' })}
          />
          <button type="button" className="btn btn-secondary" onClick={() => void stop()} disabled={busy}>
            Stop capture
          </button>
        </div>
      )}

      {capture?.status === 'stopped' && (
        <div className="rec-device-stopped">
          <p>
            <strong>{capture.name}</strong>: {capture.request_count} {capture.request_count === 1 ? 'request' : 'requests'} recorded.
          </p>
          {(capture.warnings || []).length > 0 && (
            <ul className="rec-device-warnings">
              {(capture.warnings || []).map(warning => <li key={warning}>{warning}</li>)}
            </ul>
          )}
          <div className="rec-result-actions">
            <button type="button" className="btn btn-primary" onClick={() => void convert()} disabled={busy || capture.request_count === 0}>
              Convert to script
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => void exportHAR()} disabled={busy}>
              Export HAR
            </button>
          </div>
        </div>
      )}

      {preview && (
        <div className="rec-device-review" data-testid="device-capture-review">
          {(preview.warnings || []).length > 0 && (
            <ul className="rec-device-warnings">
              {preview.warnings.map(warning => <li key={warning}>{warning}</li>)}
            </ul>
          )}
          {credentials.length > 0 && (
            <p className="rec-hint" data-testid="device-capture-credentials">
              The script keeps {credentials.length === 1 ? 'a recorded credential' : `${credentials.length} recorded credentials`} as
              recorded ({Array.from(new Set(credentials.map(requirement => requirement.label))).join(', ')}). To keep them in
              project secrets instead, change them in the script editor after saving.
            </p>
          )}
          {steps.length > 0 && (
            <div className="rec-detail-table-wrap rec-review-steps">
              <table className="rec-detail-table" aria-label="Converted steps">
                <thead>
                  <tr><th>#</th><th>Step</th><th>Method</th><th>Target</th><th>Transaction</th></tr>
                </thead>
                <tbody>
                  {steps.slice(0, RECORDING_REVIEW_STEP_LIMIT).map(step => (
                    <tr key={step.index}>
                      <td className="cell-mono">{step.index}</td>
                      <td>{step.name}</td>
                      <td className="cell-mono">{step.method}</td>
                      <td className="cell-mono rec-review-target" title={step.target}>{step.target}</td>
                      <td>{step.transaction}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {steps.length > RECORDING_REVIEW_STEP_LIMIT && (
                <p className="rec-hint">And {steps.length - RECORDING_REVIEW_STEP_LIMIT} more steps; open the script in the editor to see them all.</p>
              )}
            </div>
          )}
          <button type="button" className="btn btn-primary" onClick={() => void save()} disabled={busy || !!saved}>
            {saved ? 'Saved' : 'Save to script library'}
          </button>
        </div>
      )}
    </div>
  )
}
