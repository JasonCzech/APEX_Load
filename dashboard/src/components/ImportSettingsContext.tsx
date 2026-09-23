/* eslint-disable react-refresh/only-export-components -- This module exposes the shared editor context and its field controls together. */
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { ApiError, request } from '../hooks/apiClient'
import type { ScriptDefinition } from '../types/scripts'
import type { SecretBindingRequirement } from '../types/scriptImports'
import type { ImportFinding } from './importReviewIssues'
import { thinkTimeConfigForType } from './thinkTimeConfig'
import './ImportSettingsTables.css'

export type SettingsResult = { script: ScriptDefinition; import_draft_expires_at?: string; validation_issues: string[]; findings: ImportFinding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; unresolved_bindings?: number }; secret_binding_requirements: SecretBindingRequirement[] }
export type SettingChange = { path: string; parameter?: string; value: unknown }
type Editor = { changes: Record<string, SettingChange>; errors: Record<string, string> }
export type ImportDataView = 'parameters' | 'sources' | 'allocation'
type SettingsContextValue = {
 viewGeneration: number; dataView: ImportDataView; setDataView: (view: ImportDataView) => void
 script: ScriptDefinition; disabled: boolean; busy: boolean; editors: Record<string, Editor>
 begin: (category: string) => void; cancel: (category: string) => void
 change: (category: string, field: SettingChange) => void; apply: (category: string) => Promise<void>
}
const Context = createContext<SettingsContextValue | null>(null)
export const settingKey = (change: Pick<SettingChange, 'path' | 'parameter'>) => change.parameter ? `${change.parameter}.${change.path}` : change.path
export const useImportSettings = () => useContext(Context)

export function ImportSettingsProvider({ script, disabled, onApplied, onBusyChange, children }: {
 script: ScriptDefinition; disabled: boolean; onApplied?: (result: SettingsResult) => void; onBusyChange: (busy: boolean) => void; children: ReactNode
}) {
 const [editors, setEditors] = useState<Record<string, Editor>>({})
 const [viewGeneration, setViewGeneration] = useState(0)
 const [dataView, setDataView] = useState<ImportDataView>('parameters')
 const [busy, setBusy] = useState(false)
 const [announcement, setAnnouncement] = useState('')
 const token = script.metadata?.import?.preserved?.server_import_attestation_v1
 const scope = token ? `${script.project_id || ''}:${String(token)}` : script
 const current = useRef(scope); current.current = scope
 const mounted = useRef(true)
 const requestInFlight = useRef<object | null>(null)
 const appliedScript = useRef<ScriptDefinition | null>(null)
 const previousScope = useRef(scope)
 useEffect(() => {
  if (previousScope.current === scope) return
  previousScope.current = scope
  const applied = appliedScript.current
  // Importers may attach fresh preflight metadata to our result. Its actual
  // settings and action objects still identify this provider's own rotation.
  const ownRotation = applied && applied.project_id === script.project_id && applied.config === script.config && applied.parameters === script.parameters && applied.actions === script.actions
  appliedScript.current = null
  requestInFlight.current = null
  setBusy(false)
  if (!ownRotation) { setEditors({}); setAnnouncement(''); setDataView('parameters'); setViewGeneration(generation => generation + 1) }
 }, [scope, script])
 const notify = useRef(onBusyChange); notify.current = onBusyChange
 const pending = Object.keys(editors).length > 0
 useEffect(() => { notify.current(pending || busy) }, [pending, busy])
 useEffect(() => { mounted.current = true; return () => { mounted.current = false; requestInFlight.current = null; notify.current(false) } }, [])
 useEffect(() => {
  const warn = (event: BeforeUnloadEvent) => { if (pending) { event.preventDefault(); event.returnValue = '' } }
  window.addEventListener('beforeunload', warn); return () => window.removeEventListener('beforeunload', warn)
 }, [pending])
 const cancel = (category: string) => setEditors(previous => { const next = { ...previous }; delete next[category]; return next })
 async function apply(category: string) {
  const editor = editors[category]
  if (requestInFlight.current || busy || disabled || !editor || !onApplied) return
  const changes = Object.values(editor.changes)
  if (!changes.length) { cancel(category); return }
  const requestScope = current.current
  const operation = {}
  requestInFlight.current = operation
  const isCurrent = () => mounted.current && current.current === requestScope && requestInFlight.current === operation
  setBusy(true)
  try {
   const result = await request<SettingsResult>('/scripts/import/settings', { method: 'POST', body: JSON.stringify({ script, category, changes }) })
   if (!isCurrent()) return
   cancel(category)
   appliedScript.current = result.script
   onApplied(result)
   setAnnouncement('Settings applied to the draft. Review checks refreshed; replay must be run again.')
  } catch (error) {
   if (!isCurrent()) return
   const payload = error instanceof ApiError ? error.payload as { field_errors?: Record<string, string> } : undefined
   setEditors(previous => ({ ...previous, [category]: { ...editor, errors: payload?.field_errors || { category: error instanceof Error ? error.message : 'Could not apply settings' } } }))
  } finally { if (requestInFlight.current === operation) { requestInFlight.current = null; if (mounted.current) setBusy(false) } }
 }
 const value: SettingsContextValue = { viewGeneration, dataView, setDataView, script, editors, busy, disabled: disabled || !onApplied, apply, cancel,
  begin: category => setEditors(previous => ({ ...previous, [category]: previous[category] || { changes: {}, errors: {} } })),
  change: (category, field) => setEditors(previous => {
   const changes = { ...previous[category]?.changes, [settingKey(field)]: field }
   const errors = { ...previous[category]?.errors }
   delete errors[settingKey(field)]
   delete errors.category
   if (field.path === 'config.think_time.type' && typeof field.value === 'string') {
    const current = { ...script.config.think_time }
    for (const change of Object.values(previous[category]?.changes || {})) if (change.path.startsWith('config.think_time.')) Object.assign(current, { [change.path.slice('config.think_time.'.length)]: change.value ?? undefined })
    const next = thinkTimeConfigForType(current, field.value)
    for (const key of ['mean_ms', 'std_ms', 'min_ms', 'max_ms', 'min_percent', 'max_percent'] as const) {
     if (next[key] !== current[key]) {
      const path = `config.think_time.${key}`
      changes[path] = { path, value: next[key] ?? null }
      delete errors[path]
     }
    }
   }
   return { ...previous, [category]: { changes, errors } }
  }) }
 return <Context.Provider value={value}>
  {pending && <div className="import-settings-pending" role="status"><span>Finish editing {Object.keys(editors).map(c => c.replace('parameters:', '')).join(', ')}. Apply or cancel in the table before saving or changing the import.</span><button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setEditors({})}>Discard settings edits</button></div>}
  {announcement && <p role="status">{announcement}</p>}{children}
 </Context.Provider>
}

