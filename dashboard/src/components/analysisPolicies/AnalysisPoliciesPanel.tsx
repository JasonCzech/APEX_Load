import { useCallback, useEffect, useState, type FormEvent } from 'react'

import { ApiError } from '../../hooks/apiClient'
import { analysisPoliciesApi, type AnalysisPolicyRecord, type EditableAnalysisPolicyKind } from '../../hooks/api/analysisPoliciesApi'
import { formatDateTime } from '../../utils/dateTime'
import { getErrorMessage } from '../../utils/errorUtils'
import Notice from '../ux/Notice'
import { confirmAction } from '../ux/confirmAction'
import {
  BROWSER_POLICY_METRICS, browserDefinitionFromDraft, browserDraftFromDefinition, defaultBrowserPolicyDraft, defaultJourneyPolicyDraft,
  emptyJourney, journeyDefinitionFromDraft, journeyDraftFromDefinition, type BrowserPolicyDraft, type JourneyPolicyDraft,
} from './policyEditorModel'

const KIND_LABELS: Record<EditableAnalysisPolicyKind, { plural: string; singular: string }> = {
  browser_policy: { plural: 'Browser policies', singular: 'browser policy' },
  journey_policy: { plural: 'Journey policies', singular: 'journey policy' },
}

type Editing =
  | { kind: 'browser_policy'; id: string | null; revision: number; draft: BrowserPolicyDraft }
  | { kind: 'journey_policy'; id: string | null; revision: number; draft: JourneyPolicyDraft }

function isDefault(policy: AnalysisPolicyRecord): boolean {
  return Boolean(policy.definition && typeof policy.definition === 'object' && (policy.definition as { apply_by_default?: boolean }).apply_by_default)
}

