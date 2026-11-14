/* eslint-disable react-refresh/only-export-components -- This module exposes the shared editor context and its field controls together. */
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { ApiError, request } from '../hooks/apiClient'
import type { ScriptDefinition, ThinkTimeConfig } from '../types/scripts'
import type { EnterpriseArchiveFidelityReport, SecretBindingRequirement } from '../types/scriptImports'
import type { ImportFinding } from './importReviewIssues'
import { CANONICAL_RUNTIME_GROUP_IDS, readSetting, resolveCanonicalRuntimeGroup, type CanonicalRuntimeGroup } from './importRuntimeSettingsModel'
import { importRefusesRestoring } from './importSettingCredentialMaterial'
import { canonicalThinkTimeType, thinkTimeConfigForType } from './thinkTimeConfig'
import './ImportSettingsTables.css'

export type SettingsResult = { fidelity_report?: EnterpriseArchiveFidelityReport; script: ScriptDefinition; import_draft_expires_at?: string; validation_issues: string[]; findings: ImportFinding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; unresolved_bindings?: number }; secret_binding_requirements: SecretBindingRequirement[] }
export type SettingChange = { path: string; parameter?: string; value: unknown }
/** `derived` names the think-time companions a Mode change queued, which the user did not type. */
type Editor = { changes: Record<string, SettingChange>; errors: Record<string, string>; derived?: Record<string, true> }
export type ImportDataView = 'parameters' | 'sources' | 'allocation'
type SettingsContextValue = {
 viewGeneration: number; dataView: ImportDataView; setDataView: (view: ImportDataView) => void
 /** The draft as this review opened it; applied edits are measured against it. */
 original: ScriptDefinition
 script: ScriptDefinition; disabled: boolean; busy: boolean; editors: Record<string, Editor>
 begin: (category: string) => void; cancel: (category: string) => void
 change: (category: string, field: SettingChange) => void; apply: (category: string) => Promise<void>
}
const Context = createContext<SettingsContextValue | null>(null)
export const settingKey = (change: Pick<SettingChange, 'path' | 'parameter'>) => change.parameter ? `${change.parameter}.${change.path}` : change.path
export const useImportSettings = () => useContext(Context)

const sameValue = (left: unknown, right: unknown) => JSON.stringify(left ?? null) === JSON.stringify(right ?? null)
const THINK_TIME_TYPE = 'config.think_time.type'
const THINK_TIME_COMPANIONS = ['mean_ms', 'std_ms', 'min_ms', 'max_ms', 'min_percent', 'max_percent'] as const
const isPlainObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)

/**
 * Carries the review-open baseline across a rotation the settings editor did
 * not make (transport apply, guided repair): a setting the rotation left alone
 * keeps its review-open value, a setting it changed takes the new value.
 */
export function rebaseOriginalSetting(original: unknown, previous: unknown, next: unknown): unknown {
 if (isPlainObject(next) || isPlainObject(previous)) {
  const [base, before, after] = [original, previous, next].map(value => isPlainObject(value) ? value : {})
  const result: Record<string, unknown> = {}
  for (const key of new Set([...Object.keys(base), ...Object.keys(before), ...Object.keys(after)])) {
   const value = rebaseOriginalSetting(base[key], before[key], after[key])
   if (value !== undefined) result[key] = value
  }
  return Object.keys(result).length || isPlainObject(next) ? result : undefined
 }
 return sameValue(previous, next) ? original : next
}

// The same import keeps its name, protocol, project and source across in-review rotations.
const importIdentity = (script: ScriptDefinition) => JSON.stringify([script.project_id || '', script.name, script.protocol || '', script.metadata?.import?.source || ''])

/** True when a pending edit or an applied edit changed this path from the draft the review opened with. */
export function isSettingModified(context: SettingsContextValue | null, path: string): boolean {
 if (!context) return false
 if (Object.entries(context.editors).some(([category, editor]) => !category.startsWith('parameters:') && editor.changes[path])) return true
 return !sameValue(readSetting(context.script, path), readSetting(context.original, path))
}

// A validation issue that names another setting ("config.results.message_detail.mode
// must be ...") belongs on that setting's control as well as the category.
const ISSUE_PATH = /^(config(?:\.[A-Za-z0-9_]+)+)[\s:]/
function attachIssueToNamedSetting(errors: Record<string, string>): Record<string, string> {
 const issue = errors.category
 const path = issue?.match(ISSUE_PATH)?.[1]
 return path && !errors[path] ? { ...errors, [path]: issue } : errors
}

