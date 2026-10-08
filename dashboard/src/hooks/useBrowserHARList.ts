import { useEffect, useState } from 'react'
import { listArchiveBrowserHARs, type BrowserHARArchiveList } from './api/useBrowserHARApi'

/** Reads a run's HAR list once per run. */
export function useBrowserHARList(testID: string, loadList: typeof listArchiveBrowserHARs = listArchiveBrowserHARs) {
  const [list, setList] = useState<BrowserHARArchiveList | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setList(null); setError('')
    if (!testID) return () => controller.abort()
    loadList(testID, controller.signal).then(setList, (reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Browser HARs could not be listed.')
    })
    return () => controller.abort()
  }, [testID, loadList])
  return { list, error }
}
