import { useEffect, useId, useState, type ReactNode } from 'react'

import type { ScriptActionDefinition } from '../types/scripts'
import { dslSpecForActionType, useDslActionCatalog, type DslCatalogAction, type DslCatalogField, type DslCatalogVariant } from './dslActionCatalog'
import {
  VARIANT_ACTION_TYPES,
  catalogFieldLabel,
  activeVariant,
  chosenChoice,
  fieldUpdate,
  fieldValue,
  formFields,
  int64Problem,
  liveTableColumnProblem,
  paramsForChoice,
  strayParams,
  switchOperation,
  type LiveTableFormContext,
} from './catalogActionFormModel'
import ColumnChips from './ux/ColumnChips'
import DataRowGrid from './ux/DataRowGrid'
import ReferenceField from './ReferenceField'
import './CatalogActionForm.css'

type Action = ScriptActionDefinition

interface Props {
  action: Action
  index: number
  onUpdate: (index: number, field: keyof Action, value: unknown) => void
  /** What the script offers live-table fields: aliases, columns and earlier outputs. */
  context?: LiveTableFormContext
  /** Opens the action's JSON, for what the form cannot edit. */
  onOpenSource?: () => void
}

function SourceLink({ onOpenSource }: { onOpenSource?: () => void }) {
  return onOpenSource ? <button type="button" className="btn btn-secondary btn-sm" onClick={onOpenSource}>Edit in DSL</button> : null
}

/**
 * The editor for an action that has no hand-built one: its fields come from
 * the action catalog, the validator's own contract. An action with
 * operations (a live table) picks one by plain verb and shows only the
 * fields it takes. Fields the form does not show are listed, never dropped,
 * and the form never guesses: while the catalog loads it says so.
 */
export default function CatalogActionForm({ action, index, onUpdate, context, onOpenSource }: Props) {
  const { catalog, status, retry } = useDslActionCatalog()
  if (status === 'loading') return <p className="form-hint full-width catalog-form-state" role="status">Loading this action’s fields…</p>
  if (status === 'failed' || !catalog) {
    return <div className="form-hint full-width catalog-form-state" role="alert">
      Couldn’t load this action’s fields from the controller.{' '}
      <button type="button" className="btn btn-secondary btn-sm" onClick={retry}>Try again</button>{' '}
      <SourceLink onOpenSource={onOpenSource} />
    </div>
  }
  const spec = dslSpecForActionType(action.type, catalog)
  if (!spec) {
    return <div className="form-hint full-width catalog-form-state">
      The controller does not describe the <code>{action.type}</code> action, so it can only be edited in DSL. <SourceLink onOpenSource={onOpenSource} />
    </div>
  }
  if (VARIANT_ACTION_TYPES.has(spec.type) && !spec.variants?.length) {
    return <div className="form-hint full-width catalog-form-state">
      This editor needs a newer controller to edit this action’s operations; edit it in DSL meanwhile. <SourceLink onOpenSource={onOpenSource} />
    </div>
  }
  return <CatalogFields spec={spec} action={action} index={index} onUpdate={onUpdate} context={context} onOpenSource={onOpenSource} />
}

