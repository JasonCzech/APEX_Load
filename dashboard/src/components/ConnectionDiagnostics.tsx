import { useState } from 'react'
import { request } from '../hooks/apiClient'
import { settingsHref } from './settingsNavigation'

export default function ConnectionDiagnostics({ provider, revision, saved }: { provider: string; revision?: string; saved: boolean }) {
  const [result, setResult] = useState<{ revision?: string; message: string } | null>(null)
  const [busy, setBusy] = useState(false)
  async function test() {
    setBusy(true)
    try {
      const response = await request<{ message: string; checked_at: string }>('/system/settings/connections/test', { method: 'POST', body: JSON.stringify({ provider, expected_revision: revision }) })
      setResult({ revision, message: `${response.message} Checked ${new Date(response.checked_at).toLocaleString()}` })
    } catch (error) { setResult({ revision, message: `${error instanceof Error ? error.message : 'Connection test failed'}. Check the saved URL, controller credential environment, TLS and provider read permissions.` }) }
    finally { setBusy(false) }
  }
  return <div className="settings-subsection"><p>Credentials come from the controller environment. Save configuration first; testing checks the saved connection’s API access. It does not establish that metrics are arriving.</p><button type="button" className="btn btn-secondary" disabled={busy || !saved || !revision} onClick={() => void test()}>Test saved connection</button> <a href={settingsHref('dashboards')}>Import or view dashboards</a>{result?.revision === revision && saved && <p role="status">{result?.message}</p>}</div>
}