// AnalysisPoliciesPanel edits a project's browser and journey policies (BI-4).
// Every save names the revision it was read at, so a concurrent edit is
// refused rather than overwritten. Runs pin the revision they started with,
// and archived findings keep the revision they were evaluated under.
export default function AnalysisPoliciesPanel({ projectId, canEdit }: { projectId: string; canEdit: boolean }) {
  const [kind, setKind] = useState<EditableAnalysisPolicyKind>('browser_policy')
  const [policies, setPolicies] = useState<AnalysisPolicyRecord[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [reload, setReload] = useState(0)
  const [editing, setEditing] = useState<Editing | null>(null)
  const [saveError, setSaveError] = useState('')
  // A failed delete reloads the list, so its reason is kept apart from the
  // list's own load error.
  const [deleteError, setDeleteError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [busy, setBusy] = useState(false)
  const [revisionView, setRevisionView] = useState<{ policy: AnalysisPolicyRecord; revision: string; record: AnalysisPolicyRecord | null; error: string } | null>(null)

  useEffect(() => {
    if (!projectId) return
    const controller = new AbortController()
    setLoadError('')
    analysisPoliciesApi.list(projectId, kind, controller.signal)
      .then(list => { if (!controller.signal.aborted) setPolicies(list) })
      .catch(err => { if (!controller.signal.aborted) { setPolicies(null); setLoadError(getErrorMessage(err, 'Unable to load policies')) } })
    return () => controller.abort()
  }, [projectId, kind, reload])

  const startNew = () => {
    setSaveError(''); setConflict(false); setRevisionView(null)
    setEditing(kind === 'browser_policy'
      ? { kind, id: null, revision: 0, draft: defaultBrowserPolicyDraft() }
      : { kind, id: null, revision: 0, draft: defaultJourneyPolicyDraft() })
  }
  const startEdit = (policy: AnalysisPolicyRecord) => {
    setSaveError(''); setConflict(false); setRevisionView(null)
    setEditing(policy.kind === 'browser_policy'
      ? { kind: 'browser_policy', id: policy.id, revision: policy.revision, draft: browserDraftFromDefinition(policy.name, policy.definition) }
      : { kind: 'journey_policy', id: policy.id, revision: policy.revision, draft: journeyDraftFromDefinition(policy.name, policy.definition) })
  }

  const reloadCurrent = useCallback(async () => {
    if (!editing?.id) return
    setBusy(true)
    try {
      const current = await analysisPoliciesApi.get(projectId, editing.id)
      setConflict(false); setSaveError('')
      startEdit(current)
    } catch (err) {
      setSaveError(getErrorMessage(err, 'Unable to load the current revision'))
    } finally {
      setBusy(false)
    }
  }, [editing, projectId])

  // A 409 is a revision conflict only when the policy's revision moved: the
  // controller also refuses a second default policy or a full project.
  const revisionChanged = async (id: string, revision: number) => {
    try {
      return (await analysisPoliciesApi.get(projectId, id)).revision !== revision
    } catch {
      return false
    }
  }

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!editing) return
    const built = editing.kind === 'browser_policy' ? browserDefinitionFromDraft(editing.draft) : journeyDefinitionFromDraft(editing.draft)
    if (!built.definition) { setSaveError(built.problem || 'The policy cannot be saved.'); return }
    setBusy(true); setSaveError(''); setConflict(false)
    const body = { kind: editing.kind, name: editing.draft.name.trim(), definition: built.definition }
    try {
      if (editing.id) await analysisPoliciesApi.update(projectId, editing.id, editing.revision, body)
      else await analysisPoliciesApi.create(projectId, body)
      setEditing(null)
      setReload(value => value + 1)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && editing.id && await revisionChanged(editing.id, editing.revision)) setConflict(true)
      else setSaveError(getErrorMessage(err, 'Unable to save the policy'))
    } finally {
      setBusy(false)
    }
  }

  const remove = async (policy: AnalysisPolicyRecord) => {
    const accepted = await confirmAction(`Delete the ${KIND_LABELS[policy.kind as EditableAnalysisPolicyKind]?.singular || 'policy'} "${policy.name || policy.id}"? Runs that pinned one of its revisions keep it.`, { title: 'Delete policy', confirmLabel: 'Delete policy', destructive: true })
    if (!accepted) return
    setDeleteError('')
    try {
      await analysisPoliciesApi.remove(projectId, policy.id, policy.revision)
      setReload(value => value + 1)
    } catch (err) {
      const changed = err instanceof ApiError && err.status === 409 && await revisionChanged(policy.id, policy.revision)
      setDeleteError(changed ? 'The policy changed since the list was loaded. Review it and try again.' : getErrorMessage(err, 'Unable to delete the policy'))
      setReload(value => value + 1)
    }
  }

  const viewRevision = async (event: FormEvent) => {
    event.preventDefault()
    if (!revisionView) return
    const revision = Number(revisionView.revision)
    if (!Number.isInteger(revision) || revision < 1) { setRevisionView({ ...revisionView, record: null, error: 'Enter a revision number of 1 or more.' }); return }
    try {
      const record = await analysisPoliciesApi.get(projectId, revisionView.policy.id, revision)
      setRevisionView({ ...revisionView, record, error: '' })
    } catch (err) {
      setRevisionView({ ...revisionView, record: null, error: err instanceof ApiError && err.status === 404 ? `Revision ${revision} was not found.` : getErrorMessage(err, 'Unable to load the revision') })
    }
  }

  return <section className="settings-subsection analysis-policies" aria-label="Browser and journey policies">
    <p>Browser policies score a run's document metrics and set how the root-cause browser baseline compares them; journey policies define the journeys whose attempts are accounted. The default policy of each kind is pinned when a run starts, and findings keep the revision they were evaluated under: editing a policy never rewrites a finished run's results.</p>
    <div className="settings-inline-actions" role="group" aria-label="Policy kind">
      {(Object.keys(KIND_LABELS) as EditableAnalysisPolicyKind[]).map(value => <button key={value} type="button" className={`btn btn-sm ${kind === value ? 'btn-primary' : 'btn-secondary'}`} aria-pressed={kind === value} onClick={() => { setKind(value); setEditing(null); setRevisionView(null); setPolicies(null); setDeleteError('') }}>{KIND_LABELS[value].plural}</button>)}
      {canEdit && !editing && <button type="button" className="btn btn-sm btn-secondary" onClick={startNew}>New {KIND_LABELS[kind].singular}</button>}
    </div>
    {!canEdit && <p className="run-report-muted">Your role in this project can view policies but not change them.</p>}
    {deleteError && <Notice tone="error" title="The policy was not deleted">{deleteError}</Notice>}
    {loadError && <Notice tone="error" title="Policies could not be loaded" action={<button type="button" className="btn btn-secondary btn-sm" onClick={() => setReload(value => value + 1)}>Retry</button>}>{loadError}</Notice>}
    {policies === null && !loadError && <p role="status">Loading policies…</p>}
    {policies && policies.length === 0 && <p>This project has no {KIND_LABELS[kind].plural.toLowerCase()} yet.</p>}
    {policies && policies.length > 0 && <table className="tx-table" aria-label={KIND_LABELS[kind].plural}>
      <thead><tr><th scope="col">Name</th><th scope="col">Revision</th><th scope="col">Default</th><th scope="col">Actions</th></tr></thead>
      <tbody>{policies.map(policy => <tr key={policy.id}>
        <td>{policy.name || policy.id}</td>
        <td>{policy.revision}</td>
        <td>{isDefault(policy) ? 'Pinned at Start' : '—'}</td>
        <td>
          {canEdit && <button type="button" className="btn btn-ghost btn-sm" onClick={() => startEdit(policy)}>Edit</button>}
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => { setEditing(null); setRevisionView({ policy, revision: String(policy.revision), record: null, error: '' }) }}>View a revision</button>
          {canEdit && <button type="button" className="btn btn-ghost btn-sm" onClick={() => void remove(policy)}>Delete</button>}
        </td>
      </tr>)}</tbody>
    </table>}

    {revisionView && <section aria-label="Policy revision" className="settings-subsection">
      <h4>{revisionView.policy.name || revisionView.policy.id}: a past revision</h4>
      <form className="settings-inline-actions" onSubmit={event => void viewRevision(event)}>
        <label>Revision <input className="settings-input" type="number" min={1} value={revisionView.revision} onChange={event => setRevisionView({ ...revisionView, revision: event.target.value })} /></label>
        <button type="submit" className="btn btn-secondary btn-sm">Show</button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => setRevisionView(null)}>Close</button>
      </form>
      {revisionView.error && <p role="alert" className="settings-error-inline">{revisionView.error}</p>}
      {revisionView.record && <>
        <p>Revision {revisionView.record.revision}{revisionView.record.deleted ? ' (the policy was deleted at this revision)' : ''}, saved {formatDateTime(revisionView.record.created_at)}. Read-only: runs that pinned it keep it.</p>
        <pre className="analysis-policy-json">{JSON.stringify(revisionView.record.definition, null, 2)}</pre>
      </>}
    </section>}

    {editing && <form className="settings-subsection analysis-policy-editor" aria-label={`${editing.id ? 'Edit' : 'New'} ${KIND_LABELS[editing.kind].singular}`} onSubmit={event => void save(event)}>
      <h4>{editing.id ? `Edit ${editing.draft.name || 'policy'} (revision ${editing.revision})` : `New ${KIND_LABELS[editing.kind].singular}`}</h4>
      <label className="settings-field">Name <input className="settings-input" value={editing.draft.name} maxLength={256} onChange={event => setEditing({ ...editing, draft: { ...editing.draft, name: event.target.value } } as Editing)} /></label>
      <label className="settings-field">Description <input className="settings-input" value={editing.draft.description} maxLength={256} onChange={event => setEditing({ ...editing, draft: { ...editing.draft, description: event.target.value } } as Editing)} /></label>
      <label className="settings-checkbox"><input type="checkbox" checked={editing.draft.applyByDefault} onChange={event => setEditing({ ...editing, draft: { ...editing.draft, applyByDefault: event.target.checked } } as Editing)} /> Pin this policy when runs start (the project default)</label>
      {editing.kind === 'browser_policy'
        ? <BrowserPolicyFields draft={editing.draft} onChange={draft => setEditing({ ...editing, draft })} />
        : <JourneyPolicyFields draft={editing.draft} onChange={draft => setEditing({ ...editing, draft })} />}
      {conflict && <Notice tone="warning" title="This policy changed since you opened it" action={<button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void reloadCurrent()}>Load the current revision</button>}>
        Someone saved a newer revision. Loading it replaces your edits here; copy anything you want to keep first.
      </Notice>}
      {saveError && <Notice tone="error" title="The policy was not saved">{saveError}</Notice>}
      <div className="settings-inline-actions">
        <button type="submit" className="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save policy'}</button>
        <button type="button" className="btn btn-secondary" onClick={() => { setEditing(null); setSaveError(''); setConflict(false) }}>Cancel</button>
      </div>
    </form>}
  </section>
}

