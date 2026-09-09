import { useState } from 'react'
import { request } from '../hooks/apiClient'

export default function RedactionPreview({ rules }: { rules: { name?: string; pattern: string; replacement?: string }[] }) {
  const [sample, setSample] = useState('customer-123 password=example')
  const [result, setResult] = useState<{ key: string; value: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const key = JSON.stringify({ sample, rules })
  async function preview() {
    setBusy(true); setError('')
    try {
      const response = await request<{ preview: string }>('/system/redaction/preview', { method: 'POST', body: key })
      setResult({ key, value: response.preview })
    } catch (error) { setError(error instanceof Error ? error.message : 'Unable to preview rules') }
    finally { setBusy(false) }
  }
  return <details><summary>Validate rules and preview redaction</summary><p>Uses the runtime’s rule validator and built-in protections. Use synthetic sample text; this does not save your policy.</p><label>Sample text<textarea className="settings-input" value={sample} maxLength={16384} onChange={event => { setSample(event.target.value); setError('') }} /></label><button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void preview()}>Validate and preview</button>{error && <p role="alert">{error}</p>}{result?.key === key && <pre aria-label="Redacted sample">{result.value}</pre>}</details>
}
