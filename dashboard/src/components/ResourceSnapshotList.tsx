import type { ScriptResourceSnapshot } from '../types/scripts'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'

export default function ResourceSnapshotList({ snapshot }: { snapshot?: { resources?: ScriptResourceSnapshot[]; omitted_resources?: number } }) {
 if (!snapshot?.resources?.length && !snapshot?.omitted_resources) return null
 return <section aria-label="Captured resources"><h4>Captured resources</h4>
  {(snapshot.resources || []).map((resource, index) => <details key={`${resource.request_url}:${index}`}><summary>GET {resource.request_url} · {resource.response_status || resource.failure_kind || 'No response'}{resource.cache_hit ? ' · cache' : ''}</summary>
   <p>Body: {resource.response_body_state.replace(/_/g, ' ')}{resource.truncated ? ' · retention limit' : ''}</p>
   <SnapshotTextPreview text={Object.entries(resource.request_headers || {}).map(([key, value]) => `${key}: ${value}`).join('\n')} />
   <SnapshotTextPreview text={Object.entries(resource.response_headers || {}).map(([key, value]) => `${key}: ${value}`).join('\n')} />
   <SnapshotBodyPreview body={resource.response_body} headers={resource.response_headers} emptyMessage="No resource body retained" />
  </details>)}
  {!!snapshot.omitted_resources && <p>{snapshot.omitted_resources} resources omitted by capture limits.</p>}
 </section>
}
