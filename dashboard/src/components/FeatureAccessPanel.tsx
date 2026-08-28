import { forwardRef, useEffect, useImperativeHandle, useMemo, useState } from 'react'

import type { Domain } from '../types'
import type { DomainFeaturePolicy, DomainFeatureState } from '../types/domainFeatures'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { getErrorMessage } from '../utils/errorUtils'

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
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

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
      setExpanded(new Set(next.categories.map(category => category.id)))
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
    const enabled = effective(feature)
    if (filter === 'enabled' && !enabled) return false
    if (filter === 'disabled' && enabled) return false
    const needle = query.trim().toLowerCase()
    return !needle || `${feature.name} ${feature.id} ${feature.description || ''}`.toLowerCase().includes(needle)
  }
  const enabledCount = policy?.features.filter(effective).length || 0
  const totalCount = policy?.features.length || 0

  return (
    <div className="settings-group">
      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Domain Feature Access</span>
            <span className="setting-desc">Control Script Builder workflows, import sources, and named workload presets. Core workspace modules always remain available.</span>
          </div>
          <strong>{enabledCount} / {totalCount}</strong>
        </div>
        <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(12rem, 1fr) minmax(12rem, 2fr) minmax(9rem, auto)' }}>
          <select className="settings-input" aria-label="Feature policy domain" value={domainId} onChange={event => setDomainId(event.target.value)} disabled={dirty || loading || saving}>
            {domains.map(domain => <option key={domain.id} value={domain.id}>{domain.name}</option>)}
          </select>
          <input className="settings-input" type="search" aria-label="Search domain features" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search features…" />
          <select className="settings-input" aria-label="Filter domain features" value={filter} onChange={event => setFilter(event.target.value as FeatureFilter)}>
            <option value="all">All features</option>
            <option value="enabled">Enabled</option>
            <option value="disabled">Disabled</option>
          </select>
        </div>
        {loading && <p className="new-test-inline-note" role="status">Loading feature policy…</p>}
        {error && <div className="settings-inline-actions" role="alert"><span className="settings-error-inline">{error}</span><button type="button" className="btn btn-secondary btn-sm" onClick={() => void load()}>Reload</button></div>}
        {notice && <p className="settings-success-inline" role="status">{notice}</p>}
      </div>

      {policy?.categories.map(category => {
        const features = policy.features.filter(feature => feature.category === category.id && visible(feature))
        if (features.length === 0) return null
        const isExpanded = expanded.has(category.id)
        return (
          <section className="settings-subsection" key={category.id}>
            <button type="button" className="settings-manager-summary" aria-expanded={isExpanded} onClick={() => setExpanded(current => {
              const next = new Set(current)
              if (next.has(category.id)) next.delete(category.id); else next.add(category.id)
              return next
            })}>
              <div><span className="setting-label">{category.name}</span><span className="setting-desc">{category.description}</span></div>
              <strong>{features.filter(effective).length} / {features.length}</strong>
            </button>
            {isExpanded && features.map(feature => (
              <div className="setting-row" key={feature.id}>
                <div className="setting-info">
                  <span className="setting-label">{feature.name}</span>
                  <span className="setting-desc">{feature.id}{feature.parent_id ? ` · controlled by ${byId.get(feature.parent_id)?.name || feature.parent_id}` : ''}</span>
                </div>
                <label className="settings-toggle">
                  <input type="checkbox" checked={!disabled.has(feature.id)} onChange={() => toggle(feature)} disabled={saving} />
                  <span>{effective(feature) ? 'Enabled' : disabled.has(feature.id) ? 'Disabled' : 'Disabled by parent'}</span>
                </label>
              </div>
            ))}
          </section>
        )
      })}

      <div className="settings-inline-actions">
        <button type="button" className="btn btn-primary" disabled={!dirty || saving} onClick={() => void save()}>{saving ? 'Saving…' : 'Save Feature Access'}</button>
        <button type="button" className="btn btn-secondary" disabled={!dirty || saving} onClick={discard}>Discard</button>
        <button type="button" className="btn btn-ghost" disabled={saving || disabledIds.length === 0} onClick={() => { setDisabledIds([]); setNotice(''); setError('') }}>Reset to Defaults</button>
      </div>
    </div>
  )
})

export default FeatureAccessPanel