export function CategoryActions({ category }: { category: string }) {
 const context = useImportSettings()
 if (!context) return null
 const editor = context.editors[category]
 return <span className="import-category-actions">{editor ? <>
  <button type="button" className="btn btn-primary btn-sm" disabled={context.disabled || context.busy} onClick={() => void context.apply(category)}>{context.busy ? 'Applying…' : `Apply ${category.replace('parameters:', '')}`}</button>
  <button type="button" className="btn btn-secondary btn-sm" disabled={context.busy} onClick={() => context.cancel(category)}>Cancel</button>
 </> : <button type="button" className="btn btn-secondary btn-sm" disabled={context.disabled || context.busy} onClick={() => context.begin(category)}>Edit {category.replace('parameters:', '')}</button>}
 {editor?.errors.category && <span role="alert">{editor.errors.category}</span>}
 </span>
}

export function SettingValue({ category, path, parameter, value, label, type = 'text', options, readOnly = false }: {
 category: string; path: string; parameter?: string; value: unknown; label: string; type?: string; options?: string[]; readOnly?: boolean
}) {
 const context = useImportSettings()
 const key = settingKey({ path, parameter })
 const editor = context?.editors[category]
 const currentValue = editor?.changes[key] ? editor.changes[key].value : value
 const protectedValue = /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(JSON.stringify(value) || '')
 if (!editor || readOnly || protectedValue) return <span className="import-setting-value">{protectedValue ? 'Protected — manage in Credentials' : formatSettingValue(currentValue)}</span>
 const change = (next: unknown) => context?.change(category, { path, parameter, value: next })
 const props = { 'aria-label': label, disabled: context?.disabled || context?.busy, 'aria-invalid': !!editor.errors[key] }
 return <label className="import-setting-input"><span className="sr-only">{label}</span>
  {type === 'boolean' ? <select {...props} value={currentValue == null ? '' : String(currentValue)} onChange={e => change(e.target.value === '' ? null : e.target.value === 'true')}><option value="">Default</option><option value="true">Enabled</option><option value="false">Disabled</option></select>
   : type === 'select' ? <select {...props} value={String(currentValue ?? '')} onChange={e => change(e.target.value || null)}><option value="">Default</option>{currentValue && !options?.includes(String(currentValue)) ? <option value={String(currentValue)}>{String(currentValue)} (imported)</option> : null}{options?.map(option => <option key={option} value={option}>{option.replace(/_/g, ' ')}</option>)}</select>
   : type === 'lines' ? <textarea {...props} rows={3} value={Array.isArray(currentValue) ? currentValue.join('\n') : ''} onChange={e => change(e.target.value.split('\n'))} />
   : <input {...props} type={type === 'number' || type === 'signed' ? 'number' : 'text'} min={type === 'number' ? 0 : undefined} step="any" value={String(currentValue ?? '')} placeholder="Default" onChange={e => change(e.target.value === '' ? null : type === 'number' || type === 'signed' ? Number(e.target.value) : e.target.value)} />}
  {editor.errors[key] && <span role="alert">{editor.errors[key]}</span>}
 </label>
}
export function formatSettingValue(value: unknown): string {
 if (value == null || value === '') return 'Default / not set'
 if (typeof value === 'boolean') return value ? 'Enabled' : 'Disabled'
 return Array.isArray(value) ? value.join(', ') || '(empty list)' : typeof value === 'object' ? JSON.stringify(value) : String(value)
}

export function ImportDataExtras({ children }: { children: ReactNode }) {
 const context = useImportSettings()
 return <div hidden={!!context && context.dataView !== 'parameters'}>{children}</div>
}