function CatalogFields({ spec, action, index, onUpdate, context, onOpenSource }: Props & { spec: DslCatalogAction }) {
  const id = useId()
  const variant = activeVariant(spec, action)
  const variants = spec.variants || []
  const [pendingSwitch, setPendingSwitch] = useState<{ variant: DslCatalogVariant; removed: string[] } | null>(null)
  const discriminatorKey = spec.discriminator?.startsWith('params.') ? spec.discriminator.slice('params.'.length) : ''
  const set = (path: string, value: unknown) => {
    const change = fieldUpdate(action, path, value)
    onUpdate(index, change.key, change.value)
  }
  const choose = (next: DslCatalogVariant) => {
    const switched = switchOperation(spec, action, next)
    if (switched.removed.length) { setPendingSwitch({ variant: next, removed: switched.removed }); return }
    onUpdate(index, 'params', switched.params)
  }
  const fields = formFields(spec, variant)
  const choices = variant?.choices || []
  const choice = variant && choices.length ? chosenChoice(variant, action) : -1
  const hiddenByChoice = new Set(choices.flatMap((fieldsOfChoice, at) => at === choice ? [] : fieldsOfChoice).filter(name => !choices[choice]?.includes(name)))
  const required = new Set([...(spec.fields || []).filter(field => field.required).map(field => field.name), ...(variant?.required || []), ...(choices[choice] || [])])
  const shown = fields.filter(field => !hiddenByChoice.has(field.name))
  const basic = shown.filter(field => !field.advanced)
  const advanced = shown.filter(field => field.advanced)
  const stray = strayParams(spec, variant, action)
  const groups = [...new Set(variants.map(item => item.group))]

  const renderField = (field: DslCatalogField) => (
    <CatalogField key={field.name} field={field} action={action} required={required.has(field.name)} count={variant?.counts?.[field.name]} context={context} onChange={value => set(field.name, value)} />
  )

  return (
    <div className="catalog-action-form full-width" aria-label={`${spec.type} settings`}>
      {variants.length > 0 && (
        <div className="catalog-operation">
          <label>
            <span>Operation</span>
            <select value={variant?.value || ''} aria-describedby={`${id}-operation`} onChange={event => { const next = variants.find(item => item.value === event.target.value); if (next) choose(next) }}>
              {!variant && <option value="">Choose an operation</option>}
              {groups.map(group => <optgroup key={group} label={group}>
                {variants.filter(item => item.group === group).map(item => <option key={item.value} value={item.value}>{item.label}</option>)}
              </optgroup>)}
            </select>
          </label>
          <p id={`${id}-operation`} className="form-hint">
            {variant ? <>{variant.summary}{variant.mutates ? ' It changes the table.' : ' It does not change the table.'}{variant.table_kind ? ` It needs a ${variant.table_kind === 'native_queue' ? 'work-queue' : 'shared-columns'} table.` : ''}</> : spec.summary}
          </p>
          {pendingSwitch && (
            <div className="catalog-operation-switch" role="alertdialog" aria-labelledby={`${id}-switch`}>
              <p id={`${id}-switch`}>{pendingSwitch.variant.label} does not take {pendingSwitch.removed.map(key => catalogFieldLabel((spec.fields || []).find(field => field.name === `params.${key}`) || { name: key })).join(', ')}. Switching removes {pendingSwitch.removed.length === 1 ? 'it' : 'them'}.</p>
              <button type="button" className="btn btn-primary btn-sm" onClick={() => { onUpdate(index, 'params', switchOperation(spec, action, pendingSwitch.variant).params); setPendingSwitch(null) }}>Switch</button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setPendingSwitch(null)}>Cancel</button>
            </div>
          )}
        </div>
      )}
      {(!variants.length || variant) && <>
        {variant && choices.length > 1 && (
          <fieldset className="catalog-choice">
            <legend>Find the row by</legend>
            {choices.map((fieldsOfChoice, at) => (
              <label key={fieldsOfChoice.join('+')} className="checkbox-label">
                <input type="radio" name={`${id}-choice`} checked={choice === at} onChange={() => onUpdate(index, 'params', paramsForChoice(variant, action, at))} />
                <span>{fieldsOfChoice.map(name => catalogFieldLabel((spec.fields || []).find(field => field.name === name) || { name })).join(' and ')}</span>
              </label>
            ))}
          </fieldset>
        )}
        <div className="form-grid catalog-fields">{basic.map(renderField)}</div>
        {advanced.length > 0 && <details className="sub-section catalog-advanced">
          <summary>More settings</summary>
          <div className="form-grid catalog-fields">{advanced.map(renderField)}</div>
        </details>}
      </>}
      {discriminatorKey && !variant && fieldValue(action, spec.discriminator!) !== undefined && (
        <p className="form-hint" role="alert">The operation <code>{String(fieldValue(action, spec.discriminator!))}</code> is not one this controller knows; choose one, or edit it in DSL. <SourceLink onOpenSource={onOpenSource} /></p>
      )}
      {stray.length > 0 && (
        <div className="sub-section catalog-stray" role="group" aria-label="Not recognised">
          <h4>{variant ? `Not used by “${variant.label}”` : 'Not recognised'}</h4>
          <p className="form-hint">{variant ? 'This operation does not take these settings; the controller rejects the action until they are removed.' : 'The controller does not describe these settings.'}</p>
          <ul>
            {stray.map(item => <li key={item.key}>
              <code>{item.key}</code> <span className="catalog-stray-value">{JSON.stringify(item.value)}</span>{' '}
              <button type="button" className="btn btn-ghost btn-sm" aria-label={`Remove ${item.key}`} onClick={() => set(`params.${item.key}`, undefined)}>Remove</button>
            </li>)}
          </ul>
        </div>
      )}
    </div>
  )
}

