import { useState } from 'react'

export default function ParameterRenameInput({ name, onRename }: { name: string; onRename: (name: string) => void }) {
  const [draft, setDraft] = useState(name)
  const changed = draft.trim() !== name
  return <div className="parameter-rename-control">
    <input aria-label="Parameter name" value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') setDraft(name) }} />
    {changed && <>
      <p>Rename <code>{name}</code> to <code>{draft}</code> and update its template and same-row references together. See Usage for affected references.</p>
      <button className="btn btn-sm btn-secondary" type="button" onClick={() => onRename(draft)}>Rename references</button>
      <button className="btn btn-sm btn-ghost" type="button" onClick={() => setDraft(name)}>Cancel</button>
    </>}
  </div>
}
