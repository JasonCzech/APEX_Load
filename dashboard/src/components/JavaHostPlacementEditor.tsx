import type { GeneratorAssignmentSettings, SystemInventoryGenerator } from '../types'

interface Props {
  assignment?: GeneratorAssignmentSettings
  inventory?: SystemInventoryGenerator[]
  onChange: (assignment: GeneratorAssignmentSettings) => void
}

export default function JavaHostPlacementEditor({ assignment, inventory = [], onChange }: Props) {
  const selected = assignment?.java_host
  const profiles = inventory.filter(host => ['connected', 'healthy', 'ready'].includes(host.status)
    && host.java_host?.mode === 'dedicated' && host.java_host.containment === 'execution_pod')
    .flatMap(host => host.java_host?.profiles || [])
  const ids = Array.from(new Set(profiles.map(profile => profile.profile_id))).sort()
  const updateProfile = (id: string) => {
    const next = { ...assignment, mode: assignment?.mode || 'auto' as const }
    if (!id) {
      delete next.java_host
      onChange(next)
      return
    }
    const matches = profiles.filter(profile => profile.profile_id === id)
    if (!matches.length) return
    next.java_host = {
      profile_id: id,
      preparation_ms: Math.min(...matches.map(profile => profile.preparation_ms)),
      run_lifetime_ms: Math.min(...matches.map(profile => profile.max_run_ms)),
    }
    onChange(next)
  }
  const updateDeadline = (field: 'preparation_ms' | 'run_lifetime_ms', raw: string) => {
    if (!selected) return
    const milliseconds = Number(raw) * 1000
    if (!Number.isSafeInteger(milliseconds) || milliseconds < 1 || milliseconds > (field === 'preparation_ms' ? 3600000 : 86400000)) return
    onChange({ ...assignment, mode: assignment?.mode || 'auto', java_host: { ...selected, [field]: milliseconds } })
  }
  return <div className="new-test-generator-picker">
    <label className="scenario-field">
      <span>Java execution profile</span>
      <select aria-label="Java execution profile" value={selected?.profile_id || ''} onChange={event => updateProfile(event.target.value)}>
        <option value="">Default dedicated runner</option>
        {ids.map(id => <option key={id} value={id}>{id} — dedicated execution pods</option>)}
        {selected && !ids.includes(selected.profile_id) && <option value={selected.profile_id}>{selected.profile_id} — unavailable</option>}
      </select>
    </label>
    {selected && <>
      <p className="scenario-assignment-copy secondary">Preflight verifies this script against the operator approval. Eligible generators share the group within their host and memory ceilings. Host ceilings are configured limits; the broker confirms the reservation before execution.</p>
      <div className="scenario-browser-grid">
        <label><span>Preparation deadline (seconds)</span><input aria-label="Java preparation deadline (seconds)" type="number" min={0.001} max={3600} step={1} value={selected.preparation_ms / 1000} onChange={event => updateDeadline('preparation_ms', event.target.value)} /></label>
        <label><span>Maximum wall-clock lifetime (seconds)</span><input aria-label="Java maximum wall-clock lifetime (seconds)" type="number" min={0.001} max={86400} step={1} value={selected.run_lifetime_ms / 1000} onChange={event => updateDeadline('run_lifetime_ms', event.target.value)} /></label>
      </div>
      <p className="scenario-assignment-copy secondary">Pauses consume the lifetime. Staged increases require generators with prepared-population support. Preflight reserves a fresh session for every planned admission, including down/up cycles. Increases beyond unused prepared capacity, duration changes and generator recovery are unavailable.</p>
      {!ids.includes(selected.profile_id) && <p role="alert" className="settings-error-inline">The selected Java profile is not currently advertised. Refresh generator inventory before launch.</p>}
    </>}
  </div>
}
