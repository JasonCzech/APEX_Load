import { fingerprintPortableContent as fingerprintContent } from '../utils/contentFingerprint'
import { readSourceReview } from './scriptEditorImportModel'
import { scriptHasSourceOnlyReview, sourceReviewIsSourceOnly } from './sourceReviewDomain'
import { useEffect, useState } from 'react'
import type { ScriptDefinition, SourceReview } from '../types/scripts'
import { createMappingReviewState, readMappingReviewState, resolveMappingBindings, type MappingReviewState, type ResolvedMapping } from './sourceMappingReviewState'

const EMPTY_RESOLUTIONS: Record<string, ResolvedMapping> = {}

export function useMappingReviewState(script: ScriptDefinition | null | undefined, baseline: ScriptDefinition | null | undefined, review: SourceReview | null | undefined) {
  const [result, setResult] = useState<{ script: ScriptDefinition; review: SourceReview; state: MappingReviewState; resolved: Record<string, ResolvedMapping> } | null>(null)
  const [error, setError] = useState('')
  const enabled = Boolean(script && review && !sourceReviewIsSourceOnly(review) && !scriptHasSourceOnlyReview(script))
  useEffect(() => {
    let active = true
    if (!enabled || !script || !review) return
    setError('')
    void (async () => {
      let captured = await createMappingReviewState(baseline || script, review)
      const baselineReview = readSourceReview(baseline?.metadata?.import)
      if (baseline && baseline !== script && (!baselineReview || await fingerprintContent(baselineReview) !== captured.importRevision)) {
        captured = { ...captured, bindings: {}, decisions: {}, snapshot: undefined }
      }
      const stored = readMappingReviewState(script)
      const state = stored?.importRevision === captured.importRevision ? stored : captured
      const resolved = await resolveMappingBindings(script, review.mappings || [], state)
      if (active) setResult({ script, review, state, resolved })
    })().catch(cause => { if (active) setError(cause instanceof Error ? cause.message : 'Unable to verify mapping identity.') })
    return () => { active = false }
  }, [baseline, enabled, script, review])
  // Never show old resolutions against a newer draft while verification runs.
  if (!enabled) return { state: undefined, resolved: EMPTY_RESOLUTIONS, checking: false, error: '' }
  return { state: result && result.script === script && result.review === review ? result.state : undefined, resolved: result && result.script === script && result.review === review ? result.resolved : EMPTY_RESOLUTIONS, checking: !result || result.script !== script || result.review !== review, error }
}