function resolveActiveEditorCategory(
 editors: Record<string, Editor> | undefined,
 category: string,
 path: string,
): string {
 if (!editors) return category
 if (editors[category]) return category
 const canonicalGroup = resolveCanonicalRuntimeGroup(category, path)
 if (editors[canonicalGroup]) return canonicalGroup
 if ((path === 'config.http.proxy_mode' || path.startsWith('config.http.proxy.')) && editors.Proxy) return 'Proxy'
 if ((path === 'config.http.proxy_mode' || path.startsWith('config.http.proxy.')) && editors.HTTP) return 'HTTP'
 return category
}

export function ImportSettingsProvider({ script, identity, disabled, onApplied, onBusyChange, children }: {
 script: ScriptDefinition; disabled: boolean; onApplied?: (result: SettingsResult) => void; onBusyChange: (busy: boolean) => void; children: ReactNode
 /**
  * Names the script under review (archive item and script index, say). A change
  * starts a new baseline even when two scripts share a name; without it the
  * provider falls back to the script's name, project, protocol and source.
  */
 identity?: string
}) {
 const [editors, setEditors] = useState<Record<string, Editor>>({})
 const [viewGeneration, setViewGeneration] = useState(0)
 const [dataView, setDataView] = useState<ImportDataView>('parameters')
 const [original, setOriginal] = useState(script)
 const [busy, setBusy] = useState(false)
 const [announcement, setAnnouncement] = useState('')
 const token = script.metadata?.import?.preserved?.server_import_attestation_v1
 const scope = token ? `${script.project_id || ''}:${String(token)}` : script
 const current = useRef(scope); current.current = scope
 const mounted = useRef(true)
 const requestInFlight = useRef<object | null>(null)
 const appliedScript = useRef<ScriptDefinition | null>(null)
 const previousScope = useRef(scope)
 const previousIdentity = useRef(identity)
 // The draft as last rendered (this provider's own applies included), to tell which settings a rotation changed.
 const previousScript = useRef(script)
 useEffect(() => {
  const previous = previousScript.current
  previousScript.current = script
  const identityChanged = previousIdentity.current !== identity
  previousIdentity.current = identity
  if (previousScope.current === scope && !identityChanged) return
  previousScope.current = scope
  const applied = appliedScript.current
  // Importers may attach fresh preflight metadata to our result. Its actual
  // settings and action objects still identify this provider's own rotation.
  const ownRotation = !identityChanged && applied && applied.project_id === script.project_id && applied.config === script.config && applied.parameters === script.parameters && applied.actions === script.actions
  appliedScript.current = null
  requestInFlight.current = null
  setBusy(false)
  if (!ownRotation) {
   setEditors({}); setAnnouncement(''); setDataView('parameters'); setViewGeneration(generation => generation + 1)
   // A different import starts a new baseline; a rotation of this import keeps
   // earlier applied edits Modified and resettable to the imported value.
   setOriginal(baseline => identityChanged || importIdentity(previous) !== importIdentity(script) ? script : { ...script, config: rebaseOriginalSetting(baseline.config, previous.config, script.config) as ScriptDefinition['config'] })
  }
 }, [identity, scope, script])
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
  const apiCategory =
   category !== 'Think time' && category in CANONICAL_RUNTIME_GROUP_IDS
    ? CANONICAL_RUNTIME_GROUP_IDS[category as CanonicalRuntimeGroup]
    : category
  try {
   const result = await request<SettingsResult>('/scripts/import/settings', { method: 'POST', body: JSON.stringify({ script, category: apiCategory, changes }) })
   if (!isCurrent()) return
   cancel(category)
   appliedScript.current = result.script
   onApplied(result)
   setAnnouncement('Settings applied to the draft. Review checks refreshed; replay must be run again.')
  } catch (error) {
   if (!isCurrent()) return
   const payload = error instanceof ApiError ? error.payload as { field_errors?: Record<string, string> } : undefined
   const errors = payload?.field_errors || { category: error instanceof Error ? error.message : 'Could not apply settings' }
   setEditors(previous => ({ ...previous, [category]: { ...editor, errors: attachIssueToNamedSetting(errors) } }))
  } finally { if (requestInFlight.current === operation) { requestInFlight.current = null; if (mounted.current) setBusy(false) } }
 }
 const value: SettingsContextValue = { viewGeneration, dataView, setDataView, original, script, editors, busy, disabled: disabled || !onApplied, apply, cancel,
  begin: category => setEditors(previous => ({ ...previous, [category]: previous[category] || { changes: {}, errors: {} } })),
  change: (category, field) => setEditors(previous => {
   const key = settingKey(field)
   const changes = { ...previous[category]?.changes }
   if (field.value === undefined) delete changes[key]
   else changes[key] = field
   const errors = { ...previous[category]?.errors }
   delete errors[key]
   delete errors.category
   const derived = { ...previous[category]?.derived }
   // A value the user types is theirs, not a Mode companion.
   if (!field.parameter) delete derived[key]
   if (!field.parameter && field.path === THINK_TIME_TYPE) {
    // A Mode change replaces the companions the previous Mode change queued; a
    // reset (to imported or default) only removes them, leaving the draft's values.
    for (const path of Object.keys(derived)) { delete changes[path]; delete errors[path]; delete derived[path] }
    if (typeof field.value === 'string') {
     const typed = (base: Partial<ThinkTimeConfig> | undefined) => {
      const merged: Partial<ThinkTimeConfig> = { ...base }
      for (const change of Object.values(changes)) if (!change.parameter && change.path.startsWith('config.think_time.') && change.path !== THINK_TIME_TYPE) Object.assign(merged, { [change.path.slice('config.think_time.'.length)]: change.value ?? undefined })
      return merged
     }
     const current = typed(script.config.think_time)
     // Back to the imported Mode restores the imported companions (an applied
     // Mode change may have dropped them), not fresh defaults for that Mode.
     const importedType = readSetting(original, THINK_TIME_TYPE)
     const restoresImported = typeof importedType === 'string' && canonicalThinkTimeType(importedType) === canonicalThinkTimeType(field.value)
     const next = thinkTimeConfigForType(restoresImported ? typed(original.config?.think_time) : current, field.value)
     for (const companion of THINK_TIME_COMPANIONS) {
      if (next[companion] !== current[companion]) {
       const path = `config.think_time.${companion}`
       changes[path] = { path, value: next[companion] ?? null }
       derived[path] = true
       delete errors[path]
      }
     }
    }
   }
   return { ...previous, [category]: { changes, errors, derived } }
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

export function SettingValue({ category, path, parameter, value, label, type = 'text', options, readOnly = false, credentialReference = false, fallbackValue }: {
 category: string; path: string; parameter?: string; value: unknown; label: string; type?: string; options?: string[]; readOnly?: boolean
 /** A new value must be a Credentials reference; the imported value stays readable and editable. */
 credentialReference?: boolean
 /** Shown while the draft leaves the setting unset; never treated as the draft's value. */
 fallbackValue?: unknown
}) {
 const context = useImportSettings()
 const key = settingKey({ path, parameter })
 const activeCategory = resolveActiveEditorCategory(context?.editors, category, path)
 const editor = context?.editors[activeCategory]
 const pendingValue = editor?.changes[key] ? editor.changes[key].value : value
 const currentValue = editor?.changes[key] ? pendingValue : value ?? fallbackValue
 if (!editor || readOnly) return <span className="import-setting-value">{formatSettingValue(currentValue)}</span>
 const change = (next: unknown) => context?.change(activeCategory, { path, parameter, value: next })
 const nonEmptyOptions = options?.filter(option => option !== '')
 const importedOption = value != null && value !== '' && !nonEmptyOptions?.includes(String(value)) ? String(value) : null
 const currentOutOfCatalog = currentValue != null && currentValue !== '' && !nonEmptyOptions?.includes(String(currentValue)) ? String(currentValue) : null
 const preservedOption = importedOption ?? currentOutOfCatalog
 const props = { 'aria-label': label, disabled: context?.disabled || context?.busy, 'aria-invalid': !!editor.errors[key] }
 return <label className="import-setting-input"><span className="sr-only">{label}</span>
  {type === 'boolean' ? <select {...props} value={currentValue == null ? '' : String(currentValue)} onChange={e => change(e.target.value === '' ? null : e.target.value === 'true')}><option value="">Default</option><option value="true">Enabled</option><option value="false">Disabled</option></select>
   : type === 'select' ? <select {...props} value={String(currentValue ?? '')} onChange={e => change(importedOption !== null && e.target.value === importedOption ? undefined : (e.target.value || null))}><option value="">Default</option>{preservedOption ? <option value={preservedOption}>{preservedOption} (imported)</option> : null}{nonEmptyOptions?.map(option => <option key={option} value={option}>{option.replace(/_/g, ' ')}</option>)}</select>
   : type === 'lines' ? <textarea {...props} rows={3} value={Array.isArray(currentValue) ? currentValue.join('\n') : ''} onChange={e => change(e.target.value.split('\n'))} />
   : <input {...props} type={type === 'number' || type === 'signed' ? 'number' : 'text'} min={type === 'number' ? 0 : undefined} step="any" value={String(currentValue ?? '')} placeholder="Default" onChange={e => change(e.target.value === '' ? null : type === 'number' || type === 'signed' ? Number(e.target.value) : e.target.value)} />}
  {credentialReference && <small className="import-settings-hint">{credentialReferenceHint(path)}</small>}
  {!parameter && <SettingResetActions path={path} label={label} draftValue={value} currentValue={pendingValue} onChange={change} disabled={props.disabled} acceptedOptions={type === 'select' ? nonEmptyOptions : undefined} credentialReference={credentialReference} />}
  {editor.errors[key] && <span role="alert">{editor.errors[key]}</span>}
 </label>
}

/**
 * The hint under a setting whose new value must be a Credentials reference.
 * The imported value is script-sourced: it stays as it is and resubmits
 * unchanged; only a newly typed literal is refused.
 */
export function credentialReferenceHint(path: string): string {
 if (path === 'config.http.proxy.username') return 'Enter a Credentials reference such as {{secret.PROXY_USER}}. The import refuses a typed username.'
 return 'Enter a Credentials reference such as {{secret.NAME}} to change this value. The import refuses a newly typed value; the imported value can stay as it is.'
}

// Settings the draft must hold a value for: clearing them is a new validation
// issue the import refuses (config.base_url is empty), so they have no default.
const SETTINGS_WITHOUT_DEFAULT = new Set(['config.base_url'])

function SettingResetActions({ path, label, draftValue, currentValue, onChange, disabled, acceptedOptions, credentialReference = false }: {
 path: string; label: string; draftValue: unknown; currentValue: unknown; onChange: (next: unknown) => void; disabled?: boolean
 /** A select's accepted values; an imported value outside them cannot be sent back. */
 acceptedOptions?: string[]
 credentialReference?: boolean
}) {
 const context = useImportSettings()
 if (!context) return null
 const importedValue = readSetting(context.original, path)
 // Back to the draft's own value drops the pending change instead of resending it.
 const restore = (target: unknown) => onChange(sameValue(draftValue, target) ? undefined : target ?? null)
 // The import refuses a select value outside its options, so an imported value
 // the draft no longer holds (legacy TLS 1.0, say) cannot be restored.
 const importedEmpty = importedValue == null || importedValue === ''
 const importedRejected = !!acceptedOptions && !importedEmpty && !acceptedOptions.includes(String(importedValue)) && !sameValue(draftValue, importedValue)
 // Once an apply replaced an imported typed credential (a proxy username, URL
 // userinfo, a sig= or other credential query parameter), sending it back
 // reads as a new typed credential unless the draft still carries that
 // material (on a masked URL setting, every part the masking hides: a short
 // token=ab or svc:@ userinfo counts), and the import refuses it. Dropping a
 // pending change still restores it.
 const importedCredentialLost = !sameValue(draftValue, importedValue) && importRefusesRestoring(importedValue, draftValue, credentialReference, path)
 const differsFromImported = !sameValue(currentValue, importedValue)
 // An unset imported value is the default: Reset to default covers it.
 const canResetImported = differsFromImported && !importedRejected && !importedCredentialLost && !importedEmpty
 const canResetDefault = currentValue != null && currentValue !== '' && !SETTINGS_WITHOUT_DEFAULT.has(path)
 if (!canResetImported && !canResetDefault && !(differsFromImported && (importedRejected || importedCredentialLost))) return null
 return <span className="import-setting-reset-actions">
  {differsFromImported && importedRejected && <small className="import-settings-hint">The imported value {String(importedValue)} is not a supported option, so it cannot be restored.{path.startsWith('config.http.tls.') ? ' Legacy TLS versions stay migrated.' : ''}</small>}
  {differsFromImported && importedCredentialLost && <small className="import-settings-hint">The imported value cannot be restored here because the import refuses a typed credential. Use a Credentials reference instead.</small>}
  {canResetImported && <button type="button" className="btn btn-secondary btn-sm" disabled={disabled} aria-label={`Reset ${label} to imported value`} onClick={() => restore(importedValue)}>Reset to imported</button>}
  {canResetDefault && <button type="button" className="btn btn-secondary btn-sm" disabled={disabled} aria-label={`Reset ${label} to default`} onClick={() => restore(null)}>Reset to default</button>}
 </span>
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
