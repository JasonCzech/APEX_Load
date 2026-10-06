import type { ElasticRecord } from '../types/elastic'
import { elasticAttributes, elasticScalarValue } from './elasticMonitoringModel'
import './ElasticEvidence.css'

export default function ElasticEvidenceTable({ evidence }: { evidence: ElasticRecord[] }) {
  if (!evidence.length) return <p>No retained matching Elastic or OpenSearch records.</p>
  return <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Time</th><th>Source / query</th><th>Kind / severity</th><th>Service / record</th><th>Projection</th><th>Correlation</th></tr></thead><tbody>{evidence.map((record, index) => {
    const attributes = elasticAttributes(record)
    return <tr key={`${record.source_id || ''}:${record.id || index}`}>
      <td>{record.timestamp_utc ? new Date(record.timestamp_utc).toLocaleString() : '—'}{record.ingested_at && <p>Ingested {new Date(record.ingested_at).toLocaleString()}</p>}{record.received_at && <p>Observed {new Date(record.received_at).toLocaleString()}</p>}</td>
      <td>{record.source_id || record.provider || '—'}{record.query_id && <span> · {record.query_id}</span>}</td>
      <td>{record.kind || '—'} · {record.severity || record.log?.level || record.status || '—'}</td>
      <td>{record.service || '—'}<br />{record.index && <span>{record.index} · </span>}{record.document_id || record.id || '—'}</td>
      <td>{record.log?.message || record.span?.operation || record.transaction?.name || record.summary || '—'}{record.span && <p>Trace {record.span.trace_id} · Span {record.span.span_id} · {record.span.duration_ms} ms{record.span.parent_span_id && ` · Parent ${record.span.parent_span_id}`}</p>}{record.transaction && <p>{record.transaction.duration_ms} ms</p>}{Object.keys(attributes).length > 0 && <details><summary>{Object.keys(attributes).length} projected attributes</summary><dl>{Object.entries(attributes).map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{elasticScalarValue(value)}</dd></div>)}</dl></details>}</td>
      <td>{record.correlation || 'unmatched'}</td>
    </tr>
  })}</tbody></table></div>
}

export function ElasticTraceWaterfall({ evidence }: { evidence: ElasticRecord[] }) {
  const traces = new Map<string, ElasticRecord[]>()
  for (const record of evidence) if (record.span) traces.set(record.span.trace_id, [...(traces.get(record.span.trace_id) || []), record])
  if (!traces.size) return null
  return <section aria-label="Retained trace waterfalls"><h5>Retained trace spans</h5><p>These waterfalls show retained spans on this page. Missing parents or spans can reflect upstream sampling, projection, permissions, paging, or collection limits. They do not establish a complete trace.</p>{[...traces].map(([traceId, records]) => {
    const sorted = [...records].sort((a, b) => Date.parse(a.timestamp_utc || '') - Date.parse(b.timestamp_utc || ''))
    const start = Math.min(...sorted.map(record => Date.parse(record.timestamp_utc || '')).filter(Number.isFinite))
    const end = Math.max(...sorted.map(record => Date.parse(record.timestamp_utc || '') + (record.span?.duration_ms || 0)).filter(Number.isFinite))
    const width = Math.max(1, end - start)
    const spanIds = new Set(sorted.map(record => record.span!.span_id))
    return <details key={traceId}><summary>Trace {traceId} · {records.length} retained spans · partial trace context</summary><table className="advanced-group-table"><thead><tr><th>Operation / service</th><th>Span / parent</th><th>Timeline (ms)</th></tr></thead><tbody>{sorted.map((record, index) => {
      const span = record.span!
      const offset = Math.max(0, Date.parse(record.timestamp_utc || '') - start)
      return <tr key={`${span.span_id}:${index}`}><td>{span.operation || span.span_id} · {record.service || 'Unknown service'}</td><td>{span.span_id}{span.parent_span_id && <span> · parent {span.parent_span_id}{!spanIds.has(span.parent_span_id) && ' (not retained on this page)'}</span>}</td><td><div style={{ height: 8, background: 'var(--border-color, #ddd)', minWidth: 160 }}><div style={{ marginLeft: `${Number.isFinite(offset) ? offset / width * 100 : 0}%`, width: `${Math.min(100, Math.max(0.5, span.duration_ms / width * 100))}%`, height: 8, background: 'var(--accent, #4f86ff)' }} /></div>{Number.isFinite(offset) ? offset.toFixed(2) : 'Unknown start'} ms · {span.duration_ms} ms duration</td></tr>
    })}</tbody></table></details>
  })}</section>
}
