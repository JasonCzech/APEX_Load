import { useEffect, useState } from 'react'
import { useSystemApi } from '../hooks/api/useSystemApi'
import type { InfrastructureMonitorPack } from '../types'

export default function MetricPackPicker({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { getInfrastructureMonitorPacks } = useSystemApi()
  const [packs, setPacks] = useState<InfrastructureMonitorPack[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    getInfrastructureMonitorPacks().then(result => { if (active) setPacks(result.packs || []) }).catch(() => { if (active) setError('Pack catalog unavailable. Existing pack permissions are preserved; retry by reopening the editor.') })
    return () => { active = false }
  }, [getInfrastructureMonitorPacks])
  const selected = value.split(',').map(item => item.trim()).filter(Boolean)
  return <fieldset className="pp-field"><legend>Allowed metric packs</legend>
    <p>Select packs the collector may submit. An empty selection permits no packs. Pack support does not prove that a collector is receiving data.</p>
    {error && <p role="status">{error}</p>}
    {packs.map(pack => <label key={pack.id}><input type="checkbox" checked={selected.includes(pack.id)} onChange={event => onChange((event.target.checked ? [...selected, pack.id] : selected.filter(id => id !== pack.id)).join(', '))} /> {pack.name} — {pack.status === 'supported' ? 'Native support' : 'Integration contract'}<small>{pack.signals.join(', ')}</small></label>)}
    <details><summary>Custom or existing pack identifiers</summary><label>Additional pack identifiers<input value={value} onChange={event => onChange(event.target.value)} placeholder="linux-hosts" /></label><p>Use exact identifiers emitted by your collector. Custom identifiers do not install an integration.</p></details>
  </fieldset>
}
