import { useState } from 'react'
import type { LiveTableSandboxSeed } from '../types/replay'
import DataRowGrid from './ux/DataRowGrid'
import { parseLiveTableSandboxSeeds } from './liveTableSandboxModel'

const SANDBOX_MAX_COLUMNS = 32
const profileLabel = (profile?: string) => profile === 'vts_columns' ? 'Shared columns' : 'Native work queue'

// One parse gives both the tables and, for invalid text, why it is invalid.
function readSeeds(value: string | undefined): { seeds: LiveTableSandboxSeed[] | null; error: string | null } {
  try { return { seeds: parseLiveTableSandboxSeeds(value), error: null } } catch (cause) { return { seeds: null, error: (cause as Error).message } }
}

export default function LiveTableSandboxEditor({ value, onChange }: { value?: string; onChange: (value: string) => void }) {
  const { seeds, error } = readSeeds(value)
  // Tables once there are seeds to show; the view only changes on request,
  // so text being typed never turns into a table under the cursor.
  const [view, setView] = useState<'tables' | 'json'>(() => seeds?.length ? 'tables' : 'json')
  // Invalid text can only be fixed as text, and there is no table to show
  // until a seed declares one.
  const showJSON = view === 'json' || !seeds?.length
  const update = (index: number, change: (seed: LiveTableSandboxSeed) => LiveTableSandboxSeed) => {
    if (!seeds) return
    onChange(JSON.stringify(seeds.map((seed, position) => position === index ? change(seed) : seed), null, 2))
  }
  return <div className="full-width replay-variables-field">
    <h3>Isolated live tables</h3>
    <p>Provide explicit starting rows for the aliases used by live-table actions. Every replay starts fresh; sandbox operations never use a running table or save changes to a data pool. Seed values stay in this editor session and are excluded from saved debug profiles.</p>
    <p>Up to four tables and 64 KiB of seed data. Each table allows 32 columns, 256 rows and 256 KiB of runtime data. Leave a table without rows for producer scripts. Inspect action results through their result-prefix variables.</p>
    <div className="settings-inline-actions">
      <button type="button" className="btn btn-sm btn-secondary" disabled={!seeds?.length} onClick={() => setView(showJSON ? 'tables' : 'json')}>{showJSON ? 'Edit as tables' : 'Edit as JSON'}</button>
      <button type="button" className="btn btn-sm btn-secondary" disabled={Boolean(value?.trim() && value.trim() !== '[]')} onClick={() => { onChange(JSON.stringify([{ alias: 'work', profile: 'native_queue', columns: ['id'], rows: [] }], null, 2)); setView('tables') }}>Add empty sandbox example</button>
    </div>
    {showJSON ? <label><span>Sandbox seeds (JSON)</span><textarea aria-label="Sandbox seeds (JSON)" aria-invalid={Boolean(error)} value={value || ''} onChange={event => onChange(event.target.value)} rows={7} placeholder={'[{"alias":"work","profile":"native_queue","columns":["id"],"rows":[{"values":{"id":"sample-1"}}]}]'} /></label>
      : seeds.map((seed, index) => <section key={seed.alias} aria-label={`Sandbox table ${seed.alias}`}>
          <h4>{seed.alias} · {profileLabel(seed.profile)}</h4>
          <DataRowGrid
            label={`Starting rows for ${seed.alias}`}
            columns={seed.columns}
            rows={seed.rows.map((row, position) => ({ id: String(position), values: row.values }))}
            markEmpty
            maxColumns={SANDBOX_MAX_COLUMNS}
            emptyMessage="No starting rows; producers add them during the replay."
            onCellCommit={(row, column, cellValue) => update(index, current => ({ ...current, rows: current.rows.map((candidate, position) => String(position) === row.id ? { values: { ...candidate.values, [column]: cellValue } } : candidate) }))}
            onAddRows={added => update(index, current => ({ ...current, rows: [...current.rows, ...added.map(values => ({ values }))] }))}
            onAddColumn={name => update(index, current => ({ ...current, columns: [...current.columns, name] }))}
            rowActions={(row, rowNumber) => <button type="button" className="btn btn-sm btn-ghost" aria-label={`Remove row ${rowNumber} from ${seed.alias}`} onClick={() => update(index, current => ({ ...current, rows: current.rows.filter((_candidate, position) => String(position) !== row.id) }))}>Remove</button>}
          />
        </section>)}
    {!showJSON && <p>To rename a table or change its behavior, edit as JSON.</p>}
    {error && <p role="alert">{error}</p>}
    <p>Seed values can appear in explicitly inspected debugger results. Do not paste managed secrets. Empty strings and absent cells are preserved.</p>
  </div>
}
