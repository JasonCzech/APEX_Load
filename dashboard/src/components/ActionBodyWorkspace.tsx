import { ActionParameterMenu } from './ScriptEditorActionEditor'
import type { ActionEditorSelectionState } from './scriptEditorJsonModel'
import { bodyLanguage, bodyTemplateReferences, extractedBodyReferences } from './actionBodyModel'
import { useEffect, useMemo, useRef, useState } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { autocompletion } from '@codemirror/autocomplete'
import { EditorView } from '@codemirror/view'
import { openSearchPanel } from '@codemirror/search'
import type { ScriptActionDefinition, ScriptDefinition, ScriptParameterDefinition } from '../types/scripts'
import type { ActionEditorParameterDraft } from './scriptEditorState'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { sanitizeVarName } from './scriptEditorUtils'
import { BODY_ENCODING_OPTIONS, knownBodyEncoding } from './bodyEncodings'
import { MultipartPartsEditor } from './MultipartPartsEditor'

export function ActionBodyWorkspace({ action, index, path, parameters, script, onUpdate, onParameterize, onInspectReference }: {
  action: ScriptActionDefinition; index: number; path: string; parameters: ScriptParameterDefinition[]; script?: ScriptDefinition
  onUpdate: (field: keyof ScriptActionDefinition, value: unknown) => void
  onParameterize: (draft: ActionEditorParameterDraft) => void
  onInspectReference?: (reference: string) => void
}) {
  const viewRef = useRef<EditorView | null>(null)
  const [syntax, setSyntax] = useState(bodyLanguage(action))
  const [issue, setIssue] = useState('')
  const [selection, setSelection] = useState({ from: 0, to: 0 })
  const [menu, setMenu] = useState<ActionEditorSelectionState | null>(null)
  const [preview, setPreview] = useState(false)
  useEffect(() => {
    if (!menu) return
    const dismiss = (event: MouseEvent) => { if (!(event.target instanceof Element) || !event.target.closest('.action-parameter-menu')) setMenu(null) }
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') setMenu(null) }
    document.addEventListener('mousedown', dismiss)
    document.addEventListener('keydown', escape)
    return () => { document.removeEventListener('mousedown', dismiss); document.removeEventListener('keydown', escape) }
  }, [menu])
  const references = bodyTemplateReferences(action.body || '')
  const completions = useMemo(() => Array.from(new Set([
    ...parameters.map(p => `param.${p.name}`),
    ...(script?.data_sources || []).flatMap(source => (source.columns || []).map(column => `data.${source.name}.${column}`)),
    ...extractedBodyReferences(script ? [...(script.init || []), ...script.actions, ...(script.teardown || [])] : [action]),
    ...bodyTemplateReferences(JSON.stringify(script || action)),
    ...['fn.uuid()', 'fn.timestamp.iso8601()', 'fake.email()'],
  ])).map(label => ({ label, type: 'variable', apply: `${label}}}` })), [action, parameters, script])
  const extensions = useMemo(() => [apexCodeMirrorTheme, EditorView.contentAttributes.of({ 'aria-label': 'Request body' }), EditorView.lineWrapping, ...(syntax === 'json' ? [json()] : syntax === 'xml' ? [xml()] : []), autocompletion({ override: [context => {
    const match = context.matchBefore(/{{[\w.()]*/)
    return match ? { from: match.from + 2, options: completions } : null
  }] })], [completions, syntax])
  const validate = () => {
    try {
      if (syntax === 'json') JSON.parse(action.body || '')
      if (syntax === 'xml') {
        const parsed = new DOMParser().parseFromString(action.body || '', 'application/xml')
        if (parsed.querySelector('parsererror')) throw new Error(parsed.querySelector('parsererror')!.textContent || 'Invalid XML')
      }
      setIssue(syntax === 'text' ? 'Plain text has no syntax constraints.' : 'Syntax is valid. Runtime template values are checked during execution.')
      return true
    } catch (error) { setIssue(error instanceof Error ? error.message : 'Invalid body syntax'); return false }
  }
  const format = () => {
    if (!validate()) return
    if (syntax === 'json') onUpdate('body', JSON.stringify(JSON.parse(action.body || ''), null, 2))
    // XML whitespace can affect payload semantics. Serialize explicitly without inventing indentation.
    if (syntax === 'xml') onUpdate('body', new XMLSerializer().serializeToString(new DOMParser().parseFromString(action.body || '', 'application/xml')))
  }
  const parameterize = (requestedExistingName?: string | null) => {
    const range = menu ? { from: menu.start, to: menu.end } : viewRef.current?.state.selection.main || selection
    const selectedText = (action.body || '').slice(range.from, range.to)
    if (!selectedText.trim()) return
    const existingName = requestedExistingName || /^{{\s*param\.([\w]+)\s*}}$/.exec(selectedText)?.[1] || null
    setMenu(null)
    onParameterize({ source: 'action', actionIndex: index, start: range.from, end: range.to, selectedText, existingName, requestedName: existingName || sanitizeVarName(selectedText), seedValue: selectedText })
  }
  const fileSources = (script?.data_sources || []).filter(source => source.type === 'file').map(source => source.name)
  if (action.body_multipart) {
    // A multipart body needs at least one field; removing the last one goes
    // back to a plain body.
    return <section className="center-document author-body-document" aria-label="Request body editor"><h3>{action.name} · Request body (multipart form)</h3><p>{path} · Changes are included in Save script.</p>
      <MultipartPartsEditor parts={action.body_multipart.parts || []} fileSources={fileSources} onChange={parts => onUpdate('body_multipart', parts.length > 0 ? { ...action.body_multipart, parts } : undefined)} />
      <p><button className="btn btn-sm btn-secondary" type="button" onClick={() => onUpdate('body_multipart', undefined)}>Use a plain body instead</button></p>
    </section>
  }
  return <section className="center-document author-body-document" aria-label="Request body editor"><h3>{action.name} · Request body</h3><p>{path} · Changes are included in Save script.</p>
    {!action.body && !action.body_file && <p><button className="btn btn-sm btn-secondary" type="button" onClick={() => { onUpdate('body_encoding', undefined); onUpdate('body_multipart', { parts: [{ name: 'field_1', value: '' }] }) }}>Use multipart form fields</button></p>}
    <div className="author-body-toolbar">
      <label>Wire encoding<select aria-label="Wire encoding" value={knownBodyEncoding(action.body_encoding)?.value ?? action.body_encoding ?? ''} onChange={event => { onUpdate('body_encoding', event.target.value || undefined); if (event.target.value) setSyntax('text') }}>{BODY_ENCODING_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.value ? option.label : 'Text (as authored)'}</option>)}{action.body_encoding && !knownBodyEncoding(action.body_encoding) && <option value={action.body_encoding}>Preserved: {action.body_encoding}</option>}</select></label>
      <label>Editor language<select aria-label="Editor language" value={syntax} onChange={e => { setSyntax(e.target.value as typeof syntax); setIssue('') }}><option value="text">Plain text</option><option value="json">JSON</option><option value="xml">XML</option></select></label>
      <button className="btn btn-sm btn-secondary" type="button" onClick={() => viewRef.current && openSearchPanel(viewRef.current)}>Find</button>
      <button className="btn btn-sm btn-secondary" type="button" onClick={validate}>Check syntax</button>
      <button className="btn btn-sm btn-secondary" type="button" onClick={format} disabled={syntax === 'text'}>Format body</button>
      <button className="btn btn-sm btn-secondary" type="button" onClick={() => parameterize()} disabled={selection.from === selection.to}>Parameterize selection</button>
    </div>
    <p>Editor language changes highlighting only. Formatting is explicit and can change whitespace. Content-Type remains in request headers.</p>
    <div onContextMenu={event => {
      const range = viewRef.current?.state.selection.main
      const selectedText = range ? (action.body || '').slice(range.from, range.to) : ''
      if (!range || !selectedText.trim()) return
      event.preventDefault()
      setMenu({ start: range.from, end: range.to, selectedText, x: event.clientX, y: event.clientY, existingName: /^{{\s*param\.([\w]+)\s*}}$/.exec(selectedText)?.[1] || null })
    }}><CodeMirror theme="none" aria-label="Request body" value={action.body || ''} extensions={extensions} onCreateEditor={view => { viewRef.current = view }} onUpdate={update => { if (update.selectionSet) setSelection({ from: update.state.selection.main.from, to: update.state.selection.main.to }) }} onChange={value => { onUpdate('body', value); setIssue('') }} basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: true }} /></div>
    <ActionParameterMenu selectionState={menu} onOpenParameterEditor={parameterize} />
    {issue && <p role="status">{issue}</p>}
    <details><summary>Template references ({references.length})</summary>{references.map(ref => <button className="btn btn-sm btn-secondary" type="button" key={ref} onClick={() => onInspectReference?.(ref)} disabled={!onInspectReference}>{`{{${ref}}}`}</button>)}<p>Type {'{{'} to complete references found in this script, parameters, and built-in functions. Environment and extracted variables are resolved at runtime.</p></details>
    <button className="btn btn-sm btn-secondary" type="button" aria-expanded={preview} onClick={() => setPreview(value => !value)}>Masked template preview</button>
    {preview && <div role="region" aria-label="Masked template preview"><p>Reference values are masked. This preview does not execute the request or predict iteration values.</p><pre>{(action.body || '').replace(/{{\s*([^}]+?)\s*}}/g, (_, ref: string) => `[${ref.startsWith('secret.') ? 'secret masked' : `${ref}: runtime value`}]`)}</pre></div>}
  </section>
}
