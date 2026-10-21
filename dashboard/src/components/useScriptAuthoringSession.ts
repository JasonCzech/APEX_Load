import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { getErrorMessage } from '../utils/errorUtils'
import { admitScriptAuthoringBaseline, reapplyScriptDraft } from '../utils/scriptAuthoringRevision'
import { formatScriptJson } from './scriptEditorJsonModel'
import { admitValidationResponse, draftFingerprint, sameValidationBinding, validationContextChanges, validationRequestStillCurrent, type ValidationAdmission, type ValidationBinding, type ValidationEcho, type ValidationRequest } from './scriptAuthoringSession'
import type { ScriptValidationContext } from '../types/scripts'

// ResolvedScriptDraft is the one resolution of the current draft that
// validate, save, replay and debug act on: pending data cells flushed, a dirty
// JSON pane parsed once, and the document's fingerprint.
export interface ResolvedScriptDraft {
  script: ScriptDefinition
  documentKey: string
  fingerprint: string
  editGeneration: number
}

export type AuthoringRevisionState = 'loading' | 'ready' | 'blocked'

// BaselineOptions pair the editor with the stored script's authoring revision:
// the baseline it was opened at, and a newer one under review after a
// conflict.
interface BaselineOptions {
  scriptId?: string
  projectId?: string
  initialAuthoring?: ScriptAuthoringBaseline | null
  initialRetainedNeedsReview?: boolean
  getScriptAuthoring: (scriptId: string, projectId?: string, signal?: AbortSignal) => Promise<ScriptAuthoringBaseline>
  // A load answers only for the editor scope it started in, while mounted.
  resourceScopeRef: { readonly current: unknown }
  mountedRef: { readonly current: boolean }
  // The editor's current document key (save updates it as well).
  documentKeyRef: { readonly current: string | null }
  hasPendingEdits: () => boolean
  // A loaded baseline becomes the editor's document.
  onHydrate: (script: ScriptDefinition) => void
  // The reviewed baseline is adopted: the document to show (the draft
  // reapplied to it, or the baseline itself) and the baseline it pairs with.
  onAdopt: (document: ScriptDefinition, baselineScript: ScriptDefinition, reapplied: boolean) => void
}

// DocumentOptions decide whether the editor holds unsaved work.
interface DocumentOptions {
  initialRetainedDraft?: ScriptDefinition | null
  jsonDirty: boolean
  pendingEditCount: number
  formattedDocument: string
  savedDocumentKey: string
}

// NavigationOptions guard leaving the editor while it holds unsaved work.
interface NavigationOptions {
  id: string
  label: string
  save: () => Promise<boolean>
  onClose?: () => void
  onReturn?: () => void
  // The guards of a source surface that stays open behind the editor. Close
  // and the return both lead back to it, so leaving keeps them.
  retainedGuardIds?: readonly string[]
}

interface CommonOptions {
  // The document the editor was opened with, and whether it opens as unsaved
  // work (a draft never saved to the library).
  initialScript?: ScriptDefinition | null
  initiallyDirty?: boolean
  // The parsed current document (structured script or a dirty JSON pane).
  resolveCurrentScript: () => ScriptDefinition
  // Applies and commits pending data-cell edits, returning the edit generation.
  flushPendingEdits: (script: ScriptDefinition) => { script: ScriptDefinition; editGeneration: number }
  // The current document's canonical JSON, or null while the JSON pane is invalid.
  documentKey: string | null
  // Everything else a validation result depends on.
  context: string
  document?: DocumentOptions
  navigation?: NavigationOptions
  // Reads the controller's current validation context for the result held
  // now (AUTH-B); omitted, results bind to their document only.
  probeValidationContext?: (binding: ValidationBinding) => Promise<ScriptValidationContext | null>
}

// The session either owns the paired baseline, or is told the revision the
// draft was edited from ('' for an unsaved script). Without a baseline it is
// always ready to save, and the baseline calls do nothing.
type Options = CommonOptions & (
  | { baseline: BaselineOptions; contentRevision?: never }
  | { baseline?: undefined; contentRevision?: string }
)

