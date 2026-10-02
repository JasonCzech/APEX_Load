import { useState } from 'react'
import type { BrowserResourceTiming } from '../types'

const number = (v: number | undefined, suffix: string) => v !== undefined && Number.isFinite(v) && v >= 0 ? `${v.toFixed(1)}${suffix}` : 'Unavailable'
const bytes = (v?: number) => v === undefined || !Number.isFinite(v) || v < 0 ? 'Unavailable' : v >= 1024 ? `${(v / 1024).toFixed(2)} KB` : `${v} B`
const shortURL = (value?: string) => value?.replace(/^https?:\/\//, '') || 'Unavailable'

export default function BrowserResourceEvidence({ resources }: { resources: BrowserResourceTiming[] }) {
 const [action, setAction] = useState('')
 const [failures, setFailures] = useState(false)
 // The report contains at most 50 slowest retained resources; filters never
 // imply a complete network census or request-start timing.
 const retained = resources.slice(0, 50)
 const rows = retained.filter(r => (!action || r.action_name === action) && (!failures || r.failure_kind || (r.status_code ?? 0) >= 400))
 return <section className="tx-section archive-summary-section" aria-label="Browser resource evidence">
  <div className="archive-section-header archive-section-header-stack"><div><h3 className="chart-title">Browser Resource Waterfall</h3><p className="archive-section-copy">Slowest browser sub-resources captured from archived network entries.</p></div><span className="archive-chip">{rows.length} of {retained.length} retained resources</span></div>
  <p>Bounded diagnostic sample of up to 50 resources. The timestamp identifies the capturing action; individual request start times and transport phases are unavailable.</p>
  <label>Resource action <select aria-label="Resource action" value={action} onChange={e => setAction(e.target.value)}><option value="">All actions</option>{Array.from(new Set(retained.map(r => r.action_name).filter((v): v is string => !!v))).sort().map(v => <option key={v}>{v}</option>)}</select></label>
  <label><input type="checkbox" checked={failures} onChange={e => setFailures(e.target.checked)} />Failed resources only</label>
  <table className="tx-table"><thead><tr><th>Resource</th><th>Type</th><th>Status</th><th>Duration</th><th>Transfer</th><th>Action</th><th>Device</th><th>Evidence</th></tr></thead><tbody>{rows.map((r, i) => <tr key={`${r.url}-${r.action_name}-${i}`}>
   <td className="tx-name" title={r.url}>{shortURL(r.url)}</td><td>{r.resource_type || 'resource'}</td><td>{r.status_code && r.status_code > 0 ? r.status_code : 'Unavailable'}{r.failure_kind ? ` · ${r.failure_kind}` : ''}</td><td>{number(r.duration_ms, ' ms')}</td><td>{bytes(r.bytes_received)}</td><td>{r.action_name || 'Unavailable'}</td><td>{r.device_profile || r.device_platform || r.browser_engine || 'Unavailable'}</td>
   <td><details><summary>Inspect resource {i + 1}</summary><dl><dt>Captured by action at</dt><dd>{r.timestamp || 'Unavailable'}</dd><dt>Page</dt><dd>{r.page_url || 'Unavailable'}</dd><dt>Resource URL</dt><dd>{r.url || 'Unavailable'}</dd><dt>Method</dt><dd>{r.method || 'Unavailable'}</dd><dt>Failure classification</dt><dd>{r.failure_kind || 'Not recorded'}</dd><dt>Device / platform / engine</dt><dd>{[r.device_profile, r.device_platform, r.browser_engine].filter(Boolean).join(' / ') || 'Unavailable'}</dd></dl></details></td>
  </tr>)}</tbody></table>{!rows.length && <p>No retained resources match these filters.</p>}
 </section>
}