interface FieldProps {
  field: DslCatalogField
  action: Action
  required: boolean
  /** How many items the field takes, when fixed. */
  count?: number
  context?: LiveTableFormContext
  onChange: (value: unknown) => void
}

function CatalogField({ field, action, required, count, context, onChange }: FieldProps) {
  const id = useId()
  const value = fieldValue(action, field.name)
  const label = catalogFieldLabel(field)
  const hint = field.doc ? <small id={`${id}-doc`}>{field.doc}</small> : null
  const describedBy = field.doc ? `${id}-doc` : undefined
  const caption = <span>{label}{required ? <span className="catalog-required"> (required)</span> : null}</span>
  const widget = field.widget || ''
  const alias = typeof fieldValue(action, 'params.alias') === 'string' ? String(fieldValue(action, 'params.alias')) : ''

  if (widget === 'columns') return <ColumnChips className="full-width" label={label} caption={caption} hint={hint} columns={Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []} known={context?.columnsByAlias[alias] || []} count={count} problem={liveTableColumnProblem} onChange={next => onChange(next.length ? next : undefined)} />
  if (widget === 'values') return <ValuesField label={label} caption={caption} hint={hint} values={isStringRecord(value) ? value : {}} known={context?.columnsByAlias[alias] || []} onChange={next => onChange(Object.keys(next).length ? next : undefined)} />
  if (widget === 'enum' || (field.kind === 'enum' && field.enum_labels)) {
    return <fieldset className="catalog-enum full-width" aria-describedby={describedBy}>
      <legend>{caption}</legend>
      {!required && <label className="checkbox-label"><input type="radio" name={id} checked={value === undefined} onChange={() => onChange(undefined)} /><span>Default</span></label>}
      {(field.enum || []).map(option => <label key={option} className="checkbox-label"><input type="radio" name={id} checked={value === option} onChange={() => onChange(option)} /><span>{field.enum_labels?.[option] || option}</span></label>)}
      {hint}
    </fieldset>
  }
  if (field.kind === 'enum') {
    return <label><span>{caption}</span>
      <select value={typeof value === 'string' ? value : ''} aria-describedby={describedBy} onChange={event => onChange(event.target.value || undefined)}>
        {!required && <option value="">Default</option>}
        {value !== undefined && typeof value === 'string' && !(field.enum || []).includes(value) && <option value={value}>{value}</option>}
        {(field.enum || []).map(option => <option key={option} value={option}>{field.enum_labels?.[option] || option}</option>)}
      </select>{hint}</label>
  }
  if (field.kind === 'bool') {
    return <label className="checkbox-label full-width"><input type="checkbox" checked={value === true} aria-describedby={describedBy} onChange={event => onChange(event.target.checked ? true : undefined)} /><span>{label}</span>{hint}</label>
  }
  if (widget === 'int64' || field.kind === 'int64') return <Int64Field caption={caption} hint={hint} describedBy={describedBy} value={value} onChange={onChange} />
  if (field.kind === 'int' || field.kind === 'float') {
    const unit = widget === 'duration-ms' ? 'ms' : ''
    return <label><span>{caption}</span>
      <span className="catalog-number">
        <input type="number" inputMode={field.kind === 'int' ? 'numeric' : 'decimal'} step={field.kind === 'int' ? 1 : 'any'} max={field.maximum || undefined} value={typeof value === 'number' ? value : ''} placeholder={field.placeholder} aria-describedby={describedBy}
          onChange={event => onChange(event.target.value === '' ? undefined : field.kind === 'int' ? Math.trunc(Number(event.target.value)) : Number(event.target.value))} />
        {unit && <span className="catalog-unit">{unit}</span>}
      </span>
      {field.maximum ? <small>At most {field.maximum.toLocaleString()}{unit ? ` ${unit}` : ''}.</small> : null}{hint}</label>
  }
  if (field.kind === 'object' || field.kind === 'array' || field.kind === 'any') return <JsonField caption={caption} hint={hint} describedBy={describedBy} value={value} kind={field.kind} onChange={onChange} />
  const suggestions = widget === 'alias' ? context?.aliases || [] : widget === 'output-ref' ? context?.outputs[(field.output_kind || '') as keyof LiveTableFormContext['outputs']] || [] : []
  const text = typeof value === 'string' ? value : value === undefined ? '' : String(value)
  // A top-level field (target, value) stays a string when cleared; a nested one goes.
  const write = (next: string) => onChange(next === '' && field.name.includes('.') ? undefined : next)
  // A message body keeps its line breaks.
  // Every text field takes references (the engine resolves templates in them).
  const path = field.name.split('.')
  if (field.name === 'body' || text.includes('\n')) {
    return <label className="full-width"><span>{caption}</span>
      <ReferenceField multiline rows={4} value={text} onChange={write} field={path} label={label} inputProps={{ placeholder: field.placeholder, spellCheck: false, 'aria-describedby': describedBy, 'aria-required': required || undefined }} />{hint}</label>
  }
  return <label className={widget === 'output-ref' || field.name === 'target' ? 'full-width' : undefined}><span>{caption}</span>
    <ReferenceField value={text} onChange={write} field={path} label={label} inputProps={{ placeholder: field.placeholder, list: suggestions.length ? `${id}-suggestions` : undefined, 'aria-describedby': describedBy, 'aria-required': required || undefined }} />
    {suggestions.length > 0 && <datalist id={`${id}-suggestions`}>{suggestions.map(item => <option key={item} value={item}>{item}</option>)}</datalist>}
    {widget === 'output-ref' && suggestions.length === 0 && field.output_kind ? <small>No earlier step saves a {field.output_kind === 'lease' ? 'lease' : field.output_kind.replace('_', ' ')} yet; give one a “Save results as” name.</small> : null}
    {hint}</label>
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && Object.values(value).every(item => typeof item === 'string')
}