const noop = () => undefined

// useScriptAuthoringSession owns the script editor's session: the protocol
// shared by validate, save, replay and debug (draft resolution, the binding a
// validation result must still match to be current, and the request
// generation that drops late responses); the paired authoring baseline of a
// stored script; whether the editor holds unsaved work; and guarded
// navigation away from it.
// The interval at which a held result's controller context is re-read while
// the editor is visible (also on focus and when the page becomes visible).
export const VALIDATION_CONTEXT_PROBE_INTERVAL_MS = 60_000
export const VALIDATION_CONTEXT_PROBE_SPACING_MS = 5_000

export function useScriptAuthoringSession({ initialScript, initiallyDirty: givenInitiallyDirty, resolveCurrentScript, flushPendingEdits, documentKey, contentRevision: givenRevision = '', context, baseline: baselineOptions, document: documentOptions, navigation: navigationOptions, probeValidationContext: probeValidationContextOption }: Options) {
  // ---- Paired baseline ----------------------------------------------------
  const [authoringBaseline, setAuthoringBaseline] = useState<ScriptAuthoringBaseline | null>(null)
  const [authoringRevisionState, setAuthoringRevisionState] = useState<AuthoringRevisionState>(baselineOptions ? 'loading' : 'ready')
  const [authoringRevisionReason, setAuthoringRevisionReason] = useState('')
  const [reviewedAuthoringBaseline, setReviewedAuthoringBaseline] = useState<ScriptAuthoringBaseline | null>(null)
  const [authoringReviewLoading, setAuthoringReviewLoading] = useState(false)
  const authoringLoadGenerationRef = useRef(0)
  // Callbacks are read at call time, so the load effect follows the editor's
  // inputs, not its handlers' identities.
  const baselineOptionsRef = useRef(baselineOptions)
  baselineOptionsRef.current = baselineOptions
  const scriptId = baselineOptions?.scriptId
  const projectId = baselineOptions?.projectId
  const initialAuthoring = baselineOptions?.initialAuthoring
  const initialRetainedNeedsReview = Boolean(baselineOptions?.initialRetainedNeedsReview)
  const initiallyDirty = Boolean(givenInitiallyDirty)
  const getScriptAuthoring = baselineOptions?.getScriptAuthoring
  const ownsBaseline = Boolean(baselineOptions)

  useEffect(() => {
    const options = baselineOptionsRef.current
    if (!options || !getScriptAuthoring) return undefined
    const generation = ++authoringLoadGenerationRef.current
    const scope = options.resourceScopeRef.current
    const current = () => authoringLoadGenerationRef.current === generation && options.resourceScopeRef.current === scope && options.mountedRef.current
    const abort = new AbortController()
    setAuthoringBaseline(null)
    setReviewedAuthoringBaseline(null)
    setAuthoringRevisionReason('')
    setAuthoringReviewLoading(false)
    if (!scriptId) { setAuthoringRevisionState('ready'); return () => abort.abort() }
    if (!initialAuthoring && initialRetainedNeedsReview) {
      setAuthoringRevisionState('blocked')
      setAuthoringRevisionReason('The canonical authoring baseline could not be loaded. Your draft is retained. Load the current baseline and explicitly review it before saving.')
      return () => abort.abort()
    }
    setAuthoringRevisionState('loading')
    const initialKey = options.documentKeyRef.current
    const load = async () => {
      try {
        const loaded = initialAuthoring
          ? admitScriptAuthoringBaseline(initialAuthoring.projection, initialAuthoring.etag, scriptId, projectId)
          : await getScriptAuthoring(scriptId, projectId, abort.signal)
        if (!current()) return
        const latest = baselineOptionsRef.current ?? options
        if (initialRetainedNeedsReview || (!initialAuthoring && (initiallyDirty || latest.documentKeyRef.current !== initialKey || latest.hasPendingEdits()))) {
          setReviewedAuthoringBaseline(loaded)
          setAuthoringRevisionState('blocked')
          setAuthoringRevisionReason('The server baseline loaded after this draft was edited. Review and explicitly reapply the draft before saving.')
          return
        }
        setAuthoringBaseline(loaded)
        setAuthoringRevisionState('ready')
        if (!initialAuthoring) latest.onHydrate(loaded.projection.script)
      } catch (cause) {
        if (!current()) return
        setAuthoringRevisionState('blocked')
        setAuthoringRevisionReason(getErrorMessage(cause, 'Unable to load a protected authoring baseline.'))
      }
    }
    void load()
    return () => { abort.abort(); authoringLoadGenerationRef.current += 1 }
  }, [getScriptAuthoring, initialAuthoring, initialRetainedNeedsReview, initialScript, initiallyDirty, projectId, scriptId])

  // loadCurrentAuthoringBaseline reads the server's current baseline for
  // review after a conflict.
  const loadCurrentAuthoringBaseline = useCallback(async () => {
    const options = baselineOptionsRef.current
    if (!options || !scriptId || authoringReviewLoading) return
    const generation = ++authoringLoadGenerationRef.current
    const scope = options.resourceScopeRef.current
    const current = () => authoringLoadGenerationRef.current === generation && options.resourceScopeRef.current === scope && options.mountedRef.current
    setAuthoringReviewLoading(true)
    setReviewedAuthoringBaseline(null)
    try {
      const loaded = await options.getScriptAuthoring(scriptId, projectId)
      if (!current()) return
      setReviewedAuthoringBaseline(loaded)
    } catch (cause) {
      if (current()) setAuthoringRevisionReason(getErrorMessage(cause, 'Unable to load the current baseline.'))
    } finally {
      if (current()) setAuthoringReviewLoading(false)
    }
  }, [authoringReviewLoading, projectId, scriptId])

  // acceptSavedBaseline pairs the editor with the revision a save produced.
  const acceptSavedBaseline = useCallback((saved: ScriptAuthoringBaseline) => {
    if (baselineOptionsRef.current) setAuthoringBaseline(saved)
  }, [])

  // blockForReconciliation stops saving until the current baseline has been
  // reviewed (a save refused for a changed revision, or of unknown outcome).
  const blockForReconciliation = useCallback((reason: string) => {
    if (!baselineOptionsRef.current) return
    setAuthoringRevisionState('blocked')
    setReviewedAuthoringBaseline(null)
    setAuthoringRevisionReason(reason)
  }, [])

  // ---- Unsaved work -------------------------------------------------------
  const initialRetainedDraft = documentOptions?.initialRetainedDraft
  const [initialSaveRequired, setInitialSaveRequired] = useState(initiallyDirty || Boolean(initialRetainedDraft))
  const initialSaveSourceRef = useRef({ script: initialScript, initiallyDirty })
  useEffect(() => {
    const previous = initialSaveSourceRef.current
    if (previous.script !== initialScript || (!previous.initiallyDirty && initiallyDirty)) {
      setInitialSaveRequired(initiallyDirty || Boolean(initialRetainedDraft))
    }
    initialSaveSourceRef.current = { script: initialScript, initiallyDirty }
  }, [initialScript, initiallyDirty, initialRetainedDraft])
  // The one dirty calculation: navigation guards, save, download, validation
  // and the evidence summary all read it.
  const dirty = Boolean(documentOptions) && (initialSaveRequired || documentOptions!.jsonDirty || documentOptions!.pendingEditCount > 0 || documentOptions!.formattedDocument !== documentOptions!.savedDocumentKey)
  // markSaved records that the editor's document was saved to the library.
  const markSaved = useCallback(() => setInitialSaveRequired(false), [])

  // ---- Validation binding -------------------------------------------------
  const contentRevision = ownsBaseline ? authoringBaseline?.projection.content_revision ?? '' : givenRevision
  const currentBinding = useMemo<ValidationBinding | null>(
    () => documentKey === null ? null : { fingerprint: draftFingerprint(documentKey), contentRevision, context },
    [contentRevision, context, documentKey],
  )
  const currentBindingRef = useRef(currentBinding)
  currentBindingRef.current = currentBinding
  const [validatedBinding, setValidatedBinding] = useState<ValidationBinding | null>(null)
  const latestRequestRef = useRef(0)
  // The controller context the current result was validated under, and the
  // one a later probe observed (AUTH-B). A result from a controller that
  // reports no context binds to its document only.
  const [validatedContext, setValidatedContext] = useState<ScriptValidationContext | null>(null)
  const [observedContext, setObservedContext] = useState<ScriptValidationContext | null>(null)
  const validationContextChanged = validatedBinding ? validationContextChanges(validatedContext, observedContext) : []

  const resolveDraft = useCallback((): ResolvedScriptDraft => {
    const { script, editGeneration } = flushPendingEdits(resolveCurrentScript())
    const key = formatScriptJson(script)
    return { script, documentKey: key, fingerprint: draftFingerprint(key), editGeneration }
  }, [flushPendingEdits, resolveCurrentScript])

  // adoptReviewedAuthoringBaseline makes the reviewed baseline current: the
  // draft reapplied to it, or (reapply false) the baseline itself.
  const adoptReviewedAuthoringBaseline = useCallback((reapply: boolean) => {
    const options = baselineOptionsRef.current
    if (!options || !reviewedAuthoringBaseline || authoringReviewLoading) return
    try {
      const adopted = reapply
        ? reapplyScriptDraft(resolveDraft().script, reviewedAuthoringBaseline, authoringBaseline?.projection.script || initialScript || undefined)
        : reviewedAuthoringBaseline.projection.script
      options.onAdopt(adopted, reviewedAuthoringBaseline.projection.script, reapply)
      setAuthoringBaseline(reviewedAuthoringBaseline)
      setReviewedAuthoringBaseline(null)
      setAuthoringRevisionReason('')
      setAuthoringRevisionState('ready')
    } catch (cause) { setAuthoringRevisionReason(getErrorMessage(cause, 'Resolve invalid draft JSON before reapplying.')) }
  }, [authoringBaseline, authoringReviewLoading, initialScript, resolveDraft, reviewedAuthoringBaseline])

  // beginValidation issues a request for draft and supersedes any earlier one.
  // The flushed draft may not have rendered yet, so the request binds to the
  // draft's own fingerprint and the session's current revision and context.
  const beginValidation = useCallback((draft: ResolvedScriptDraft, stored: boolean): ValidationRequest => {
    latestRequestRef.current += 1
    setValidatedBinding(null)
    setValidatedContext(null)
    setObservedContext(null)
    const current = currentBindingRef.current
    return {
      requestId: latestRequestRef.current,
      stored,
      sent: { fingerprint: draft.fingerprint, contentRevision: current?.contentRevision ?? '', context: current?.context ?? '' },
      issuedAgainst: current,
    }
  }, [])

  // admitValidation decides a response against the document current now.
  const admitValidation = useCallback((request: ValidationRequest, echo: ValidationEcho | null | undefined, current: ValidationBinding | null = currentBindingRef.current): ValidationAdmission => (
    admitValidationResponse(latestRequestRef.current, request, echo, current)
  ), [])

  // adoptValidated binds an admitted result to the document the editor holds
  // after applying it (validation may add runtime-settings metadata).
  const adoptValidated = useCallback((request: ValidationRequest, adoptedDocumentKey: string, context?: ScriptValidationContext | null) => {
    setValidatedBinding({ ...request.sent, fingerprint: draftFingerprint(adoptedDocumentKey) })
    setValidatedContext(context ?? null)
    setObservedContext(context ?? null)
  }, [])

  // probeValidationContext re-reads the controller context for the result
  // held now. A probe answer is applied only while that same result is
  // still held: a late answer cannot judge a newer result.
  const probeRef = useRef(probeValidationContextOption)
  probeRef.current = probeValidationContextOption
  const validatedBindingRef = useRef(validatedBinding)
  validatedBindingRef.current = validatedBinding
  // One probe at a time, and none within VALIDATION_CONTEXT_PROBE_SPACING_MS
  // of the last: a focus and a visibility change arrive together, and each
  // probe resolves the script's secrets on the controller.
  const probeInFlightRef = useRef(false)
  const lastProbeAtRef = useRef(0)
  const probeValidationContext = useCallback(async () => {
    const probe = probeRef.current
    const binding = validatedBindingRef.current
    if (!probe || !binding || !sameValidationBinding(binding, currentBindingRef.current)) return
    if (probeInFlightRef.current || Date.now() - lastProbeAtRef.current < VALIDATION_CONTEXT_PROBE_SPACING_MS) return
    probeInFlightRef.current = true
    lastProbeAtRef.current = Date.now()
    const request = latestRequestRef.current
    try {
      const observed = await probe(binding)
      if (observed && request === latestRequestRef.current && validatedBindingRef.current === binding) setObservedContext(observed)
    } catch { /* An unreachable controller leaves the last observation; a start rechecks everything. */ } finally {
      probeInFlightRef.current = false
    }
  }, [])
  useEffect(() => {
    if (!validatedBinding || !validatedContext || !probeRef.current) return undefined
    const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
    const onFocus = () => { if (visible()) void probeValidationContext() }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onFocus)
    const timer = window.setInterval(onFocus, VALIDATION_CONTEXT_PROBE_INTERVAL_MS)
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onFocus)
      window.clearInterval(timer)
    }
  }, [probeValidationContext, validatedBinding, validatedContext])

  const isLatestValidation = useCallback((request: ValidationRequest) => request.requestId === latestRequestRef.current, [])

  // validationRequestIsCurrent: the latest request, for the document current now.
  const validationRequestIsCurrent = useCallback((request: ValidationRequest) => (
    validationRequestStillCurrent(latestRequestRef.current, request, currentBindingRef.current)
  ), [])

  // invalidateValidation drops every in-flight request and the current result.
  const invalidateValidation = useCallback(() => {
    latestRequestRef.current += 1
    setValidatedBinding(null)
    setValidatedContext(null)
    setObservedContext(null)
  }, [])

  const clearValidatedBinding = useCallback(() => {
    setValidatedBinding(null)
    setValidatedContext(null)
    setObservedContext(null)
  }, [])

  // ---- Navigation ---------------------------------------------------------
  const navigationRef = useRef(navigationOptions)
  navigationRef.current = navigationOptions
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const guardId = navigationOptions?.id
  const guardLabel = navigationOptions?.label
  const guard = useMemo<NavigationGuard | null>(() => guardId ? {
    id: guardId,
    label: guardLabel || guardId,
    isDirty: () => dirtyRef.current,
    save: () => navigationRef.current?.save() ?? false,
    discard: noop,
  } : null, [guardId, guardLabel])
  useNavigationGuard(guard, dirty)
  const { runGuarded } = useNavigationGuardRunner()
  // close and returnToSource leave the editor only after the guard accepts.
  const close = useCallback(() => {
    const onClose = navigationRef.current?.onClose
    if (onClose) runGuarded(onClose, navigationRef.current?.retainedGuardIds)
  }, [runGuarded])
  const returnToSource = useCallback(() => {
    const onReturn = navigationRef.current?.onReturn
    if (onReturn) runGuarded(onReturn, navigationRef.current?.retainedGuardIds)
  }, [runGuarded])

  return {
    currentBinding,
    validatedBinding,
    validationIsCurrent: sameValidationBinding(validatedBinding, currentBinding) && validationContextChanged.length === 0,
    // Components of the controller context that changed since the result
    // was validated; non-empty means the result is stale for that reason.
    validationContextChanged,
    validatedContext,
    probeValidationContext,
    resolveDraft,
    beginValidation,
    admitValidation,
    adoptValidated,
    isLatestValidation,
    validationRequestIsCurrent,
    invalidateValidation,
    clearValidatedBinding,
    authoringBaseline,
    authoringRevisionState,
    authoringRevisionReason,
    reviewedAuthoringBaseline,
    authoringReviewLoading,
    loadCurrentAuthoringBaseline,
    adoptReviewedAuthoringBaseline,
    acceptSavedBaseline,
    blockForReconciliation,
    dirty,
    markSaved,
    close,
    returnToSource,
  }
}
