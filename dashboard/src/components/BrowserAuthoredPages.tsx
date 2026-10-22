import ExportableChart from './ExportableChart'

interface Binding {
  version: number; state: string; observed_at: string; page_id: string; source_generation: string
  request_id: string; document_id: string; lifecycle_id: string; association: string; navigation_sequence?: number
  authored_step: { version: number; script_sha256: string; source_structure_sha256: string; step_path: string; step_id: string }
}
interface Report { version: number; receipt_records: number; bindings: Binding[]; unavailable?: Record<string, number>; dropped: number; incomplete: boolean }
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
const count = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
const sha = (value: unknown) => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
function reportValue(raw: unknown): Report | null {
  if (!object(raw) || raw.version !== 1 || !count(raw.receipt_records) || !count(raw.dropped) || typeof raw.incomplete !== 'boolean' || !Array.isArray(raw.bindings) || raw.bindings.length > 1024) return null
  if (raw.receipt_records < raw.bindings.length + raw.dropped || raw.dropped > 0 && !raw.incomplete) return null
  if (raw.unavailable !== undefined && (!object(raw.unavailable) || Object.entries(raw.unavailable).some(([key, value]) => !/^[a-z_]{1,64}$/.test(key) || !count(value)))) return null
  const seen = new Set<string>()
  for (const item of raw.bindings) {
    if (!object(item) || ![1, 2].includes(item.version as number) || item.state !== 'bound' || !sha(item.page_id) || typeof item.observed_at !== 'string' || !Number.isFinite(Date.parse(item.observed_at)) || typeof item.source_generation !== 'string' || !/^[0-9a-f]{32}$/.test(item.source_generation)) return null
    if (item.version === 1 ? item.association !== 'cdp_loader_join' || item.navigation_sequence !== undefined && item.navigation_sequence !== 0 : item.association !== 'native_navigation_commit' || !count(item.navigation_sequence) || item.navigation_sequence === 0) return null
    for (const key of ['request_id', 'document_id', 'lifecycle_id']) if (typeof item[key] !== 'string' || !(item[key] as string).startsWith(item.source_generation + '-') || (item[key] as string).length > 160) return null
    const step = item.authored_step
    if (!object(step) || step.version !== 2 || !sha(step.script_sha256) || !sha(step.source_structure_sha256) || !sha(step.step_id) || typeof step.step_path !== 'string' || step.step_path.length > 4096) return null
    const key = `${item.document_id}/${item.lifecycle_id}`
    if (seen.has(key)) return null
    seen.add(key)
  }
  return raw as unknown as Report
}

export default function BrowserAuthoredPages({ value }: { value?: unknown }) {
  if (value == null) return <p>Authored page bindings were not retained. URLs and action names do not supply missing page identity.</p>
  const report = reportValue(value)
  if (!report) return <p role="status">Authored page bindings are unavailable because the retained report is malformed or unsupported.</p>
  const shown = report.bindings.slice(0, 256)
  return <section aria-label="Authored browser pages" style={{ minWidth: 0, overflowWrap: 'anywhere' }}>
    <h4>Authored browser pages</h4>
    <p>These receipts join an original authored navigation to its observed native document. They do not change document measurements or establish compatible runtime state, a successful journey or a qualified baseline.</p>
    <p>{report.receipt_records} retained receipts · {report.bindings.length} distinct document bindings · {report.dropped} dropped receipts. Counts do not describe every page issued by the run.</p>
    {report.incomplete && <p role="status">Page binding evidence is incomplete. Conflicting or malformed receipts cannot qualify an identity.</p>}
    {Object.entries(report.unavailable ?? {}).map(([reason, total]) => <p key={reason}>Unavailable: {reason.replace(/_/g, ' ')} ({total})</p>)}
    {!shown.length && <p>No proved authored page bindings were retained.</p>}
    {!!shown.length && <>
      <ExportableChart allowSVG={false} definitionId="browser-authored-pages" title="Authored browser pages" description="Separate original-navigation/native-document receipts; no baseline qualification." rows={report.bindings} columns={[
        { key: 'page', label: 'Authored page', value: row => row.page_id }, { key: 'step', label: 'Original step', value: row => row.authored_step.step_id },
        { key: 'path', label: 'Structural step', value: row => row.authored_step.step_path }, { key: 'source', label: 'Original source SHA-256', value: row => row.authored_step.script_sha256 },
        { key: 'generation', label: 'Source generation', value: row => row.source_generation }, { key: 'document', label: 'Document', value: row => row.document_id },
        { key: 'lifecycle', label: 'Lifecycle', value: row => row.lifecycle_id }, { key: 'request', label: 'Native request', value: row => row.request_id },
        { key: 'association', label: 'Association', value: row => row.association }, { key: 'sequence', label: 'Native navigation sequence', value: row => row.navigation_sequence ?? '' }, { key: 'observed', label: 'Observed at', value: row => row.observed_at },
      ]}>{null}</ExportableChart>
      <p>Showing {shown.length} of {report.bindings.length} retained bindings. Export includes all retained bindings.</p>
      {shown.map(binding => <details key={`${binding.document_id}/${binding.lifecycle_id}`}><summary>Inspect authored page for {binding.authored_step.step_path} · document …{binding.document_id.slice(-8)}</summary><dl className="browser-document-details">
        <dt>Authored page / step</dt><dd>{binding.page_id} / {binding.authored_step.step_id}</dd>
        <dt>Original source / structure SHA-256</dt><dd>{binding.authored_step.script_sha256} / {binding.authored_step.source_structure_sha256}</dd>
        <dt>Source generation</dt><dd>{binding.source_generation}</dd><dt>Document / lifecycle</dt><dd>{binding.document_id} / {binding.lifecycle_id}</dd>
        <dt>Native request / association</dt><dd>{binding.request_id} / {binding.association}</dd>{binding.version === 2 && <><dt>Native navigation sequence</dt><dd>{binding.navigation_sequence}</dd></>}<dt>Observed at (source UTC)</dt><dd>{binding.observed_at}</dd>
      </dl></details>)}
    </>}
  </section>
}
