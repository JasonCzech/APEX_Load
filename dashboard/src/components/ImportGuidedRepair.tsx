import { useEffect, useRef, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { EnterpriseArchiveFidelityReport, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'

type Finding = { code: string; action_path?: string; source_ref?: string; detail: string; gate?: string }
type RepairResult = { secret_binding_requirements: SecretBindingRequirement[]; fidelity_report: EnterpriseArchiveFidelityReport; candidate_id: string; revision: string; script: ScriptDefinition; before: ScriptDefinition; findings: Finding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; blocking_codes?: string[] }; allocation?: { name: string; rows: number; block_size: number; population: number }[]; expires_at: string }
type Operation = { type: string; [key: string]: unknown }

export function ImportGuidedRepair({ script, projectId, disabled, onApplied, onViewSource }: { script: ScriptDefinition; projectId: string; disabled?: boolean; onApplied: (result: RepairResult) => void; onViewSource?: (path: string) => void }) {
  const candidate = String(script.metadata?.import?.preserved?.server_import_attestation_v1 || '')
  const currentScope = useRef('')
  currentScope.current = `${candidate}:${projectId}`
  useEffect(() => () => { currentScope.current = '' }, [])
  const [review, setReview] = useState<RepairResult | null>(null)
  const [preview, setPreview] = useState<RepairResult | null>(null)
  const [selection, setSelection] = useState('')
  const [kind, setKind] = useState('restore_body')
  const [value, setValue] = useState('')
  const [responsePath, setResponsePath] = useState('')
  const [extractorType, setExtractorType] = useState('jsonpath')
  const [users, setUsers] = useState(2)
  const [iterations, setIterations] = useState(0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState<Operation | null>(null)
  const finding = review?.findings.find(f => `${f.action_path}:${f.code}` === selection)
  const payload = (operation: Operation) => ({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: finding?.code || '', action_path: finding?.action_path || '', operation })
  useEffect(() => {
    setReview(null); setPreview(null); setValue(''); setSelection(''); setError(''); setPending(null); setBusy(false)
    if (!candidate.startsWith('protected:')) return
    let active = true
    request<RepairResult>('/scripts/import/repairs/preview', { method: 'POST', body: JSON.stringify({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: '', action_path: '', operation: { type: 'inspect' } }) })
      .then(result => { if (active) setReview(result) }).catch(e => { if (active) setError(String(e.message || e)) })
    return () => { active = false }
  }, [candidate, projectId])
  async function run(operation: Operation, apply = false) {
    const scope = currentScope.current
    setBusy(true); setError('')
    try {
      const result = await request<RepairResult>(`/scripts/import/repairs/${apply ? 'apply' : 'preview'}`, { method: 'POST', body: JSON.stringify(payload(operation)) })
      if (scope !== currentScope.current) return
      if (apply) { setValue(''); setPreview(null); onApplied(result) }
      else { setPreview(result); setPending(operation) }
    } catch (e) { if (scope === currentScope.current) setError(e instanceof Error ? e.message : 'Repair failed') }
    finally { if (scope === currentScope.current) setBusy(false) }
  }
  function operation(): Operation {
    const variable = finding?.code.split(':').slice(1).join(':') || ''
    switch (kind) {
      case 'restore_body': return { type: kind, body: value }
      case 'replace_proxy_secret': return { type: kind, secret_name: value }
      case 'bind_variable': return { type: kind, producer: value }
      case 'add_parameter': return { type: kind, parameter: { name: variable, type: 'list', values: value.split('\n'), update: 'once', selection: 'sequential' } }
      case 'add_extractor': return { type: kind, response_path: responsePath, extractor: { name: variable, type: extractorType, source: 'body', expression: value, on_miss: 'fail' } }
      default: return { type: 'select_tls', tls_min: value || '1.2', tls_max: '1.3' }
    }
  }
  const locked = disabled || busy
  return <section className="lr-repair-summary" aria-label="Guided import repair">
    <h4>Guided import repair</h4>
    {!candidate.startsWith('protected:') ? <p>Guided repair requires a protected import candidate. Re-import with encrypted import storage configured.</p> : <>
      <p>Source-linked requirements stay blocked until their behavior is replaced. Restored bodies are protected and previews are redacted.</p>
      {review && <p role="status">{review.readiness.replay_ready ? 'Ready for replay checks' : `${review.readiness.blocking_codes?.length || 0} unresolved execution requirements`}</p>}
      <label>Finding <select aria-label="Repair finding" value={selection} disabled={locked} onChange={e => { setSelection(e.target.value); setPreview(null); setValue('') }}><option value="">Select a requirement</option>{review?.findings.map(f => <option key={`${f.action_path}:${f.code}`} value={`${f.action_path}:${f.code}`}>{f.source_ref || f.action_path}: {f.code}</option>)}</select></label>
      {finding && <><p>{finding.detail}</p>{onViewSource && finding.action_path && <button type="button" onClick={() => onViewSource(finding.action_path!)}>Open source location</button>}
        <label>Repair <select aria-label="Repair operation" value={kind} disabled={locked} onChange={e => { setKind(e.target.value); setPreview(null); setValue('') }}>
          <option value="restore_body">Supply restored request body</option><option value="replace_proxy_secret">Replace proxy secret binding</option><option value="bind_variable">Bind existing producer</option><option value="add_parameter">Add parameter</option><option value="add_extractor">Add response extractor</option><option value="select_tls">Select supported TLS</option>
        </select></label>
        {kind === 'add_extractor' && <><label>Preceding response path <input aria-label="Preceding response path" value={responsePath} onChange={e => { setResponsePath(e.target.value); setPreview(null) }} /></label><label>Extractor type <select value={extractorType} onChange={e => { setExtractorType(e.target.value); setPreview(null) }}><option value="jsonpath">JSONPath</option><option value="regex">Regular expression</option><option value="boundary">Boundary</option></select></label></>}
        {kind === 'select_tls' ? <label>Minimum TLS <select value={value || '1.2'} onChange={e => { setValue(e.target.value); setPreview(null) }}><option value="1.2">TLS 1.2 (up to 1.3)</option><option value="1.3">TLS 1.3 only</option></select><p>This records an intentional migration change, not exact SSL 2/3 parity.</p></label> : <label>{kind === 'restore_body' ? 'Restored body' : kind === 'add_parameter' ? 'Parameter values, one per line' : kind === 'add_extractor' ? 'Extractor expression' : 'Existing producer or secret name'}<textarea aria-label="Repair value" autoComplete="off" value={value} onChange={e => { setValue(e.target.value); setPreview(null) }} disabled={locked} /></label>}
        <button type="button" disabled={locked} onClick={() => run(operation())}>Preview repair</button>
      </>}
      <fieldset disabled={locked}><legend>Unique parameter allocation</legend><label>Planned VUsers <input type="number" min={1} value={users} onChange={e => setUsers(Number(e.target.value))} /></label><label>Iterations per VUser (0 = duration run) <input type="number" min={0} value={iterations} onChange={e => setIterations(Number(e.target.value))} /></label><button type="button" onClick={() => run({ type: 'preview_allocation', vusers: users, iterations })}>Preview allocation</button></fieldset>
      {preview && <div aria-label="Redacted repair preview"><p>{preview.readiness.blocking_codes?.length || 0} execution requirements remain after this repair.</p>{preview.allocation && <table><thead><tr><th>Parameter</th><th>Usable rows</th><th>Block per VUser</th><th>VUsers</th></tr></thead><tbody>{preview.allocation.map(p => <tr key={p.name}><td>{p.name}</td><td>{p.rows}</td><td>{p.block_size}</td><td>{p.population}</td></tr>)}</tbody></table>}<details><summary>Redacted before / after</summary><pre>{JSON.stringify({ before: preview.before, after: preview.script }, null, 2)}</pre></details>{pending && pending.type !== 'preview_allocation' && <button type="button" disabled={locked} onClick={() => run(pending, true)}>Apply reviewed repair</button>}</div>}
    </>}
    {error && <p role="alert">{error}</p>}
  </section>
}
