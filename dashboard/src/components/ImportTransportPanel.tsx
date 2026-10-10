import { useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { advertisedOriginsFrom, observedSummary, policyLabel, TRANSPORT_POLICY_OPTIONS, transportEdits, transportRows } from './importTransportModel'
import './ImportTransportPanel.css'

type Props = {
  script: ScriptDefinition
  diagnostics?: ReadonlyArray<{ code?: string; location?: string }>
  disabled?: boolean
  onApplyingChange?: (applying: boolean) => void
  onApplied: (script: ScriptDefinition, importDraftExpiresAt?: string) => void
}

const KEEP = '__keep__'
const GATE_LABELS: Record<string, string> = {
  curl_http3_race_not_modeled: 'resolves the curl --http3 gate',
  http3_draft_token: 'resolves the draft HTTP/3 review',
}

/**
 * Import review's Transport panel: one row per request origin, where an
 * explicit HTTP version choice applies to every request of the origin,
 * nested ones included, after a preview. The server records provenance and
 * re-secures the draft.
 */
export function ImportTransportPanel({ script, diagnostics, disabled = false, onApplyingChange, onApplied }: Props) {
  const api = useApi()
  const rows = useMemo(() => transportRows(script, advertisedOriginsFrom(diagnostics)), [script, diagnostics])
  const [staged, setStaged] = useState<Record<string, string>>({})
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState('')
  const [announcement, setAnnouncement] = useState('')
  // A new draft (another edit, a reconversion) makes the staged choices stale.
  useEffect(() => { setStaged({}); setError('') }, [script])

  const preview = rows.flatMap(row => {
    const to = staged[row.origin]
    if (to === undefined) return []
    return transportEdits(row, to).map(edit => {
      const request = row.requests.find(candidate => candidate.path === edit.path)!
      const resolves = edit.to === '' ? [] : request.versionGates.map(code => GATE_LABELS[code]).filter(Boolean)
      return { ...edit, name: request.name, resolves }
    })
  })
  if (rows.length === 0) return null

  const apply = async () => {
    if (preview.length === 0) return
    setApplying(true)
    onApplyingChange?.(true)
    setError('')
    try {
      const response = await api.applyImportTransport({ script, edits: preview.map(({ path, origin, from, to }) => ({ path, origin, from, to })) })
      setStaged({})
      setAnnouncement(`Applied HTTP version choices to ${response.changes.length} request${response.changes.length === 1 ? '' : 's'}.`)
      onApplied(response.script, response.import_draft_expires_at)
    } catch (err) {
      setError(getErrorMessage(err, 'The HTTP version choice could not be applied.'))
    } finally {
      setApplying(false)
      onApplyingChange?.(false)
    }
  }

  const locked = disabled || applying
  return <section className="import-transport-panel" aria-label="Transport">
    <h4>Transport</h4>
    <p>One row per origin. A choice applies to every request to the origin, including requests inside sequences and branches. Captured versions stay recorded as observations.</p>
    <p className="import-transport-status" role="status">{announcement}</p>
    <table className="import-transport-table">
      <thead><tr><th scope="col">Origin</th><th scope="col">Requests</th><th scope="col">Captured</th><th scope="col">Advertises HTTP/3</th><th scope="col">HTTP version</th><th scope="col">Fidelity</th></tr></thead>
      <tbody>
        {rows.map(row => {
          const label = row.origin || 'Set at runtime'
          const value = staged[row.origin] ?? (row.policy ?? KEEP)
          const proposal = row.requests.find(request => request.proposal)?.proposal
          return <tr key={row.origin || '(runtime)'}>
            <th scope="row" data-label="Origin"><code>{label}</code></th>
            <td data-label="Requests">{row.requests.length}</td>
            <td data-label="Captured">{observedSummary(row.counts)}</td>
            <td data-label="Advertises HTTP/3">{row.advertised ? 'Yes (Alt-Svc)' : 'Not seen'}</td>
            <td data-label="HTTP version">
              <select aria-label={`HTTP version for ${label}`} value={value} disabled={locked || !row.origin}
                onChange={event => {
                  const next = event.target.value
                  setAnnouncement('')
                  setStaged(current => {
                    const updated = { ...current }
                    if (next === KEEP || next === row.policy) delete updated[row.origin]
                    else updated[row.origin] = next
                    return updated
                  })
                }}>
                {row.policy === null && <option value={KEEP}>Keep each request's policy (mixed)</option>}
                {TRANSPORT_POLICY_OPTIONS.map(option => {
                  // HTTP/3 serves only https origins; the server refuses exact 3 on http://.
                  const cleartextH3 = option.value === '3' && row.origin.startsWith('http://')
                  return <option key={option.value} value={option.value} disabled={cleartextH3}>{option.label}{cleartextH3 ? ' (https only)' : ''}</option>
                })}
              </select>
              {proposal && <small>Suggested for curl --http3: {policyLabel(proposal)} (approximate)</small>}
            </td>
            <td data-label="Fidelity">{row.fidelity}</td>
          </tr>
        })}
      </tbody>
    </table>
    {preview.length > 0 && <div className="import-transport-preview" role="region" aria-label="Transport preview">
      <h5>Preview: {preview.length === 1 ? '1 request changes' : `${preview.length} requests change`}</h5>
      <ul>
        {preview.map(change => <li key={change.path}>
          <code>{change.path}</code> {change.name}: {policyLabel(change.from)} → {policyLabel(change.to)}{change.resolves.length > 0 && <> ({change.resolves.join('; ')})</>}
        </li>)}
      </ul>
      <div className="import-transport-actions">
        <button type="button" className="btn btn-primary btn-sm" disabled={locked} onClick={() => void apply()}>{applying ? 'Applying…' : `Apply to ${preview.length} request${preview.length === 1 ? '' : 's'}`}</button>
        <button type="button" className="btn btn-secondary btn-sm" disabled={locked} onClick={() => { setStaged({}); setError('') }}>Discard</button>
      </div>
    </div>}
    {error && <p role="alert">{error}</p>}
  </section>
}
