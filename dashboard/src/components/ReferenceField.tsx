import { useEffect, useId, useMemo, useRef, useState, type InputHTMLAttributes, type KeyboardEvent, type ReactNode } from 'react'

import { REFERENCE_GROUP_LABELS, unresolvedReferences, type ReferenceGroup, type ReferenceItem, type UnresolvedReference } from './referenceScopeModel'
import { useReferenceFieldContext, type FieldPath } from './referenceFieldContext'
import './ReferenceField.css'

const GROUP_ORDER = Object.keys(REFERENCE_GROUP_LABELS) as ReferenceGroup[]

/** A picker's search box takes focus when it opens (the Insert button opened it). */
const focusOnMount = (element: HTMLInputElement | null) => { element?.focus() }
const MAX_OPTIONS = 200

/**
 * The scope's items matching a query, in group order. A search also matches
 * what an item is ("jsonpath", "parameter"); typing after {{ matches the
 * reference itself.
 */
function matching(items: ReferenceItem[], query: string, inline = false): ReferenceItem[] {
  const needle = query.trim().toLowerCase()
  const found = needle ? items.filter(item => item.expression.toLowerCase().includes(needle) || (!inline && item.detail.toLowerCase().includes(needle))) : items
  return GROUP_ORDER.flatMap(group => found.filter(item => item.group === group)).slice(0, MAX_OPTIONS)
}

function ReferenceOptions({ id, options, active, onPick, onHover }: { id: string; options: ReferenceItem[]; active: number; onPick: (item: ReferenceItem) => void; onHover: (index: number) => void }) {
  if (!options.length) return <p className="reference-picker-empty">Nothing matches.</p>
  const rows: ReactNode[] = []
  let group: ReferenceGroup | null = null
  options.forEach((item, index) => {
    if (item.group !== group) {
      group = item.group
      rows.push(<li key={`group-${group}`} role="presentation" className="reference-picker-group">{REFERENCE_GROUP_LABELS[group]}</li>)
    }
    rows.push(
      <li key={item.expression} id={`${id}-option-${index}`} role="option" aria-selected={index === active} className={index === active ? 'active' : undefined}
        onMouseDown={event => event.preventDefault()} onMouseEnter={() => onHover(index)} onClick={() => onPick(item)} onKeyDown={event => { if (event.key === 'Enter') onPick(item) }}>
        <code>{`{{${item.expression}}}`}</code>
        <small>{item.mayBeUnset ? 'May be unset here · ' : ''}{item.detail}</small>
      </li>,
    )
  })
  return <ul id={`${id}-listbox`} role="listbox" aria-label="References" className="reference-picker-options">{rows}</ul>
}

interface Props {
  value: string
  onChange: (value: string) => void
  /** The field's path in the selected step, for "Make this a parameter". */
  field: FieldPath
  /** What the field is called, for its Insert reference button. */
  label: string
  multiline?: boolean
  rows?: number
  inputProps?: InputHTMLAttributes<HTMLInputElement> & { 'data-author-field'?: string }
}

/**
 * A text field the engine resolves templates in. Insert reference (or typing
 * {{) lists what exists before this step; the editor writes the syntax. A
 * reference nothing defines is flagged under the field, with fixes.
 */
