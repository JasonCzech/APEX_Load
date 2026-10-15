import { useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import DialogLayer from './ux/DialogLayer'
import { useModalDialog } from '../hooks/useModalDialog'
import { dslActionTypesForProtocol, type DslCatalogVariant } from './dslActionCatalog'
import { humanActionType, type InsertPosition } from './authorExperience'
import { LINKED_TEMPLATES, linkedTemplateActions, sharedDataAction, suggestedResultPrefix, templateStepNames, type SharedDataContext, type TemplateSpot } from './sharedDataTemplates'
import { LIVE_TABLE_KIND_LABELS, LIVE_TABLE_OPERATIONS, type DataAliasUsage, type LiveTableKind } from './scriptDataUsageModel'
import type { AliasBinding } from './useScriptScenarioBindings'
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

// Shared-data entries: a live-table operation, or a linked template of several.
const OPERATION = 'live_table:'
const TEMPLATE = 'template:'
const ALIAS = /^[A-Za-z_][A-Za-z0-9_-]{0,127}$/
const RESULT_PREFIX = /^[A-Za-z_][A-Za-z0-9_.-]{0,127}$/
const NO_VARIANTS: DslCatalogVariant[] = []

/** What the insert dialog offers for shared data: the script's aliases, how scenarios bind them, and a step's wiring. */
export interface InsertSharedData {
  usage: DataAliasUsage[]
  bindings: Map<string, AliasBinding[]>
  /** What a new step at the insertion point can be wired to (fresh each call). */
  context: (alias: string) => SharedDataContext
}

/** The table kind an entry needs, if one. */
function entryKind(entry: string): LiveTableKind | undefined {
  const operations = entry.startsWith(TEMPLATE) ? LINKED_TEMPLATES.find(template => template.key === entry.slice(TEMPLATE.length))?.operations || [] : [entry.slice(OPERATION.length)]
  return operations.map(operation => LIVE_TABLE_OPERATIONS[operation]?.kind).find(Boolean)
}

export function AuthorInsertDialog({ protocol, seed, phase, position, anchorLabel, canInsertInside, onPlan, onInsert, onInsertMany, onClose, onOpenThinkTime, library, sharedData }: {
  protocol: string; seed: Action; phase: 'init' | 'action' | 'teardown'; position: InsertPosition
  anchorLabel?: string; canInsertInside: boolean
  onPlan: (phase: 'init' | 'action' | 'teardown', position: InsertPosition) => void
  onInsert: (action: Action) => void
  /** Several linked steps, with where the user's own steps go. */
  onInsertMany: (actions: Action[], label: string, spot?: TemplateSpot) => void
  onClose: () => void
  /** VuGen users look for lr_think_time here; this takes them to where it is set. */
  onOpenThinkTime?: () => void
  library: ReactNode
  sharedData?: InsertSharedData
}) {
  // What to add comes first, then its name, then where it goes.
  const id = useId()
  const dialogRef = useRef<HTMLDivElement | null>(null), firstRef = useRef<HTMLInputElement | null>(null), nameRef = useRef<HTMLInputElement | null>(null), aliasRef = useRef<HTMLInputElement | null>(null), prefixRef = useRef<HTMLInputElement | null>(null)
  useModalDialog({ dialogRef, initialFocusRef: firstRef, onClose })
  const [draft, setDraft] = useState<Action>({ ...seed, name: '' })
  const [entry, setEntry] = useState(seed.type)
  const [filter, setFilter] = useState('')
  const [error, setError] = useState('')
  const catalog = dslActionTypesForProtocol(protocol)
  const variants = (sharedData && catalog.find(spec => spec.type === 'live_table')?.variants) || NO_VARIANTS
  const specs = (catalog.length ? catalog : Array.from(new Set([seed.type, 'control_loop', 'control_if', 'control_switch', 'control_parallel'])).map(type => ({ type, summary: humanActionType(type), fields: [] })))
    .filter(spec => (!NOT_AUTHORABLE.has(spec.type) || spec.type === seed.type) && !(variants.length && spec.type === 'live_table'))
  const matches = (text: string) => text.toLowerCase().includes(filter.toLowerCase())
  const visibleSpecs = specs.filter(spec => spec.type === entry || matches(`${spec.type} ${spec.summary}`))
  const selectedSpec = specs.find(spec => spec.type === draft.type)
  // Shared data: the alias first used for table steps, else the first one a scenario supplies.
  const aliases = (sharedData?.usage || []).filter(usage => !usage.suppliedByScript || usage.operations.length > 0)
  const [alias, setAlias] = useState(() => (aliases.find(usage => usage.operations.length > 0) || aliases[0])?.alias || '')
  const [prefix, setPrefix] = useState('')
  const [edited, setEdited] = useState({ name: false, prefix: false })
  const variant = entry.startsWith(OPERATION) ? variants.find(candidate => candidate.value === entry.slice(OPERATION.length)) : undefined
  const template = entry.startsWith(TEMPLATE) ? LINKED_TEMPLATES.find(candidate => candidate.key === entry.slice(TEMPLATE.length)) : undefined
  const shared = Boolean(variant || template)
  const context = useMemo(() => sharedData?.context(alias.trim()) || null, [sharedData, alias])
  // The template's steps, for its description and the button (named for any table until one is entered).
  const preview = useMemo(() => template && sharedData ? linkedTemplateActions(template.key, variants, sharedData.context(ALIAS.test(alias.trim()) ? alias.trim() : 'table')) : null, [template, variants, sharedData, alias])
  const previewNames = preview ? templateStepNames(preview.actions) : []
  const isControl = !shared && draft.type.startsWith('control_')
  const needsTarget = !shared && !isControl && (selectedSpec?.fields?.some(field => field.name === 'target' && field.required) || draft.type === seed.type || draft.type === 'request')
  const sharedName = (next: DslCatalogVariant, nextAlias: string) => `${next.label}${nextAlias ? ` (${nextAlias})` : ''}`
  const changeType = (type: string) => {
    setEntry(type)
    setError('')
    if (type.startsWith(OPERATION) || type.startsWith(TEMPLATE)) {
      const next = type.startsWith(OPERATION) ? variants.find(candidate => candidate.value === type.slice(OPERATION.length)) : undefined
      if (next && !edited.name) setDraft({ ...draft, name: sharedName(next, alias.trim()) })
      if (next && !edited.prefix) setPrefix(suggestedResultPrefix(next.value, context?.taken || new Set()))
      return
    }
    const control = type === 'control_loop' ? { count: 1, steps: [] } : type === 'control_if' ? { condition: { op: 'exists', left: '' }, steps: [] } : type === 'control_switch' ? { cases: [], default: [] } : type === 'control_parallel' ? { branches: [{ name: 'Branch 1', steps: [] }, { name: 'Branch 2', steps: [] }], concurrency: 2 } : undefined
    setDraft({ name: edited.name ? draft.name : '', target: '', type, ...(type === seed.type ? { ...seed, name: edited.name ? draft.name : '' } : {}), ...(control ? { control } : {}) } as Action)
  }
  const changeAlias = (value: string) => {
    setAlias(value)
    if (variant && !edited.name) setDraft({ ...draft, name: sharedName(variant, value.trim()) })
  }
  const kind = entryKind(entry)
  const table = alias.trim()
  const boundOtherwise = kind ? (sharedData?.bindings.get(table) || []).filter(binding => binding.kind && binding.kind !== kind) : []
  const usedOtherwise = kind ? (sharedData?.usage.find(usage => usage.alias === table)?.kinds || []).filter(other => other !== kind) : []
  const optionLabel = (label: string, needs?: LiveTableKind) => needs ? `${label} (needs a ${LIVE_TABLE_KIND_LABELS[needs]} table)` : label
  const submit = () => {
    if (shared) {
      if (!ALIAS.test(table)) { setError('Enter the table alias: a letter or underscore, then letters, numbers, underscores or hyphens.'); aliasRef.current?.focus(); return }
      const wiring = sharedData!.context(table)
      if (template) {
        const made = linkedTemplateActions(template.key, variants, wiring)
        if (!made) { setError('This controller’s action catalog lacks a step this workflow needs.'); return }
        onInsertMany(made.actions, template.label, made.spot)
        return
      }
      if (!draft.name.trim()) { setError('Give the action a name.'); nameRef.current?.focus(); return }
      const saveAs = prefix.trim()
      if (!RESULT_PREFIX.test(saveAs)) { setError('Name where the results are saved: a letter or underscore, then letters, numbers, underscores, dots or hyphens.'); prefixRef.current?.focus(); return }
      if (wiring.taken.has(saveAs)) { setError(`Another step or data source already uses the name ${saveAs}. Choose another.`); prefixRef.current?.focus(); return }
      onInsert(sharedDataAction(variant!, draft.name.trim(), wiring, saveAs))
      return
    }
    if (!draft.name.trim()) { setError('Give the action a name.'); nameRef.current?.focus(); return }
    if (needsTarget && !draft.target?.trim()) { setError('Enter a target before inserting this request.'); dialogRef.current?.querySelector<HTMLInputElement>('[name="target"]')?.focus(); return }
    onInsert({ ...draft, name: draft.name.trim() })
  }
  const sharedGroups = [...new Set(variants.map(item => item.group))]
  const visibleTemplates = LINKED_TEMPLATES.filter(item => `${TEMPLATE}${item.key}` === entry || matches(`${item.label} ${item.summary} shared data table`))
  return createPortal(<DialogLayer><div className="modal-overlay"><div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="author-insert-title" className="modal author-insert-dialog">
    <h3 id="author-insert-title">Insert action or block</h3>
    <p>Choose what to add and where it belongs. Cancel leaves the document unchanged.</p>
    <fieldset className="author-insert-group"><legend>Type</legend>
      <label>Find action type<input ref={firstRef} type="search" autoComplete="off" value={filter} onChange={e => setFilter(e.target.value)} /></label>
      <label>Action type<select aria-label="Action type" value={entry} onChange={e => changeType(e.target.value)}>
        {GROUP_ORDER.slice(0, 2).map(group => {
          const members = visibleSpecs.filter(spec => actionGroup(spec.type) === group)
          return members.length ? <optgroup key={group} label={group}>{members.map(spec => <option key={spec.type} value={spec.type}>{humanActionType(spec.type)}</option>)}</optgroup> : null
        })}
        {variants.length > 0 && visibleTemplates.length > 0 && <optgroup label="Shared data: workflows">{visibleTemplates.map(item => <option key={item.key} value={`${TEMPLATE}${item.key}`}>{optionLabel(item.label, entryKind(`${TEMPLATE}${item.key}`))}</option>)}</optgroup>}
        {sharedGroups.map(group => {
          const members = variants.filter(item => item.group === group && (`${OPERATION}${item.value}` === entry || matches(`${item.label} ${item.group} ${item.value} shared data table`)))
          return members.length ? <optgroup key={group} label={`Shared data: ${group.toLowerCase()}`}>{members.map(item => <option key={item.value} value={`${OPERATION}${item.value}`}>{optionLabel(item.label, item.table_kind as LiveTableKind | undefined)}</option>)}</optgroup> : null
        })}
        {GROUP_ORDER.slice(2).map(group => {
          const members = visibleSpecs.filter(spec => actionGroup(spec.type) === group)
          return members.length ? <optgroup key={group} label={group}>{members.map(spec => <option key={spec.type} value={spec.type}>{humanActionType(spec.type)}</option>)}</optgroup> : null
        })}
      </select></label>
      {shared && <p className="author-insert-hint">Most scripts need no table steps: a scenario binding can read, remove or lease a row every iteration, and steps read it as <code>{'{{data.alias.column}}'}</code>. Add table steps to add rows, change a row you read, or lease work.</p>}
      {(variant?.summary || template?.summary || (!shared && selectedSpec?.summary)) && <p className="author-insert-hint">{variant?.summary || template?.summary || selectedSpec?.summary}</p>}
      {kind && <p className="author-insert-hint">Needs a {LIVE_TABLE_KIND_LABELS[kind]} table.{boundOtherwise.map(binding => ` ${table} is a ${LIVE_TABLE_KIND_LABELS[binding.kind!]} table in ${binding.scenarioName}.`).join('')}{usedOtherwise.length ? ` Other steps here use ${table} as a ${LIVE_TABLE_KIND_LABELS[usedOtherwise[0]]} table.` : ''}</p>}
      <p className="author-insert-hint">Think time and pacing are not actions; they are set in Runtime Settings.{onOpenThinkTime && <> <button type="button" className="link-button" onClick={onOpenThinkTime}>Open think time settings</button></>}</p>
    </fieldset>
    {shared ? <fieldset className="author-insert-group"><legend>{template ? 'Table' : 'Name and table'}</legend>
      {variant && <label>Action name<input ref={nameRef} value={draft.name} onChange={e => { setEdited({ ...edited, name: true }); setDraft({ ...draft, name: e.target.value }) }} /></label>}
      <label>Table alias<input ref={aliasRef} name="alias" value={alias} list={aliases.length ? `${id}-aliases` : undefined} placeholder="orders" onChange={e => changeAlias(e.target.value)} /></label>
      {aliases.length > 0 && <datalist id={`${id}-aliases`}>{aliases.map(usage => <option key={usage.alias} value={usage.alias}>{usage.alias}</option>)}</datalist>}
      {variant && <label>Save results as<input ref={prefixRef} name="result_prefix" value={prefix} onChange={e => { setEdited({ ...edited, prefix: true }); setPrefix(e.target.value) }} /></label>}
      {variant && RESULT_PREFIX.test(prefix.trim()) && <p className="author-insert-hint">Later steps read <code>{`{{var.${prefix.trim()}.values.column}}`}</code>, <code>{`{{var.${prefix.trim()}.record_id}}`}</code> and <code>{`{{var.${prefix.trim()}.status}}`}</code>.</p>}
      {preview && ALIAS.test(alias.trim()) && <p className="author-insert-hint">Inserts {previewNames.length === 1 ? 'one step' : `${previewNames.length} steps`}: {previewNames.join(', ')}.{preview.spot ? ` Your own steps go ${preview.spot.position} “${preview.spot.name}”.` : ''}</p>}
      <p className="author-insert-hint">The alias names a table the scenario binds; it is not a table itself. A scenario chooses the table for each alias.</p>
    </fieldset> : <fieldset className="author-insert-group"><legend>{isControl ? 'Name' : 'Name and target'}</legend>
      <label>Action name<input ref={nameRef} value={draft.name} onChange={e => { setEdited({ ...edited, name: true }); setDraft({ ...draft, name: e.target.value }) }} /></label>
      {!isControl && <label>Target{needsTarget ? ' (required)' : ' (optional)'}<input name="target" value={draft.target || ''} onChange={e => setDraft({ ...draft, target: e.target.value })} /></label>}
      <p className="author-insert-hint">Requests are measured under their action names.</p>
    </fieldset>}
    <fieldset className="author-insert-group"><legend>Place</legend>
      <div className="form-grid"><label>Phase<select aria-label="Phase" value={phase} onChange={e => onPlan(e.target.value as typeof phase, 'end')}><option value="init">Init</option><option value="action">Actions</option><option value="teardown">Teardown</option></select></label>
      <label>Placement<select aria-label="Placement" value={position} onChange={e => onPlan(phase, e.target.value as InsertPosition)}><option value="end">End of phase</option>{anchorLabel && <><option value="before">Before selected action</option><option value="after">After selected action</option>{canInsertInside && <option value="inside">Inside selected block</option>}</>}</select></label></div>
      <p role="status">Insert {position === 'end' ? `at the end of ${phase === 'action' ? 'Actions' : humanActionType(phase)}` : `${position} ${anchorLabel}`}.</p>
    </fieldset>
    {error && <p role="alert">{error}</p>}
    {library}
    <div className="author-edit-toolbar"><button className="btn btn-secondary" type="button" onClick={onClose}>Cancel</button><button className="btn btn-primary" type="button" onClick={submit}>{previewNames.length > 1 ? `Insert ${previewNames.length} steps` : 'Insert action'}</button></div>
  </div></div></DialogLayer>, document.body)
}
