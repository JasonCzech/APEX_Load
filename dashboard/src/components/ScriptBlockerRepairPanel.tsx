import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { ApiError, ApiResponseTooLargeError } from '../hooks/apiClient'
import type { ScriptRepairPreviewResponse, ScriptRepairResponse } from '../hooks/api/useScriptApi'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { scriptSaveFailedOnServer, scriptSaveNeedsReconciliation } from '../utils/scriptAuthoringRevision'
import { GuidedRepairForm, RepairPreview } from './GuidedRepairForm'
import { actionImportFindings, groupImportFindings, importActionEntries, importReviewFindings, readinessCounts, storedRepairOptions, type ImportFinding, type RepairFields, type RepairOperation } from './importReviewIssues'
import { blockerTitle, distinctBlockerCodes, REPLAY_REFUSAL_SENTENCE, storedScriptImpactText, type ProtectedBodySource } from './replayReadinessIssues'

type Props = {
  // The saved script; a script not yet saved has no copy to repair.
  scriptId?: string | null
  projectId?: string
  // The editor's paired baseline: the revision a repair is computed from.
  baseline: ScriptAuthoringBaseline | null
  // Why the editor's baseline cannot be repaired now (loading, or held for reconciliation).
  blockedReason?: string
  // The editor's document, for an extractor's preceding responses.
  script: ScriptDefinition
  // Where a protected body is restored: importing the source again, or the
  // import review an unsaved draft was opened from.
  bodySource?: ProtectedBodySource
  action: ScriptActionDefinition
  // The step's path as the import findings name it: `init[0]`, `actions[2].params.requests[1]`.
  actionPath: string
  // A repair saves straight to the script, so it waits for unsaved edits to
  // be saved or discarded (it never rebases them).
  dirty: boolean
  saving: boolean
  onSaveFirst: () => void
  // The repair was saved: the response carries the stored script and its
  // validation, not the preview's before/after scripts.
  onApplied: (response: ScriptRepairResponse) => void
  // The saved copy changed under the editor, or an apply's outcome is unknown.
  onReconcile: (error: unknown) => void
}

const FINDING_GONE = 'Finding is no longer present'
const SCRIPT_NOT_FOUND = /script not found/i
const DIRTY_REASON = 'Save or discard your changes first'
// A preview carries the whole script twice (before and after), so a large
// enough script's preview passes the response cap although the request worked.
const PREVIEW_TOO_LARGE = 'This repair preview is too large to load: it holds the whole script before and after the repair, which is more than the editor accepts in one response. Nothing was changed. Re-import the source to repair this blocker in the import review.'

// The route's one lasting 503: this controller keeps no script revisions, so
// it repairs no saved script. Every other 5xx is masked, and may be transient
// or follow an apply the server already saved.
function revisionsUnavailable(cause: unknown): boolean {
  return cause instanceof ApiError && cause.status === 503
    && (cause.code === 'script_revisions_unavailable' || /^durable script content revisions unavailable\b/i.test(cause.message))
}
const emptyFields = (code: string): RepairFields => ({ kind: storedRepairOptions(code)[0]?.value || '', value: '', responsePath: '', extractorType: 'jsonpath' })

// Blocking issues after a repair, grouped as the import review groups them.
function repairedBlockers(result: ScriptRepairPreviewResponse): number {
  const findings = importReviewFindings({ script: result.script, inspection: result, fidelityBlockers: result.fidelity_report?.blockers })
  return readinessCounts(groupImportFindings(findings, result.script)).blockers
}

// What a step's blocker means, and that replay refuses it, worded as Author
// Problems words it for the editor's script (no form here supplies a
// protected body).
function blockerReason(finding: ImportFinding, bodySource: ProtectedBodySource): string {
  const impact = storedScriptImpactText(finding.code, finding.detail, bodySource) || 'The import held this step back because it could not convert it faithfully.'
  return `${impact} ${REPLAY_REFUSAL_SENTENCE}`
}

/**
 * Why a blocked step cannot replay, and the import's guided repair for it on
 * the saved script (POST /scripts/{id}/repairs/{preview|apply}). The editor
 * focuses its first control from "Resolve N blockers".
 */
