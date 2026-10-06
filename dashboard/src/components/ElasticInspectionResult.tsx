import type { ElasticInspection } from '../types/elastic'
import ElasticEvidenceTable, { ElasticTraceWaterfall } from './ElasticEvidenceTable'
import { elasticCapabilityHealth } from './elasticMonitoringModel'

export default function ElasticInspectionResult({ operation, onCancel }: { operation?: ElasticInspection; onCancel?: () => void }) {
  if (!operation) return null
  const result = operation.result
  const preview = result?.preview || result
  const health = elasticCapabilityHealth(result?.capabilities)
  return <section className="elastic-evidence" aria-label="Elastic inspection result"><h4>Elastic inspection · {operation.kind}</h4>
    <p role="status">{operation.status} · connection revision {operation.connection_revision}{operation.message ? ` · ${operation.message}` : ''}</p>
    {['pending', 'queued', 'running'].includes(operation.status) && onCancel && <button type="button" onClick={onCancel}>Cancel Elastic inspection</button>}
    {result?.capabilities && <p>Detected {result.capabilities.product} {result.capabilities.version || ''}{result.capabilities.serverless ? ' · serverless' : ''}. ES|QL: {result.capabilities.esql ? 'supported' : 'unavailable'}.</p>}
    {health && <ul>{Object.entries(health).map(([name, capability]) => <li key={name}>{name}: {capability.status}</li>)}</ul>}
    {(result?.truncated || Object.values(preview?.coverage || {}).some(capability => capability.truncated)) && <p role="status">The preview was bounded or truncated. Inspect coverage before treating the returned records or metrics as complete.</p>}
    {preview?.coverage && <ul>{Object.entries(preview.coverage).map(([name, capability]) => <li key={name}>{name}: {capability.status}{capability.message ? ` · ${capability.message}` : ''}{capability.reason ? ` · ${capability.reason}` : ''}</li>)}</ul>}
    {result?.indices && <ul aria-label="Discovered indices">{result.indices.map(index => <li key={index.name}>{index.name} · {index.type}</li>)}</ul>}
    {result?.fields && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Field</th><th>Mapping type</th><th>Capabilities</th></tr></thead><tbody>{result.fields.map(field => <tr key={field.name}><td>{field.name}</td><td>{field.types.join(', ')}</td><td>{field.conflict ? 'Conflicting types · ' : ''}{field.searchable ? 'Searchable' : 'Not searchable'} · {field.aggregatable ? 'Aggregatable' : 'Not aggregatable'}</td></tr>)}</tbody></table></div>}
    {result?.columns && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr>{result.columns.map(column => <th key={column.name}>{column.name} ({column.type})</th>)}</tr></thead><tbody>{result.rows?.map((row, index) => <tr key={index}>{row.map((value, column) => <td key={column}>{value === null ? 'null' : typeof value === 'object' ? JSON.stringify(value) : String(value)}</td>)}</tr>)}</tbody></table></div>}
    {preview?.points?.length ? <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Time</th><th>Metric</th><th>Value / unit</th><th>Dimensions / quality</th></tr></thead><tbody>{preview.points.map((point, index) => <tr key={index}><td>{point.timestamp_utc}</td><td>{point.metric_name}</td><td>{point.value ?? 'Missing'} {point.unit}</td><td>{JSON.stringify(point.dimensions || {})} · {point.quality}{point.provider?.approximate ? ' · approximate' : ''}</td></tr>)}</tbody></table></div> : null}
    {preview?.evidence && <><ElasticEvidenceTable evidence={preview.evidence} /><ElasticTraceWaterfall evidence={preview.evidence} /></>}
  </section>
}
