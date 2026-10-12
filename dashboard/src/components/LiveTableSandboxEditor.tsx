import { liveTableSandboxError } from './liveTableSandboxModel'

export default function LiveTableSandboxEditor({ value, onChange }: { value?: string; onChange: (value: string) => void }) {
  const error = liveTableSandboxError(value)
  return <div className="full-width replay-variables-field">
    <h3>Isolated live tables</h3>
    <p>Provide explicit starting rows for the aliases used by live-table actions. Every replay starts fresh; sandbox operations never use a running table or save changes to a data pool. Seed values stay in this editor session and are excluded from saved debug profiles.</p>
    <p>Up to four tables and 64 KiB of seed data. Each table allows 32 columns, 256 rows and 256 KiB of runtime data. Use an empty rows array for producer scripts. Inspect action results through their result-prefix variables.</p>
    <label><span>Sandbox seeds (JSON)</span><textarea aria-label="Sandbox seeds (JSON)" aria-invalid={Boolean(error)} value={value || ''} onChange={event => onChange(event.target.value)} rows={7} placeholder={'[{"alias":"work","profile":"native_queue","columns":["id"],"rows":[{"values":{"id":"sample-1"}}]}]'} /></label>
    {error && <p role="alert">{error}</p>}
    <button type="button" className="btn btn-sm btn-secondary" disabled={Boolean(value?.trim() && value.trim() !== '[]')} onClick={() => onChange(JSON.stringify([{ alias: 'work', profile: 'native_queue', columns: ['id'], rows: [] }], null, 2))}>Add empty sandbox example</button>
    <p>Seed values can appear in explicitly inspected debugger results. Do not paste managed secrets. Empty strings and absent cells are preserved.</p>
  </div>
}
