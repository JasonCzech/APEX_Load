import { useCallback, useMemo, useRef, useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { formatScriptJson } from './scriptEditorJsonModel'
import { admitValidationResponse, draftFingerprint, sameValidationBinding, validationRequestStillCurrent, type ValidationAdmission, type ValidationBinding, type ValidationEcho, type ValidationRequest } from './scriptAuthoringSession'

// ResolvedScriptDraft is the one resolution of the current draft that
// validate, save, replay and debug act on: pending data cells flushed, a dirty
// JSON pane parsed once, and the document's fingerprint.
export interface ResolvedScriptDraft {
  script: ScriptDefinition
  documentKey: string
  fingerprint: string
  editGeneration: number
}

interface Options {
  // The parsed current document (structured script or a dirty JSON pane).
  resolveCurrentScript: () => ScriptDefinition
  // Applies and commits pending data-cell edits, returning the edit generation.
  flushPendingEdits: (script: ScriptDefinition) => { script: ScriptDefinition; editGeneration: number }
  // The current document's canonical JSON, or null while the JSON pane is invalid.
  documentKey: string | null
  // The baseline revision the draft was edited from ('' for an unsaved script).
  contentRevision: string
  // Everything else a validation result depends on.
  context: string
}

// useScriptAuthoringSession owns the session protocol shared by validate,
// save, replay and debug: draft resolution, the binding a validation result
// must still match to be current, and the request generation that drops late
// responses.
export function useScriptAuthoringSession({ resolveCurrentScript, flushPendingEdits, documentKey, contentRevision, context }: Options) {
  const currentBinding = useMemo<ValidationBinding | null>(
    () => documentKey === null ? null : { fingerprint: draftFingerprint(documentKey), contentRevision, context },
    [contentRevision, context, documentKey],
  )
  const currentBindingRef = useRef(currentBinding)
  currentBindingRef.current = currentBinding
  const [validatedBinding, setValidatedBinding] = useState<ValidationBinding | null>(null)
  const latestRequestRef = useRef(0)

  const resolveDraft = useCallback((): ResolvedScriptDraft => {
    const { script, editGeneration } = flushPendingEdits(resolveCurrentScript())
    const key = formatScriptJson(script)
    return { script, documentKey: key, fingerprint: draftFingerprint(key), editGeneration }
  }, [flushPendingEdits, resolveCurrentScript])

  // beginValidation issues a request for draft and supersedes any earlier one.
  // The flushed draft may not have rendered yet, so the request binds to the
  // draft's own fingerprint and the session's current revision and context.
  const beginValidation = useCallback((draft: ResolvedScriptDraft, stored: boolean): ValidationRequest => {
    latestRequestRef.current += 1
    setValidatedBinding(null)
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
  const adoptValidated = useCallback((request: ValidationRequest, adoptedDocumentKey: string) => {
    setValidatedBinding({ ...request.sent, fingerprint: draftFingerprint(adoptedDocumentKey) })
  }, [])

  const isLatestValidation = useCallback((request: ValidationRequest) => request.requestId === latestRequestRef.current, [])

  // validationRequestIsCurrent: the latest request, for the document current now.
  const validationRequestIsCurrent = useCallback((request: ValidationRequest) => (
    validationRequestStillCurrent(latestRequestRef.current, request, currentBindingRef.current)
  ), [])

  // invalidateValidation drops every in-flight request and the current result.
  const invalidateValidation = useCallback(() => {
    latestRequestRef.current += 1
    setValidatedBinding(null)
  }, [])

  const clearValidatedBinding = useCallback(() => setValidatedBinding(null), [])

  return {
    currentBinding,
    validatedBinding,
    validationIsCurrent: sameValidationBinding(validatedBinding, currentBinding),
    resolveDraft,
    beginValidation,
    admitValidation,
    adoptValidated,
    isLatestValidation,
    validationRequestIsCurrent,
    invalidateValidation,
    clearValidatedBinding,
  }
}
