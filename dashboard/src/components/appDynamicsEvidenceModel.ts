import type { AnalysisPoint } from '../types/analysis'

export function safeAppDynamicsEvidenceLink(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const url = new URL(value)
    if (url.protocol !== 'https:' || url.username || url.password) return undefined
    return url.href
  } catch { return undefined }
}

export async function readBoundedMonitorArchive(response: Response): Promise<AnalysisPoint[]> {
  if (!response.ok) throw new Error(`Monitoring artifact unavailable (${response.status}).`)
  const limit = 8 * 1024 * 1024
  if (Number(response.headers.get('content-length')) > limit) throw new Error('Monitoring artifact exceeds the 8 MiB preview limit. Download the complete evidence for analysis.')
  const reader = response.body?.getReader()
  if (!reader) throw new Error('Monitoring artifact streaming is unavailable.')
  const decoder = new TextDecoder()
  let bytes = 0; let text = ''
  try {
    while (true) {
      const chunk = await reader.read()
      if (chunk.done) break
      bytes += chunk.value.byteLength
      if (bytes > limit) throw new Error('Monitoring artifact exceeds the 8 MiB preview limit. Download the complete evidence for analysis.')
      text += decoder.decode(chunk.value, { stream: true })
    }
    text += decoder.decode()
  } finally { await reader.cancel() }
  const rows = text.split('\n').filter(line => line.trim()).map(line => JSON.parse(line) as AnalysisPoint)
  const points = rows.filter(row => row.provider?.name === 'appdynamics')
  if (points.length > 10000) throw new Error('Monitoring artifact exceeds the 10,000 point preview limit. Download the complete evidence for analysis.')
  return points
}
