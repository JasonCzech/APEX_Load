import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'

interface Evidence {
  timestamp: string
  interval_seconds: number
  graph?: { nodes: { id: string; label: string; kind: string; dependency_type?: string; p95_ms?: number; errors?: number }[] }
  monitors: { source_id: string; name: string; dependency: string; state: string; coverage: string; metrics: string[] }[]
  coverage: string
}

export default function DependencyEvidence({ testId, projectId, select }: { testId: string; projectId?: string; select: (source: string, metric: string, from: string, to: string) => void }) {
  const [enabled, setEnabled] = useState(false)
  const [at, setAt] = useState('')
  const [window, setWindow] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [evidence, setEvidence] = useState<Evidence>()
  const [error, setError] = useState('')
  useEffect(() => {
    const abort = new AbortController(); setEnabled(false)
    if (projectId) void request<{ diagnostics_enabled?: boolean }>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: abort.signal }).then(value => { if (!abort.signal.aborted) setEnabled(value.diagnostics_enabled === true) }).catch(() => {})
    return () => abort.abort()
  }, [projectId])
  useEffect(() => {
    if (!enabled) return
    const abort = new AbortController(); setEvidence(undefined); setError('')
    let pending = false
    const load = async () => {
      if (pending) return
      pending = true
      try {
        const value = await request<Evidence>(`/tests/${encodeURIComponent(testId)}/dependency-evidence${window ? `?at=${encodeURIComponent(window)}` : ''}`, { signal: abort.signal })
        if (!abort.signal.aborted) { setEvidence(value); setError('') }
      } catch (e) { if (!abort.signal.aborted) setError(String(e)) }
      finally { pending = false }
    }
    void load()
    const timer = window ? undefined : globalThis.setInterval(() => void load(), 5000)
    return () => { abort.abort(); if (timer !== undefined) globalThis.clearInterval(timer) }
  }, [testId, window, enabled, refresh])
  if (!enabled) return null
  return <section aria-label="Dependency monitoring evidence">
    <h3>Dependency monitoring evidence</h3>
    <p>Infrastructure links use the configured dependency identity. Measurements show association, not proof of causation.</p>
    <form onSubmit={event => { event.preventDefault(); setRefresh(value => value + 1); if (!at) { setWindow(''); return }; const parsed = new Date(at); if (Number.isNaN(parsed.getTime())) { setError('Enter a valid timestamp.'); return }; setWindow(parsed.toISOString()) }}>
      <label>Observation time (ISO timestamp, blank for latest)<input value={at} onChange={e => setAt(e.target.value)} /></label><button type="submit">Inspect time window</button>
    </form>
    {error && <p role="alert">{error}</p>}
    {evidence && <>
      <p role="status">Coverage: {evidence.coverage} · {new Date(evidence.timestamp).toLocaleString()} · {evidence.interval_seconds}s interval</p>
      <ul>{evidence.graph?.nodes.filter(node => node.kind === 'dependency').map(node => <li key={node.id}>
        {node.label} ({node.dependency_type ?? 'unknown'}) · P95 {node.p95_ms ?? 'unavailable'} ms · {node.errors ?? 0} errors
        {evidence.monitors.filter(monitor => monitor.dependency === node.label).map(monitor => <div key={monitor.source_id}>{monitor.name}: {monitor.coverage} ({monitor.state}) {monitor.metrics.map(metric => <button type="button" key={metric} onClick={() => select(monitor.source_id, metric, new Date(Date.parse(evidence.timestamp) - evidence.interval_seconds * 1000).toISOString(), evidence.timestamp)}>Inspect {metric}</button>)}</div>)}
      </li>)}</ul>
    </>}
  </section>
}