function Int64Field({ caption, hint, describedBy, value, onChange }: { caption: ReactNode; hint: ReactNode; describedBy?: string; value: unknown; onChange: (value: unknown) => void }) {
  const id = useId()
  const stored = typeof value === 'string' ? value : typeof value === 'number' ? String(value) : ''
  const [text, setText] = useState(stored)
  useEffect(() => { setText(current => current === stored || (current !== '' && int64Problem(current)) ? current : stored) }, [stored])
  const problem = text === '' ? '' : int64Problem(text)
  return <label><span>{caption}</span>
    <input type="text" inputMode="numeric" value={text} aria-invalid={Boolean(problem)} aria-describedby={[problem ? `${id}-problem` : '', describedBy || ''].filter(Boolean).join(' ') || undefined}
      onChange={event => { const next = event.target.value.trim(); setText(next); if (next === '') onChange(undefined); else if (!int64Problem(next)) onChange(next) }} />
    {problem && <small id={`${id}-problem`} role="alert">{problem}</small>}{hint}</label>
}

function JsonField({ caption, hint, describedBy, value, kind, onChange }: { caption: ReactNode; hint: ReactNode; describedBy?: string; value: unknown; kind: string; onChange: (value: unknown) => void }) {
  const id = useId()
  const stored = value === undefined ? '' : JSON.stringify(value, null, 2)
  const [text, setText] = useState(stored)
  const [problem, setProblem] = useState('')
  useEffect(() => { setText(stored); setProblem('') }, [stored])
  const commit = () => {
    if (text.trim() === '') { setProblem(''); if (value !== undefined) onChange(undefined); return }
    try {
      const parsed: unknown = JSON.parse(text)
      if (kind === 'object' && (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed))) throw new Error('Enter a JSON object, such as {"key": "value"}.')
      if (kind === 'array' && !Array.isArray(parsed)) throw new Error('Enter a JSON list, such as ["a", "b"].')
      setProblem('')
      if (JSON.stringify(parsed) !== JSON.stringify(value)) onChange(parsed)
    } catch (error) {
      setProblem(error instanceof SyntaxError ? 'Enter valid JSON.' : error instanceof Error ? error.message : 'Enter valid JSON.')
    }
  }
  return <label className="full-width"><span>{caption}</span>
    <textarea rows={3} spellCheck={false} value={text} aria-invalid={Boolean(problem)} aria-describedby={[problem ? `${id}-problem` : '', describedBy || ''].filter(Boolean).join(' ') || undefined} onChange={event => setText(event.target.value)} onBlur={commit} />
    {problem && <small id={`${id}-problem`} role="alert">{problem}</small>}{hint}</label>
}