export default function ReferenceField({ value, onChange, field, label, multiline = false, rows = 3, inputProps = {} }: Props) {
  const id = useId()
  const { scope, onMakeParameter, registerInsertTarget, releaseInsertTarget } = useReferenceFieldContext()
  const element = useRef<HTMLInputElement & HTMLTextAreaElement>(null)
  const selection = useRef({ from: value.length, to: value.length })
  // The picker opened from the button (with its own search), or inline while typing after {{.
  const [picker, setPicker] = useState<{ mode: 'search'; query: string } | { mode: 'inline'; start: number; query: string } | null>(null)
  const [active, setActive] = useState(0)
  const options = useMemo(() => scope && picker ? matching(scope.items, picker.query, picker.mode === 'inline') : [], [picker, scope])
  const unresolved = useMemo(() => scope ? unresolvedReferences(value, scope) : [], [scope, value])

  useEffect(() => { setActive(0) }, [picker?.query, picker?.mode])
  useEffect(() => {
    if (!picker) return undefined
    const close = (event: MouseEvent) => { if (!(event.target instanceof Node) || !element.current?.parentElement?.contains(event.target)) setPicker(null) }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [picker])

  const remember = () => {
    const target = element.current
    if (target) selection.current = { from: target.selectionStart ?? value.length, to: target.selectionEnd ?? value.length }
  }
  const placeCaret = (at: number) => {
    requestAnimationFrame(() => {
      const target = element.current
      if (!target) return
      target.focus()
      target.setSelectionRange(at, at)
      selection.current = { from: at, to: at }
    })
  }
  const replaceRange = (from: number, to: number, text: string) => {
    onChange(`${value.slice(0, from)}${text}${value.slice(to)}`)
    placeCaret(from + text.length)
  }
  const insert = (expression: string) => {
    const { from, to } = selection.current
    replaceRange(Math.min(from, value.length), Math.min(to, value.length), `{{${expression}}}`)
  }
  // "This step produces" › Insert reaches this field through one stable handle
  // that always inserts into the current value, until the field goes away.
  const insertLatest = useRef(insert)
  insertLatest.current = insert
  const [insertHandle] = useState(() => (expression: string) => insertLatest.current(expression))
  const releaseLatest = useRef(releaseInsertTarget)
  releaseLatest.current = releaseInsertTarget
  useEffect(() => () => releaseLatest.current?.(insertHandle), [insertHandle])
  const pick = (item: ReferenceItem) => {
    if (picker?.mode === 'inline') {
      const caret = picker.start + 2 + picker.query.length
      // The rest of a reference the caret is inside (and its closing braces) is replaced too.
      const rest = /^[\w.()-]*\}\}/.exec(value.slice(caret))
      replaceRange(picker.start, caret + (rest ? rest[0].length : 0), `{{${item.expression}}}`)
    } else {
      insert(item.expression)
    }
    setPicker(null)
  }
  const pickerKeys = (event: KeyboardEvent) => {
    if (!picker) return false
    if (event.key === 'ArrowDown') setActive(index => Math.min(options.length - 1, index + 1))
    else if (event.key === 'ArrowUp') setActive(index => Math.max(0, index - 1))
    else if ((event.key === 'Enter' || (event.key === 'Tab' && picker.mode === 'inline')) && options[active]) pick(options[active])
    else if (event.key === 'Escape') { setPicker(null); if (picker.mode === 'search') element.current?.focus() }
    else return false
    event.preventDefault()
    return true
  }
  const typed = (next: string, caret: number) => {
    onChange(next)
    // Typing after {{ lists references in place, narrowed by what follows.
    const match = /\{\{([\w.()-]*)$/.exec(next.slice(0, caret))
    setPicker(match && scope ? { mode: 'inline', start: caret - match[0].length, query: match[1] } : null)
  }

  const shared = {
    ref: element,
    value,
    onChange: (event: { target: HTMLInputElement | HTMLTextAreaElement }) => typed(event.target.value, event.target.selectionStart ?? event.target.value.length),
    onSelect: remember,
    onKeyUp: remember,
    // Leaving the field closes the in-place list (picking an option does not take focus).
    onBlur: () => { remember(); setPicker(current => current?.mode === 'inline' ? null : current) },
    onFocus: () => registerInsertTarget?.(insertHandle),
    onKeyDown: (event: KeyboardEvent<HTMLInputElement & HTMLTextAreaElement>) => { pickerKeys(event) },
    // Named by the field's label alone: the Insert button and warnings sit inside the same <label>.
    'aria-label': inputProps['aria-label'] ?? label,
    'aria-describedby': [inputProps['aria-describedby'], unresolved.length ? `${id}-unresolved` : ''].filter(Boolean).join(' ') || undefined,
    'aria-invalid': unresolved.length ? true : inputProps['aria-invalid'],
  }

  return (
    <span className={`reference-field${multiline ? ' multiline' : ''}`}>
      <span className="reference-field-control">
        {multiline
          ? <textarea {...(inputProps as InputHTMLAttributes<HTMLTextAreaElement>)} {...shared} rows={rows} />
          : <input {...inputProps} type="text" {...shared} />}
        {scope && <button type="button" className="btn btn-ghost btn-sm reference-insert" aria-label={`Insert a reference into ${label}`} aria-haspopup="listbox" aria-expanded={picker?.mode === 'search'}
          onMouseDown={remember} onClick={() => setPicker(current => current?.mode === 'search' ? null : { mode: 'search', query: '' })}>{'{ }'}</button>}
        {picker && scope && (
          <span className="reference-picker" role="dialog" aria-label={`References for ${label}`}>
            {picker.mode === 'search' && <input ref={focusOnMount} type="search" className="reference-picker-search" role="combobox" aria-expanded="true" aria-controls={`${id}-listbox`} aria-activedescendant={options[active] ? `${id}-option-${active}` : undefined} aria-label="Find a reference" placeholder="Find a reference" value={picker.query}
              onChange={event => setPicker({ mode: 'search', query: event.target.value })} onKeyDown={event => { pickerKeys(event) }} />}
            <ReferenceOptions id={id} options={options} active={active} onPick={pick} onHover={setActive} />
            {picker.mode === 'search' && onMakeParameter && value.trim() && (
              <button type="button" className="btn btn-secondary btn-sm reference-make-parameter" onClick={() => {
                const { from, to } = selection.current
                const whole = from === to
                setPicker(null)
                onMakeParameter({ field, value, from: whole ? 0 : Math.min(from, to), to: whole ? value.length : Math.max(from, to) })
              }}>{selection.current.from !== selection.current.to ? 'Make the selected text a parameter' : 'Make this a parameter'}</button>
            )}
            {picker.mode === 'inline' && <span className="sr-only" aria-live="polite">{options.length ? `${options.length} references; ${options[active]?.expression || ''}. Up and down choose, Enter inserts.` : 'No reference matches.'}</span>}
          </span>
        )}
      </span>
      <ReferenceWarnings id={`${id}-unresolved`} value={value} unresolved={unresolved} onReplace={replaceRange} />
    </span>
  )
}

/**
 * The references in a value that nothing defines before the step, each with
 * fixes: a similar name, an extractor, or a parameter.
 */
export function ReferenceWarnings({ id, value, unresolved: given, onReplace }: {
  id?: string
  value: string
  /** The value's unresolved references, when the caller has them already. */
  unresolved?: UnresolvedReference[]
  onReplace: (from: number, to: number, text: string) => void
}) {
  const { scope, onAddExtractor, onCreateParameter } = useReferenceFieldContext()
  const computed = useMemo(() => !given && scope ? unresolvedReferences(value, scope) : [], [given, scope, value])
  const unresolved = given || computed
  if (!unresolved.length) return null
  return (
    <span id={id} className="reference-warnings">
      {unresolved.map(item => (
        <span key={`${item.token.from}:${item.token.text}`} className="reference-warning">
          <span>{item.message}.</span>
          {item.suggestions.map(suggestion => <button key={suggestion} type="button" className="btn btn-ghost btn-sm" onClick={() => onReplace(item.token.from, item.token.to, `{{${suggestion}}}`)}>Use {`{{${suggestion}}}`}</button>)}
          {item.token.root === 'var' && onAddExtractor && <button type="button" className="btn btn-ghost btn-sm" onClick={() => onAddExtractor(item.name)}>Add an extractor</button>}
          {(item.token.root === 'var' || item.token.root === 'param') && onCreateParameter && /^\w+$/.test(item.name) && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => {
              const created = onCreateParameter(item.name)
              if (created) onReplace(item.token.from, item.token.to, `{{param.${created}}}`)
            }}>Make it a parameter</button>
          )}
        </span>
      ))}
    </span>
  )
}