function BrowserPolicyFields({ draft, onChange }: { draft: BrowserPolicyDraft; onChange: (draft: BrowserPolicyDraft) => void }) {
  const metric = (name: typeof BROWSER_POLICY_METRICS[number], patch: Partial<BrowserPolicyDraft['metrics'][typeof name]>) => onChange({ ...draft, metrics: { ...draft.metrics, [name]: { ...draft.metrics[name], ...patch } } })
  return <>
    <table className="tx-table" aria-label="Scored metrics">
      <thead><tr><th scope="col">Metric</th><th scope="col">Scored</th><th scope="col">Good at or below</th><th scope="col">Poor at or above</th><th scope="col">Weight</th></tr></thead>
      <tbody>{BROWSER_POLICY_METRICS.map(name => <tr key={name}>
        <th scope="row">{name.toUpperCase()}{name === 'cls' ? ' (score)' : ' (ms)'}</th>
        <td><input type="checkbox" aria-label={`Score ${name.toUpperCase()}`} checked={draft.metrics[name].include} onChange={event => metric(name, { include: event.target.checked })} /></td>
        <td><input className="settings-input" aria-label={`${name.toUpperCase()} good bound`} inputMode="decimal" value={draft.metrics[name].good} disabled={!draft.metrics[name].include} onChange={event => metric(name, { good: event.target.value })} /></td>
        <td><input className="settings-input" aria-label={`${name.toUpperCase()} poor bound`} inputMode="decimal" value={draft.metrics[name].poor} disabled={!draft.metrics[name].include} onChange={event => metric(name, { poor: event.target.value })} /></td>
        <td><input className="settings-input" aria-label={`${name.toUpperCase()} weight`} inputMode="decimal" value={draft.metrics[name].weight} disabled={!draft.metrics[name].include} onChange={event => metric(name, { weight: event.target.value })} /></td>
      </tr>)}</tbody>
    </table>
    <fieldset><legend>Applies to</legend>
      <label className="settings-checkbox"><input type="checkbox" checked={draft.engines.chromium} onChange={event => onChange({ ...draft, engines: { ...draft.engines, chromium: event.target.checked } })} /> Chromium</label>
      <label className="settings-checkbox"><input type="checkbox" checked={draft.engines.webkit} onChange={event => onChange({ ...draft, engines: { ...draft.engines, webkit: event.target.checked } })} /> WebKit</label>
      <label className="settings-field">Device profiles (comma separated; empty applies to all) <input className="settings-input" value={draft.deviceProfiles} onChange={event => onChange({ ...draft, deviceProfiles: event.target.value })} /></label>
      <p className="run-report-muted">With no engine selected, the policy applies to every engine.</p>
    </fieldset>
    <fieldset><legend>Baseline comparison (root-cause analysis)</legend>
      <label className="settings-field">Baseline <select className="settings-input" value={draft.baselineMode} onChange={event => onChange({ ...draft, baselineMode: event.target.value as BrowserPolicyDraft['baselineMode'] })}>
        <option value="latest_compatible">The analysis baseline run</option>
        <option value="pinned_run">One pinned run</option>
        <option value="none">No baseline comparison</option>
      </select></label>
      {draft.baselineMode === 'pinned_run' && <label className="settings-field">Pinned run ID <input className="settings-input" value={draft.baselineRunId} maxLength={128} onChange={event => onChange({ ...draft, baselineRunId: event.target.value })} /></label>}
      <p className="run-report-muted">Effect floors only raise the defaults (50 ms and 10% for timings, 0.02 for CLS).</p>
      <label className="settings-field">Timing floor (ms) <input className="settings-input" inputMode="decimal" value={draft.floorTimingMs} onChange={event => onChange({ ...draft, floorTimingMs: event.target.value })} /></label>
      <label className="settings-field">Relative timing floor (fraction, 0.1–10) <input className="settings-input" inputMode="decimal" value={draft.floorTimingRelative} onChange={event => onChange({ ...draft, floorTimingRelative: event.target.value })} /></label>
      <label className="settings-field">CLS floor <input className="settings-input" inputMode="decimal" value={draft.floorCls} onChange={event => onChange({ ...draft, floorCls: event.target.value })} /></label>
    </fieldset>
  </>
}

