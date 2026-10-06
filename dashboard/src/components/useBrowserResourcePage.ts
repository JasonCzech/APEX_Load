import { useEffect, useRef, useState } from 'react'
import type { BrowserResourcePage, BrowserResourcePageQuery } from '../types/browserResourcePage'
import { decodeBrowserResourcePage } from './browserResourcePageModel'

export type BrowserResourcePageLoader = (testID: string, projectID: string, query: BrowserResourcePageQuery, signal: AbortSignal) => Promise<unknown>
interface Props { testID: string; projectID: string; sourceIdentity: string; query: BrowserResourcePageQuery; loadPage: BrowserResourcePageLoader; refreshRevision?: number }
interface State { generation: number; page: BrowserResourcePage | null; loading: boolean; error: string }

/** Ownership changes retire the response synchronously, including A→B→A
 * navigation. Cancellation is an additional transport safeguard, not proof
 * that an old promise cannot resolve. StrictMode may restart the same request. */
export function useBrowserResourcePage({ testID, projectID, sourceIdentity, query, loadPage, refreshRevision = 0 }: Props) {
  const queryIdentity = JSON.stringify(query)
  const scope = JSON.stringify([testID, projectID, sourceIdentity, queryIdentity, refreshRevision])
  const owner = useRef({ scope, generation: 1 })
  if (owner.current.scope !== scope) owner.current = { scope, generation: owner.current.generation + 1 }
  const generation = owner.current.generation
  const [state, setState] = useState<State>({ generation: 0, page: null, loading: false, error: '' })
  useEffect(() => {
    if (!testID || !projectID || !sourceIdentity) return
    const abort = new AbortController()
    let live = true
    const current = () => live && !abort.signal.aborted && owner.current.generation === generation
    setState({ generation, page: null, loading: true, error: '' })
    void (async () => {
      try {
        const raw = await loadPage(testID, projectID, JSON.parse(queryIdentity) as BrowserResourcePageQuery, abort.signal)
        if (!current()) return
        const page = decodeBrowserResourcePage(raw, { testID, projectID })
        // The canonical request remains private even if a custom loader mutates its copy.
        const requested = JSON.parse(queryIdentity) as BrowserResourcePageQuery
        if (page.items.length > requested.limit) throw new Error('Archive resource page exceeds requested size')
        if (current()) setState({ generation, page, loading: false, error: '' })
      } catch {
        if (current()) setState({ generation, page: null, loading: false,
          error: 'Unable to load retained archive resources. The archive may have changed, expired, or exceeded its read limits. Try the first page.' })
      }
    })()
    return () => { live = false; abort.abort() }
  }, [generation, loadPage, projectID, queryIdentity, refreshRevision, sourceIdentity, testID])
  return state.generation === generation && testID && projectID && sourceIdentity
    ? state : { generation, page: null, loading: Boolean(testID && projectID && sourceIdentity), error: '' }
}