function ValuesField({ label, caption, hint, values, known, onChange }: { label: string; caption: ReactNode; hint: ReactNode; values: Record<string, string>; known: string[]; onChange: (values: Record<string, string>) => void }) {
  // Columns offered without a value yet (added here, or used elsewhere in the
  // script) stay absent until typed into; one taken away is not offered again.
  const [extra, setExtra] = useState<string[]>([])
  const [hidden, setHidden] = useState<string[]>([])
  const [problem, setProblem] = useState('')
  const present = Object.keys(values)
  const columns = [...new Set([...present, ...extra, ...known])].filter(column => present.includes(column) || !hidden.includes(column))
  return <div className="catalog-values full-width" role="group" aria-label={label}>
    <span>{caption}</span>
    <DataRowGrid
      label={label}
      columns={columns}
      rows={[{ id: 'values', values }]}
      markEmpty
      onCellCommit={(_row, column, value) => { onChange({ ...values, [column]: value }); return true }}
      onAddColumn={name => {
        // The grid checks names by the data-pool rule; a live-table column is stricter.
        const why = liveTableColumnProblem(name.trim(), columns)
        setProblem(why)
        if (why) return false
        setExtra(previous => [...previous, name.trim()])
        setHidden(previous => previous.filter(column => column !== name.trim()))
        return true
      }}
      onRemoveColumn={name => { setExtra(previous => previous.filter(column => column !== name)); if (known.includes(name)) setHidden(previous => [...previous, name]); return true }}
    />
    {problem && <small role="alert">{problem}</small>}
    {present.length > 0 && <p className="catalog-values-set">
      Set: {present.map(column => <span key={column} className="column-chip"><code>{column}</code><button type="button" className="btn btn-ghost btn-sm" aria-label={`Leave ${column} absent`} onClick={() => { const next = { ...values }; delete next[column]; onChange(next) }}>×</button></span>)}
    </p>}
    {hint}
  </div>
}
