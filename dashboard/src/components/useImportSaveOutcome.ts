import { useRef, useState } from 'react'
import { type ImportedSaveResult } from './useScriptsAuthoringState'

export type ImportSaveDestination = 'stay' | 'editor'

/** Keep durable save notification separate from the user's navigation choice. */
export function useImportSaveOutcome(scope: unknown, onImported?: (result: ImportedSaveResult) => void) {
  const [completed, setCompleted] = useState<{ scope: unknown; result: ImportedSaveResult } | null>(null)
  const currentScope = useRef(scope)
  currentScope.current = scope
  const savedResult = completed && completed.scope === scope ? completed.result : null
  const publish = (result: ImportedSaveResult, destination: ImportSaveDestination = 'stay') => {
    const stale = currentScope.current !== scope
    if (!stale && result.scripts.length > 0) setCompleted({ scope, result })
    onImported?.({
      ...result,
      ...(stale || destination === 'stay' || result.scripts.length > 1 ? { stayInImportReview: true } : {}),
    })
  }
  const openSaved = (result: ImportedSaveResult) => onImported?.({ ...result, stayInImportReview: false })
  return { savedResult, publish, openSaved }
}
