import { useEffect, useId, useState } from 'react'
import { useSystemApi } from '../hooks/api/useSystemApi'
import type { InfrastructureMonitorPack } from '../types'
import './MonitoringWorkspace.css'

export default function MetricPackPicker({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { getInfrastructureMonitorPacks } = useSystemApi()
  const [packs, setPacks] = useState<InfrastructureMonitorPack[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [attempt, setAttempt] = useState(0)
  const id = useId()
  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    getInfrastructureMonitorPacks().then(result => { if (active) setPacks(result.packs || []) })
      .catch(() => { if (active) setError('Pack catalog unavailable. Existing permissions are preserved.') })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [getInfrastructureMonitorPacks, attempt])
  const selected = [...new Set(value.split(',').map(item => item.trim()).filter(Boolean))]
  return <fieldset className="pp-field monitor-pack-picker"><legend>Allowed metric packs</legend>
    <p>Select packs the collector may submit. An empty selection permits no packs. Pack support does not prove that a collector is receiving data.</p>
    {loading && <p role="status">Loading metric packs…</p>}
    {error && <p role="alert">{error} <button type="button" className="btn btn-secondary btn-sm" onClick={() => setAttempt(n => n + 1)}>Retry pack catalog</button></p>}
    {packs.map((pack, index) => <label key={pack.id} className="monitor-pack-option"><input type="checkbox" aria-label={pack.name} aria-describedby={`${id}-${index}`} checked={selected.includes(pack.id)} onChange={event => onChange((event.target.checked ? [...selected, pack.id] : selected.filter(item => item !== pack.id)).join(', '))} />
      <span><strong>{pack.name}</strong><small id={`${id}-${index}`}>{pack.status === 'supported' ? 'Native support' : 'Integration contract'} — {pack.signals.join(', ')}</small></span>
    </label>)}
    <details><summary>Custom or existing pack identifiers</summary><label>All allowed pack identifiers<input value={value} onChange={event => onChange(event.target.value)} placeholder="linux-hosts" /></label><p>This edits the complete allow-list, including selected packs above. Custom identifiers do not install an integration.</p></details>
  </fieldset>
}
