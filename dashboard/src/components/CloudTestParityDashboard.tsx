import { useEffect, useMemo, useState } from 'react'

import { useSystemApi } from '../hooks/api/useSystemApi'
import type { CloudTestCapability } from '../types/system'
import { getErrorMessage } from '../utils/errorUtils'
import './CloudTestParityDashboard.css'

const STATUS_ORDER = ['supported', 'partial', 'planned', 'demand_gated', 'non_goal'] as const

function label(value: string) {
  return value.replace(/_/g, ' ').replace(/\b\w/g, (character: string) => character.toUpperCase())
}

function statusTone(status: CloudTestCapability['apex_status']) {
  if (status === 'supported') return 'healthy'
  if (status === 'partial' || status === 'planned' || status === 'demand_gated') return 'warning'
  return 'neutral'
}

export default function CloudTestParityDashboard() {
  const { getParityStatus } = useSystemApi()
  const [capabilities, setCapabilities] = useState<CloudTestCapability[]>([])
  const [status, setStatus] = useState('all')
  const [category, setCategory] = useState('all')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    void getParityStatus()
      .then(response => {
        if (!active) return
        setCapabilities(response.cloudtest_capabilities || [])
        setError('')
      })
      .catch(reason => {
        if (active) setError(getErrorMessage(reason, 'Failed to load CloudTest parity status.'))
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => { active = false }
  }, [getParityStatus])

  const categories = useMemo(
    () => Array.from(new Set(capabilities.map(capability => capability.category))).sort(),
    [capabilities],
  )
  const counts = useMemo(() => Object.fromEntries(STATUS_ORDER.map(value => [
    value,
    capabilities.filter(capability => capability.apex_status === value).length,
  ])), [capabilities])
  const filtered = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    return capabilities.filter(capability => (
      (status === 'all' || capability.apex_status === status)
      && (category === 'all' || capability.category === category)
      && (!normalizedQuery || `${capability.id} ${capability.name} ${capability.workstream}`.toLowerCase().includes(normalizedQuery))
    ))
  }, [capabilities, category, query, status])

  return (
    <div className="settings-subsection cloudtest-parity" data-testid="cloudtest-parity-dashboard">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">CloudTest outcome parity</span>
          <span className="setting-desc">Canonical, evidence-linked delivery ledger. Supported means the user outcome is present; it does not require copying CloudTest internals.</span>
        </div>
        <strong aria-label="CloudTest capability total">{capabilities.length}</strong>
      </div>

      {loading && <p className="new-test-inline-note" role="status">Loading capability ledger…</p>}
      {error && <div className="settings-error" role="alert">{error}</div>}

      {!loading && !error && (
        <>
          <div className="cloudtest-parity-summary" aria-label="Capability status summary">
            {STATUS_ORDER.map(value => (
              <button
                key={value}
                type="button"
                className={`cloudtest-parity-summary-card ${status === value ? 'active' : ''}`}
                onClick={() => setStatus(current => current === value ? 'all' : value)}
                aria-pressed={status === value}
              >
                <span>{label(value)}</span>
                <strong>{counts[value] || 0}</strong>
              </button>
            ))}
          </div>

          <div className="cloudtest-parity-filters">
            <label>
              <span>Status</span>
              <select aria-label="CloudTest parity status" value={status} onChange={event => setStatus(event.target.value)}>
                <option value="all">All statuses</option>
                {STATUS_ORDER.map(value => <option key={value} value={value}>{label(value)}</option>)}
              </select>
            </label>
            <label>
              <span>Category</span>
              <select aria-label="CloudTest parity category" value={category} onChange={event => setCategory(event.target.value)}>
                <option value="all">All categories</option>
                {categories.map(value => <option key={value} value={value}>{label(value)}</option>)}
              </select>
            </label>
            <label className="cloudtest-parity-search">
              <span>Find capability</span>
              <input aria-label="Find CloudTest capability" value={query} onChange={event => setQuery(event.target.value)} placeholder="Name, ID, or workstream" />
            </label>
          </div>

          <div className="cloudtest-parity-table-wrap">
            <table className="cloudtest-parity-table">
              <thead>
                <tr><th>Capability</th><th>APEX status</th><th>Delivery</th><th>Evidence</th><th>Acceptance gate</th></tr>
              </thead>
              <tbody>
                {filtered.map(capability => (
                  <tr key={capability.id}>
                    <td><strong>{capability.name}</strong><code>{capability.id} · {capability.workstream}</code></td>
                    <td><span className={`repository-status-pill ${statusTone(capability.apex_status)}`}>{label(capability.apex_status)}</span></td>
                    <td>{label(capability.implementation_status)}</td>
                    <td><a href={capability.cloudtest_evidence_url} target="_blank" rel="noreferrer">{label(capability.evidence_confidence)}</a></td>
                    <td>{capability.acceptance_test}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {filtered.length === 0 && <p className="new-test-inline-note">No capabilities match these filters.</p>}
          <p className="new-test-inline-note">Source: <code>pkg/api/cloudtest_parity_catalog.json</code> via <code>GET /api/v1/system/parity-status</code>.</p>
        </>
      )}
    </div>
  )
}
