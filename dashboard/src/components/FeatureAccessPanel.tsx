import './Access.css'
import { forwardRef, useEffect, useId, useImperativeHandle, useMemo, useState } from 'react'

import type { Domain } from '../types'
import type { DomainFeaturePolicy, DomainFeatureState } from '../types/domainFeatures'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { getErrorMessage } from '../utils/errorUtils'
import { destinationTitle } from './settingsNavigation'

export interface FeatureAccessController {
  save: () => Promise<boolean>
  discard: () => void
}

interface Props {
  domains: Domain[]
  initialDomainId?: string
  onDirtyChange?: (dirty: boolean) => void
}

type FeatureFilter = 'all' | 'enabled' | 'disabled'

function effectiveEnabled(feature: DomainFeatureState, disabled: Set<string>, byId: Map<string, DomainFeatureState>) {
  if (disabled.has(feature.id)) return false
  let parentId = feature.parent_id
  const visited = new Set<string>()
  while (parentId && !visited.has(parentId)) {
    visited.add(parentId)
    if (disabled.has(parentId)) return false
    parentId = byId.get(parentId)?.parent_id
  }
  return true
}

const FeatureAccessPanel = forwardRef<FeatureAccessController, Props>(function FeatureAccessPanel({ domains, initialDomainId, onDirtyChange }, ref) {
  const api = useRepositoryApi()
  const [domainId, setDomainId] = useState(initialDomainId || domains[0]?.id || '')
  const [policy, setPolicy] = useState<DomainFeaturePolicy | null>(null)
  const [disabledIds, setDisabledIds] = useState<string[]>([])
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<FeatureFilter>('all')
  const [categoryFilter, setCategoryFilter] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const domainLockedHintId = useId()
  const dirty = policy !== null && JSON.stringify([...disabledIds].sort()) !== JSON.stringify([...(policy?.disabled_feature_ids || [])].sort())
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange])

  useEffect(() => {
    setDomainId(current => {
      if (current && domains.some(domain => domain.id === current)) return current
      if (initialDomainId && domains.some(domain => domain.id === initialDomainId)) return initialDomainId
      return domains[0]?.id || ''
    })
  }, [domains, initialDomainId])

  const load = async (nextDomainId = domainId) => {
    if (!nextDomainId) return
    setLoading(true)
    setError('')
    setNotice('')
    try {
      const next = await api.getDomainFeatures(nextDomainId)
      setPolicy(next)
      setDisabledIds(next.disabled_feature_ids || [])
      setCategoryFilter(current => next.categories.some(category => category.id === current) ? current : '')
    } catch (loadError: unknown) {
      setPolicy(null)
      setDisabledIds([])
      setError(getErrorMessage(loadError, 'Unable to load feature access policy'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load(domainId) }, [domainId]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (domainId) return
    setPolicy(null)
    setDisabledIds([])
  }, [domainId])

  const save = async () => {
    if (!policy || !dirty) return true
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const next = await api.updateDomainFeatures(policy.domain_id, disabledIds, policy.revision)
      setPolicy(next)
      setDisabledIds(next.disabled_feature_ids || [])
      setNotice('Feature access policy saved. Open workspaces will refresh automatically.')
      return true
    } catch (saveError: unknown) {
      const message = getErrorMessage(saveError, 'Unable to save feature access policy')
      setError(message.includes('changed while') ? `${message} Reload the policy, review the changes, and save again.` : message)
      return false
    } finally {
      setSaving(false)
    }
  }

  const discard = () => {
    setDisabledIds(policy?.disabled_feature_ids || [])
    setError('')
    setNotice('')
  }

  useImperativeHandle(ref, () => ({ save, discard }))

  const disabled = useMemo(() => new Set(disabledIds), [disabledIds])
  const byId = useMemo(() => new Map((policy?.features || []).map(feature => [feature.id, feature])), [policy])
  const effective = (feature: DomainFeatureState) => effectiveEnabled(feature, disabled, byId)
  const toggle = (feature: DomainFeatureState) => {
    setDisabledIds(current => {
      const next = new Set(current)
      if (next.has(feature.id)) next.delete(feature.id)
      else next.add(feature.id)
      return [...next].sort()
    })
    setNotice('')
    setError('')
  }
  const visible = (feature: DomainFeatureState) => {
    if (categoryFilter && feature.category !== categoryFilter) return false
    const enabled = effective(feature)
    if (filter === 'enabled' && !enabled) return false
    if (filter === 'disabled' && enabled) return false
    const needle = query.trim().toLowerCase()
    return !needle || `${feature.name} ${feature.id} ${feature.description || ''}`.toLowerCase().includes(needle)
  }
  const enabledCount = policy?.features.filter(effective).length || 0
  const totalCount = policy?.features.length || 0
  const categoryNames = new Map((policy?.categories || []).map(category => [category.id, category.name]))
  const categoryOrder = new Map((policy?.categories || []).map((category, index) => [category.id, index]))
  const rows = (policy?.features || [])
    .filter(visible)
    .sort((a, b) => (categoryOrder.get(a.category) ?? Number.MAX_SAFE_INTEGER) - (categoryOrder.get(b.category) ?? Number.MAX_SAFE_INTEGER))
  const savedDisabled = new Set(policy?.disabled_feature_ids || [])

  return (
    <div className="settings-group">
      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">{destinationTitle('features')}</span>
            <span className="setting-desc">Control Script Builder workflows, import sources, and named workload presets. Core workspace modules always remain available.</span>
          </div>
          <strong>{enabledCount} / {totalCount}</strong>
        </div>
        <div className="access-table-toolbar">
          <label>
            <span>Domain</span>
            <select aria-label="Feature policy domain" value={domainId} onChange={event => setDomainId(event.target.value)} disabled={dirty || loading || saving} aria-describedby={dirty ? domainLockedHintId : undefined} title={dirty ? 'Save or discard your changes to switch domain' : undefined}>
              {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
            </select>
            {dirty && <small id={domainLockedHintId} className="setting-desc">Save or discard your changes to switch domain.</small>}
          </label>
          <label>
            <span>Search</span>
            <input type="search" aria-label="Search domain features" value={query} onChange={event => setQuery(event.target.value)} placeholder="name, key, description" />
          </label>
          <label>
            <span>Category</span>
            <select aria-label="Filter feature category" value={categoryFilter} onChange={event => setCategoryFilter(event.target.value)}>
              <option value="">All categories</option>
              {policy?.categories.map(category => <option key={category.id} value={category.id}>{category.name}</option>)}
            </select>
          </label>
          <label>
            <span>Status</span>
            <select aria-label="Filter domain features" value={filter} onChange={event => setFilter(event.target.value as FeatureFilter)}>
              <option value="all">All statuses</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
        </div>
        {loading && <p className="new-test-inline-note" role="status">Loading feature policy…</p>}
        {error && <div className="settings-inline-actions" role="alert"><span className="settings-error-inline">{error}</span><button type="button" className="btn btn-secondary btn-sm" onClick={() => void load()}>Reload</button></div>}
        {notice && <p className="settings-success-inline" role="status">{notice}</p>}
        {policy && (
          <div className="access-table-wrap feature-access-table-wrap" data-table-pagination="off">
            <table className="access-data-table feature-access-table">
              <caption className="sr-only">Domain features</caption>
              <thead><tr>{['Feature', 'Category', 'Key', 'Depends on', 'Status', 'Allow'].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead>
              <tbody>{rows.map(feature => {
                const enabled = effective(feature)
                const status = enabled ? 'Enabled' : disabled.has(feature.id) ? 'Disabled' : 'Disabled by parent'
                const changed = disabled.has(feature.id) !== savedDisabled.has(feature.id)
                return (
                  <tr key={feature.id} className={changed ? 'selected' : undefined}>
                    <td>
                      <span className="feature-access-name">{feature.name}</span>
                      {(feature.description || changed) && <span className="access-row-meta">{changed ? 'Unsaved change' : feature.description}</span>}
                    </td>
                    <td>{categoryNames.get(feature.category) || feature.category}</td>
                    <td>{feature.id}</td>
                    <td>{feature.parent_id ? byId.get(feature.parent_id)?.name || feature.parent_id : '—'}</td>
                    <td><span className={`access-status-badge feature-access-status${enabled ? ' healthy' : ''}`}>{status}</span></td>
                    <td><input type="checkbox" aria-label={`Allow ${feature.name}`} checked={!disabled.has(feature.id)} onChange={() => toggle(feature)} disabled={saving} /></td>
                  </tr>
                )
              })}</tbody>
            </table>
            {!rows.length && <div className="access-empty-state"><strong>No features match these filters</strong><span>Clear the search or choose a broader category or status.</span></div>}
          </div>
        )}
      </div>

      <div className="settings-inline-actions">
        <button type="button" className="btn btn-primary" disabled={!dirty || saving} onClick={() => void save()}>{saving ? 'Saving…' : `Save ${destinationTitle('features').toLowerCase()}`}</button>
        <button type="button" className="btn btn-secondary" disabled={!dirty || saving} onClick={discard}>Discard</button>
        <button type="button" className="btn btn-ghost" disabled={saving || disabledIds.length === 0} onClick={() => { setDisabledIds([]); setNotice(''); setError('') }}>Reset to Defaults</button>
      </div>
    </div>
  )
})

export default FeatureAccessPanel
