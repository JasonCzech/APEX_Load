import { useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import DialogLayer from './ux/DialogLayer'
import { useModalDialog } from '../hooks/useModalDialog'
import { dslActionTypesForProtocol } from './dslActionCatalog'
import { humanActionType, type InsertPosition } from './authorExperience'
import type { ScriptActionDefinition as Action } from '../types/scripts'

// What an LRE engineer scans for first. Imported source markers record what a
// converted script contained; they come last, as advanced. "Review required"
// is produced by importers and is never authored by hand.
const NOT_AUTHORABLE = new Set(['review_required'])
function actionGroup(type: string): string {
  if (/^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)$/i.test(type) || type === 'request' || type.startsWith('http_')) return 'Requests'
  if (type.startsWith('control_') || type === 'break' || type === 'continue' || type === 'rendezvous') return 'Flow control'
  if (type.startsWith('source_')) return 'Imported source markers (advanced)'
  return 'Session, state and other'
}
const GROUP_ORDER = ['Requests', 'Flow control', 'Session, state and other', 'Imported source markers (advanced)']

export function AuthorInsertDialog({ protocol, seed, phase, position, anchorLabel, canInsertInside, onPlan, onInsert, onClose, library }: {
  protocol: string; seed: Action; phase: 'init' | 'action' | 'teardown'; position: InsertPosition
  anchorLabel?: string; canInsertInside: boolean
  onPlan: (phase: 'init' | 'action' | 'teardown', position: InsertPosition) => void
  onInsert: (action: Action) => void; onClose: () => void; library: ReactNode
}) {
  const dialogRef = useRef<HTMLDivElement | null>(null), firstRef = useRef<HTMLInputElement | null>(null)
  useModalDialog({ dialogRef, initialFocusRef: firstRef, onClose })
  const [draft, setDraft] = useState<Action>({ ...seed, name: '' })
  const [filter, setFilter] = useState('')
  const [error, setError] = useState('')
  const catalog = dslActionTypesForProtocol(protocol)
  const specs = (catalog.length ? catalog : Array.from(new Set([seed.type, 'control_loop', 'control_if', 'control_switch', 'control_parallel'])).map(type => ({ type, summary: humanActionType(type), fields: [] })))
    .filter(spec => !NOT_AUTHORABLE.has(spec.type) || spec.type === seed.type)
  const visibleSpecs = specs.filter(spec => spec.type === draft.type || `${spec.type} ${spec.summary}`.toLowerCase().includes(filter.toLowerCase()))
  const selectedSpec = specs.find(spec => spec.type === draft.type)
  const isControl = draft.type.startsWith('control_')
  const needsTarget = !isControl && (selectedSpec?.fields?.some(field => field.name === 'target' && field.required) || draft.type === seed.type || draft.type === 'request')
  const changeType = (type: string) => {
    const control = type === 'control_loop' ? { count: 1, steps: [] } : type === 'control_if' ? { condition: { op: 'exists', left: '' }, steps: [] } : type === 'control_switch' ? { cases: [], default: [] } : type === 'control_parallel' ? { branches: [{ name: 'Branch 1', steps: [] }, { name: 'Branch 2', steps: [] }], concurrency: 2 } : undefined
    setDraft({ name: draft.name, target: '', type, ...(type === seed.type ? { ...seed, name: draft.name } : {}), ...(control ? { control } : {}) } as Action)
    setError('')
  }
  const submit = () => {
    if (!draft.name.trim()) { setError('Give the action a name.'); firstRef.current?.focus(); return }
    if (needsTarget && !draft.target?.trim()) { setError('Enter a target before inserting this request.'); dialogRef.current?.querySelector<HTMLInputElement>('[name="target"]')?.focus(); return }
    onInsert({ ...draft, name: draft.name.trim() })
  }
  return createPortal(<DialogLayer><div className="modal-overlay"><div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="author-insert-title" className="modal author-insert-dialog">
    <h3 id="author-insert-title">Insert action or block</h3>
    <p>Choose what to add and where it belongs. Cancel leaves the document unchanged.</p>
    <div className="form-grid"><label>Phase<select aria-label="Phase" value={phase} onChange={e => onPlan(e.target.value as typeof phase, 'end')}><option value="init">Init</option><option value="action">Actions</option><option value="teardown">Teardown</option></select></label>
    <label>Placement<select aria-label="Placement" value={position} onChange={e => onPlan(phase, e.target.value as InsertPosition)}><option value="end">End of phase</option>{anchorLabel && <><option value="before">Before selected action</option><option value="after">After selected action</option>{canInsertInside && <option value="inside">Inside selected block</option>}</>}</select></label></div>
    <p role="status">Insert {position === 'end' ? `at the end of ${phase === 'action' ? 'Actions' : humanActionType(phase)}` : `${position} ${anchorLabel}`}.</p>
    <label>Action name<input ref={firstRef} value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} /></label>
    <label>Find action type<input type="search" value={filter} onChange={e => setFilter(e.target.value)} /></label>
    <label>Action type<select aria-label="Action type" value={draft.type} onChange={e => changeType(e.target.value)}>{GROUP_ORDER.map(group => {
      const members = visibleSpecs.filter(spec => actionGroup(spec.type) === group)
      return members.length ? <optgroup key={group} label={group}>{members.map(spec => <option key={spec.type} value={spec.type}>{humanActionType(spec.type)}</option>)}</optgroup> : null
    })}</select></label>
    <p className="author-insert-hint">Requests are measured under their action names. Think time and pacing are set in Runtime Settings, not inserted as actions.</p>
    <p>{selectedSpec?.summary}</p>
    {!isControl && <label>Target{needsTarget ? ' (required)' : ' (optional)'}<input name="target" value={draft.target || ''} onChange={e => setDraft({ ...draft, target: e.target.value })} /></label>}
    {error && <p role="alert">{error}</p>}
    {library}
    <div className="author-edit-toolbar"><button className="btn btn-secondary" type="button" onClick={onClose}>Cancel</button><button className="btn btn-primary" type="button" onClick={submit}>Insert action</button></div>
  </div></div></DialogLayer>, document.body)
}