export default function ScriptBlockerRepairPanel({ scriptId, projectId, baseline, blockedReason, script, bodySource = 'reimport', action, actionPath, dirty, saving, onSaveFirst, onApplied, onReconcile }: Props) {
  const api = useApi()
  const headingId = useId()
  // One problem per code, as the start gate and Author Problems count them.
  const findings = useMemo(() => {
    const gated = actionImportFindings(action, actionPath).filter(finding => finding.gate === 'execution')
    const codes = new Set(distinctBlockerCodes(gated.map(finding => finding.code)))
    return gated.filter(finding => codes.has(finding.code))
  }, [action, actionPath])
  const repairable = findings.filter(finding => storedRepairOptions(finding.code).length > 0)
  const entries = useMemo(() => importActionEntries(script), [script])
  const revision = baseline?.projection.content_revision || ''
  const [selectedCode, setSelectedCode] = useState('')
  const finding = repairable.find(item => item.code === selectedCode) || repairable[0]
  const [fieldDraft, setFields] = useState<RepairFields>(() => emptyFields(finding?.code || ''))
  const [preview, setPreview] = useState<ScriptRepairPreviewResponse | null>(null)
  const [pending, setPending] = useState<{ operation: RepairOperation; finding: ImportFinding } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  // The controller has no saved-script repairs for this script.
  const [unavailable, setUnavailable] = useState('')
  // Whether the editor has unsaved edits now, for a response to a request
  // sent while it had none.
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  // A response is for the copy, step and revision it was asked about.
  const scope = `${scriptId || ''}:${revision}:${actionPath}`
  const scopeRef = useRef(scope)
  const previewVersion = useRef(0)
  useEffect(() => {
    scopeRef.current = scope
    previewVersion.current++
    setPreview(null); setPending(null); setError(''); setBusy(false)
    return () => { scopeRef.current = '' }
  }, [scope])
  // Grouping the repaired copy analyzes the whole script, so it runs once per
  // preview, not on every editor render.
  const previewBlockers = useMemo(() => preview ? repairedBlockers(preview) : 0, [preview])
  const locked = busy || saving
  const options = finding ? storedRepairOptions(finding.code) : []
  // A draft for another blocker's operation starts afresh for this one.
  const fields = options.some(option => option.value === fieldDraft.kind) ? fieldDraft : emptyFields(finding?.code || '')

  function resetPreview() { previewVersion.current++; setPreview(null); setPending(null) }
  function changeFields(change: Partial<RepairFields>) {
    setFields({ ...fields, ...change })
    resetPreview()
  }
  function chooseFinding(code: string) {
    setSelectedCode(code); setFields(emptyFields(code)); setError(''); setNotice(''); resetPreview()
  }

  // Preview and apply both read the saved copy at the editor's step path,
  // which unsaved edits may have moved, so both wait for a clean editor.
  async function run(operation: RepairOperation, apply = false) {
    const target = apply ? pending?.finding : finding
    if (!scriptId || !baseline || blockedReason || !target || locked || dirty) return
    const requestScope = scopeRef.current
    const requestPreviewVersion = previewVersion.current
    const current = () => scopeRef.current === requestScope
    setBusy(true); setError(''); setNotice('')
    try {
      const request = { finding: target.code, action_path: target.action_path || actionPath, operation }
      if (apply) {
        const result = await api.applyScriptRepair(scriptId, request, baseline, projectId)
        // Saved: the editor adopts it even if this step is no longer open,
        // and says so in its own live region, which outlives this panel.
        onApplied(result)
        if (current()) { resetPreview(); setSelectedCode(''); setFields(emptyFields('')) }
      } else {
        const result = await api.previewScriptRepair(scriptId, request, baseline, projectId)
        if (current() && requestPreviewVersion === previewVersion.current) { setPreview(result); setPending({ operation, finding: target }) }
      }
    } catch (cause) {
      // An apply the server may have saved (a lost response, an outcome code,
      // any server failure, including one after the save) is reconciled, as a
      // stale revision is; only the capability refusal saved nothing.
      const reconcile = apply
        ? !revisionsUnavailable(cause) && (scriptSaveNeedsReconciliation(cause) || scriptSaveFailedOnServer(cause))
        : cause instanceof ApiError && cause.status === 412
      if (reconcile) {
        onReconcile(cause)
        if (current()) setError(cause instanceof ApiError && cause.status === 412
          ? 'The saved script changed since this editor loaded it, so the repair was not applied. Load the current version, then repair it again.'
          : 'We could not confirm whether this repair was saved. Load the current version to see whether it was applied.')
        return
      }
      if (!current()) return
      const status = cause instanceof ApiError ? cause.status : 0
      const message = cause instanceof Error && cause.message ? cause.message : 'Repair failed'
      // The route's own 404 names the script; any other 404 is an older
      // controller without the route.
      if (cause instanceof ApiResponseTooLargeError) setError(PREVIEW_TOO_LARGE)
      else if (status === 404 && SCRIPT_NOT_FOUND.test(message)) setError('This script was deleted or moved to another project since the editor loaded it. Reload the script.')
      else if (status === 404 || status === 405) setUnavailable('This controller cannot repair saved scripts. Re-import the source to resolve this blocker.')
      else if (revisionsUnavailable(cause)) setUnavailable('This controller keeps no script revisions, so it cannot repair saved scripts.')
      else if (status >= 500) setError(`Previewing the repair failed on the server. Choose Preview repair to try again. Server detail: ${message}`)
      else if (status === 409) setUnavailable(message)
      else if (message.includes(FINDING_GONE)) {
        resetPreview()
        // A reload would discard edits made since the request was sent.
        if (dirtyRef.current) setError('The saved script no longer matches this editor. Save your changes first, then preview the repair again.')
        else setNotice('Repair already applied: the saved script no longer has this finding. Reload the script to see it.')
      }
      else setError(message)
    } finally {
      if (current()) setBusy(false)
    }
  }

  let body
  if (repairable.length === 0) {
    // A protected body's reason above already says where it is restored.
    const protectedBody = findings.some(item => item.code === 'restored_body_required')
    body = <p className="script-blocker-repair-note">{protectedBody && bodySource === 'import-review' ? 'Needs the body repair in the import review.' : 'Needs a source change or re-import.'}{protectedBody ? '' : ' Correct the source and import it again; no guided repair applies to this blocker.'}</p>
  } else if (!scriptId) {
    // Saving an unsaved draft opened from the import review returns to that
    // review, which repairs the draft before it is saved.
    body = <p className="script-blocker-repair-note">{bodySource === 'import-review'
      ? 'Return to the import review to apply this repair to the draft before saving, or save the script and then repair it from the script library.'
      : 'Save the script to apply this repair. Repairs change the saved script, and this one is not saved yet.'}</p>
  } else if (blockedReason || unavailable) {
    body = <p className="script-blocker-repair-note">{blockedReason || unavailable}</p>
  } else if (!baseline) {
    // A saved script whose baseline the editor has not loaded, or holds for review.
    body = <p className="script-blocker-repair-note">Load and review the current version of the script before repairing it.</p>
  } else if (finding) {
    body = <>
      {repairable.length > 1 && <label>Blocker to repair<select aria-label="Blocker to repair" value={finding.code} disabled={locked} onChange={event => chooseFinding(event.target.value)}>{repairable.map(item => <option key={item.code} value={item.code}>{blockerTitle(item.code)}</option>)}</select></label>}
      {dirty && <div className="script-blocker-repair-guard" role="note"><p>Repairs are saved directly to the script. Save or discard your changes first.</p><button type="button" className="btn btn-secondary btn-sm" disabled={saving} onClick={onSaveFirst}>{saving ? 'Saving…' : 'Save changes'}</button></div>}
      <GuidedRepairForm finding={finding} occurrences={[finding]} options={options} entries={entries} fields={fields} onChange={changeFields} onOccurrence={() => undefined}
        locked={locked} busy={busy} onPreview={operation => { void run(operation) }} previewBlockedReason={dirty ? DIRTY_REASON : undefined} subject="script">
        {preview && <RepairPreview preview={preview} blockers={previewBlockers} apply={pending ? {
          disabled: locked || dirty, title: dirty ? DIRTY_REASON : undefined,
          onApply: () => { void run(pending.operation, true) },
          note: <p>Preview only. Apply saves the repaired script as a new revision.</p>,
        } : undefined} />}
      </GuidedRepairForm>
    </>
  }

  return <section className="script-blocker-repair" data-blocker-repair aria-labelledby={headingId}>
    <h4 id={headingId}>{findings.length === 1 ? 'This step blocks replay' : `${findings.length} problems on this step block replay`}</h4>
    <ul className="script-blocker-repair-reasons">
      {findings.map(item => <li key={item.code}>
        <strong>{blockerTitle(item.code)}</strong>
        {item.source_ref && <span className="script-blocker-repair-source">Source {item.source_ref}</span>}
        <p>{blockerReason(item, bodySource)}</p>
      </li>)}
    </ul>
    {body}
    <p className="script-blocker-repair-status" role="status">{notice}</p>
    {error && <p className="script-blocker-repair-error" role="alert">{error}</p>}
  </section>
}
