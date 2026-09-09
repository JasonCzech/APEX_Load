import { useState } from 'react'
import { request } from '../hooks/apiClient'

export default function NotificationDiagnostics({ format, index, revision, saved, eventType = 'test_complete' }: { format: string; index: number; revision?: string; saved: boolean; eventType?: string }) {
  const [preview, setPreview] = useState<{ key: string; payload: unknown } | null>(null)
  const previewKey = JSON.stringify({ format, eventType })
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [deliveries, setDeliveries] = useState<{ name: string; status: string; finished_at: string }[] | null>(null)
  async function perform(action: 'preview' | 'test' | 'deliveries') {
    if (busy) return
    setBusy(true); setMessage('')
    try {
      const result = await request<{ payload?: unknown; message?: string; deliveries?: { name: string; status: string; finished_at: string }[] }>(`/system/settings/notifications/${action}`, action === 'deliveries' ? undefined : { method: 'POST', body: JSON.stringify(action === 'test' ? { index, expected_revision: revision } : { format, event_type: eventType }) })
      if (action === 'preview') setPreview({ key: previewKey, payload: result.payload })
      if (action === 'deliveries') setDeliveries(result.deliveries || [])
      setMessage(result.message || (action === 'preview' ? 'Preview only; nothing was sent.' : 'Delivery history refreshed.'))
    } catch (error) { setMessage(error instanceof Error ? error.message : 'Request failed') }
    finally { setBusy(false) }
  }
  return <div className="settings-subsection">
    <div className="settings-inline-actions"><button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void perform('preview')}>Preview sample payload</button><button className="btn btn-secondary" type="button" disabled={busy || !saved || !revision} onClick={() => void perform('test')}>Send test to saved destination</button><button className="btn btn-secondary" type="button" disabled={busy} onClick={() => void perform('deliveries')}>Refresh recent deliveries</button></div>
    <p>Testing sends an explicitly marked sample to the enabled, saved route. Save or discard changes first. History covers this controller’s latest 50 completed attempts and clears on restart.</p>
    {message && <p role="status">{message}</p>}
    {preview?.key === previewKey && <details open><summary>Sample payload</summary><pre>{JSON.stringify(preview?.payload, null, 2)}</pre></details>}
    {deliveries && <ul>{deliveries.length ? deliveries.map((row, position) => <li key={position}>{row.name || 'Unnamed route'}: {row.status} · {new Date(row.finished_at).toLocaleString()}</li>) : <li>No completed deliveries on this controller.</li>}</ul>}
  </div>
}
