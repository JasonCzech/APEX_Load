import { useCallback, useEffect, useState } from 'react'

import { useProjectApi } from '../hooks/api/useProjectApi'
import type { SharedAssetUsage, SharedAssetUsageKind } from '../types/sharedAssets'
import { getErrorMessage } from '../utils/errorUtils'

/**
 * What uses each of a project's assets of one kind, read once per project and
 * again on refresh(), after a change that can alter it. The previous answer
 * stays while a refresh is in flight; another project's is never returned.
 * error says when what uses them could not be checked, so "nothing" is never
 * implied by a failed read.
 */
export function useSharedAssetUsage(projectId: string | undefined, kind: SharedAssetUsageKind) {
  const api = useProjectApi()
  const [usage, setUsage] = useState<SharedAssetUsage | null>(null)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  useEffect(() => {
    setError('')
    if (!projectId) return
    const abort = new AbortController()
    api.getSharedAssetUsage(projectId, [kind], { signal: abort.signal })
      .then(next => { if (!abort.signal.aborted) setUsage(next) })
      .catch((cause: unknown) => { if (!abort.signal.aborted) setError(`Could not check what uses these. ${getErrorMessage(cause, '')}`.trim()) })
    return () => abort.abort()
  }, [api, kind, projectId, version])
  const refresh = useCallback(() => setVersion(current => current + 1), [])
  const current = projectId && usage?.project_id === projectId ? usage : null
  return { usage: current, error, refresh }
}
