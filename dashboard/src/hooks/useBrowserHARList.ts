import { useCallback, useEffect, useState } from 'react'
import { listArchiveBrowserHARs, type BrowserHARArchiveList } from './api/useBrowserHARApi'

/** Reads a run's HAR list once per run, and again on reload. */
export function useBrowserHARList(testID: string, loadList: typeof listArchiveBrowserHARs = listArchiveBrowserHARs) {
  const [list, setList] = useState<BrowserHARArchiveList | null>(null)
  const [error, setError] = useState('')
  const [archiveMissing, setArchiveMissing] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setList(null); setError(''); setArchiveMissing(false)
    if (!testID) return () => controller.abort()
    loadList(testID, controller.signal).then(setList, (reason: unknown) => {
      if (controller.signal.aborted) return
      const message = reason instanceof Error ? reason.message : 'Browser HARs could not be listed.'
      // A run whose saved archive is not available (yet: an HA controller
      // answers 404 until it is published) is a state, not a failure, so it
      // is said plainly with a reload instead of echoing the server.
      if ((reason as { status?: unknown } | null)?.status === 404 && /archive not found/i.test(message)) setArchiveMissing(true)
      else setError(message)
    })
    return () => controller.abort()
  }, [testID, loadList, attempt])
  const reload = useCallback(() => setAttempt(value => value + 1), [])
  return { list, error, archiveMissing, reload }
}
