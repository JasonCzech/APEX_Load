import { useEffect, useState } from 'react'

import { request } from '../hooks/apiClient'
import { type SourceReviewFile, type SourceReviewText } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'

// A review keeps a bounded preview of each source file. A truncated file that
// a mapping opens also names its complete text by hash, which loads on demand
// so the source pane can show and highlight any mapped line.

export type SourceReviewFullText =
  | { status: 'none' }
  | { status: 'loading' }
  | { status: 'ready'; content: string; lines: number }
  | { status: 'unavailable'; message: string }

// The texts are large (up to 16 MiB each), so keep only the most recent few.
const MAX_CACHED_TEXTS = 4
const MAX_TEXT_RESPONSE_BYTES = 40 << 20
const cachedTexts = new Map<string, SourceReviewText>()

function rememberText(key: string, text: SourceReviewText) {
  cachedTexts.delete(key)
  cachedTexts.set(key, text)
  while (cachedTexts.size > MAX_CACHED_TEXTS) {
    const oldest = cachedTexts.keys().next().value
    if (oldest === undefined) break
    cachedTexts.delete(oldest)
  }
}

/** Forget cached texts (tests). */
export function clearSourceReviewFullTextCache() {
  cachedTexts.clear()
}

export function getSourceReviewText(projectId: string, sha256: string, signal?: AbortSignal) {
  return request<SourceReviewText>(
    `/projects/${encodeURIComponent(projectId)}/source-review/texts/${encodeURIComponent(sha256)}`,
    { signal, maxResponseBytes: MAX_TEXT_RESPONSE_BYTES },
  )
}

function fullTextKey(projectId: string | undefined, file: SourceReviewFile | null | undefined) {
  const sha256 = file?.truncated ? file.full_sha256?.trim() : ''
  return projectId?.trim() && sha256 ? `${projectId.trim()}:${sha256}` : ''
}

/**
 * The complete text of a truncated review file, loaded when the file is
 * selected. A file with no stored text, or a review outside a project,
 * reports "none" and the pane keeps the preview.
 */
export function useSourceReviewFullText(projectId: string | undefined, file: SourceReviewFile | null | undefined): SourceReviewFullText {
  const key = fullTextKey(projectId, file)
  const sha256 = file?.full_sha256?.trim() || ''
  const [result, setResult] = useState<{ key: string; value: SourceReviewFullText } | null>(null)

  useEffect(() => {
    if (!key || cachedTexts.has(key)) return
    const controller = new AbortController()
    getSourceReviewText(projectId!.trim(), sha256, controller.signal)
      .then(text => {
        if (controller.signal.aborted) return
        if (text?.sha256 !== sha256 || typeof text.content !== 'string') {
          setResult({ key, value: { status: 'unavailable', message: 'The controller returned a different file.' } })
          return
        }
        rememberText(key, text)
        setResult({ key, value: { status: 'ready', content: text.content, lines: text.lines } })
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        setResult({ key, value: { status: 'unavailable', message: getErrorMessage(error, 'The full file could not be loaded.') } })
      })
    return () => controller.abort()
  }, [key, projectId, sha256])

  if (!key) return { status: 'none' }
  const cached = cachedTexts.get(key)
  if (cached) return { status: 'ready', content: cached.content, lines: cached.lines }
  if (result?.key === key) return result.value
  return { status: 'loading' }
}