function JourneyPolicyFields({ draft, onChange }: { draft: JourneyPolicyDraft; onChange: (draft: JourneyPolicyDraft) => void }) {
  const update = (index: number, patch: Partial<JourneyPolicyDraft['journeys'][number]>) => onChange({ ...draft, journeys: draft.journeys.map((journey, current) => current === index ? { ...journey, ...patch } : journey) })
  return <>
    <label className="settings-checkbox"><input type="checkbox" checked={draft.stepRiskEnabled} onChange={event => onChange({ ...draft, stepRiskEnabled: event.target.checked })} /> Report the step-risk index</label>
    <table className="tx-table" aria-label="Journeys">
      <thead><tr><th scope="col">ID</th><th scope="col">Name</th><th scope="col">Starts at</th><th scope="col">Succeeds at</th><th scope="col">Weight</th><th scope="col"><span className="sr-only">Remove</span></th></tr></thead>
      <tbody>{draft.journeys.map((journey, index) => <tr key={index}>
        <td><input className="settings-input" aria-label={`Journey ${index + 1} ID`} value={journey.id} onChange={event => update(index, { id: event.target.value })} /></td>
        <td><input className="settings-input" aria-label={`Journey ${index + 1} name`} value={journey.name} onChange={event => update(index, { name: event.target.value })} /></td>
        <td>
          <select className="settings-input" aria-label={`Journey ${index + 1} start kind`} value={journey.startKind} onChange={event => update(index, { startKind: event.target.value as 'transaction' | 'action' })}><option value="transaction">Transaction</option><option value="action">Action</option></select>
          <input className="settings-input" aria-label={`Journey ${index + 1} start`} value={journey.start} onChange={event => update(index, { start: event.target.value })} />
        </td>
        <td>
          <select className="settings-input" aria-label={`Journey ${index + 1} success kind`} value={journey.successKind} onChange={event => update(index, { successKind: event.target.value as 'transaction' | 'action' })}><option value="transaction">Transaction</option><option value="action">Action</option></select>
          <input className="settings-input" aria-label={`Journey ${index + 1} success`} value={journey.success} onChange={event => update(index, { success: event.target.value })} />
        </td>
        <td><input className="settings-input" aria-label={`Journey ${index + 1} weight`} inputMode="decimal" value={journey.weight} onChange={event => update(index, { weight: event.target.value })} /></td>
        <td><button type="button" className="btn btn-ghost btn-sm" disabled={draft.journeys.length === 1} onClick={() => onChange({ ...draft, journeys: draft.journeys.filter((_, current) => current !== index) })}>Remove</button></td>
      </tr>)}</tbody>
    </table>
    <button type="button" className="btn btn-secondary btn-sm" disabled={draft.journeys.length >= 64} onClick={() => onChange({ ...draft, journeys: [...draft.journeys, emptyJourney(draft.journeys.length)] })}>Add journey</button>
  </>
}
