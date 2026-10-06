import { safeAppDynamicsEvidenceLink } from './appDynamicsEvidenceModel'
import type { AppDynamicsEvidence } from '../types/appdynamics'

export default function AppDynamicsEvidenceTable({ evidence, provider = 'AppDynamics' }: { evidence: AppDynamicsEvidence[]; provider?: 'AppDynamics' | 'Dynatrace' | 'Monitoring' }) {
  if (!evidence.length) return <p>No retained matching {provider} evidence.</p>
  return <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Time</th><th>Source / type</th><th>Severity / status</th><th>Summary</th><th>Entity</th><th>Correlation</th><th>Provider</th></tr></thead><tbody>{evidence.map((record, index) => {
    const link = safeAppDynamicsEvidenceLink(record.deep_link)
    const requestGuid = record.properties?.requestguid || record.properties?.request_guid
    const correlation = record.properties?.correlation || record.correlation_strength || record.correlation || 'unmatched'
    return <tr key={`${record.source_id || ''}:${record.id || index}:${record.timestamp_utc}`}>
      <td>{record.timestamp_utc || record.timestamp ? new Date(record.timestamp_utc || record.timestamp || '').toLocaleString() : '—'}</td>
      <td>{record.source_id ? `${record.source_id} · ` : ''}{record.kind || record.type || '—'}</td>
      <td>{[record.severity, record.status].filter(Boolean).join(' · ') || '—'}</td>
      <td>{record.summary || record.title || record.message || '—'}{record.properties?.duration_ms && <span> · {record.properties.duration_ms} ms</span>}{record.properties?.experience && <span> · {record.properties.experience}</span>}{record.properties?.error === 'true' && <span> · error</span>}{record.properties?.evidence_scope === 'sampled_span' && <span> · Sampled span summary</span>}</td>
      <td>{record.entity_id || Object.entries(record.entity_ids || {}).map(([name, id]) => `${name}: ${id}`).join(', ') || record.application_id || '—'}</td>
      <td>{correlation}{requestGuid && <span> · Request GUID: {requestGuid}</span>}{record.properties?.trace_id && <span> · Trace ID: {record.properties.trace_id}</span>}{record.properties?.span_id && <span> · Span ID: {record.properties.span_id}</span>}</td>
      <td>{link ? <a href={link} target="_blank" rel="noopener noreferrer">Open in {record.provider === 'dynatrace' ? 'Dynatrace' : record.provider === 'appdynamics' ? 'AppDynamics' : provider}</a> : '—'}</td>
    </tr>
  })}</tbody></table></div>
}