/** The reference picker on its own, for an editor that is not a plain field (the request body). */
export function ReferenceSearch({ label, onPick, onClose }: { label: string; onPick: (expression: string) => void; onClose: () => void }) {
  const id = useId()
  const { scope } = useReferenceFieldContext()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const options = useMemo(() => scope ? matching(scope.items, query) : [], [query, scope])
  const container = useRef<HTMLSpanElement>(null)
  const closeLatest = useRef(onClose)
  closeLatest.current = onClose
  useEffect(() => { setActive(0) }, [query])
  // A click outside the picker and the control that opened it (its parent) closes it.
  useEffect(() => {
    const close = (event: MouseEvent) => { if (!(event.target instanceof Node) || !container.current?.parentElement?.contains(event.target)) closeLatest.current() }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [])
  if (!scope) return null
  return (
    <span ref={container} className="reference-picker standalone" role="dialog" aria-label={`References for ${label}`}>
      <input ref={focusOnMount} type="search" className="reference-picker-search" role="combobox" aria-expanded="true" aria-controls={`${id}-listbox`} aria-activedescendant={options[active] ? `${id}-option-${active}` : undefined} aria-label="Find a reference" placeholder="Find a reference" value={query}
        onChange={event => setQuery(event.target.value)}
        onKeyDown={event => {
          if (event.key === 'ArrowDown') setActive(index => Math.min(options.length - 1, index + 1))
          else if (event.key === 'ArrowUp') setActive(index => Math.max(0, index - 1))
          else if (event.key === 'Enter' && options[active]) onPick(options[active].expression)
          else if (event.key === 'Escape') onClose()
          else return
          event.preventDefault()
        }} />
      <ReferenceOptions id={id} options={options} active={active} onPick={item => onPick(item.expression)} onHover={setActive} />
    </span>
  )
}
