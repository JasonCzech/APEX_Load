import type { ElasticRecord } from '../types/elastic'

export function elasticEvidenceTimestampKey(value: string): string {
  const fraction = value.match(/\.(\d+)Z$/)?.[1] || ''
  return `${value.slice(0, 19)}.${fraction.padEnd(9, '0')}`
}

export function elasticEvidenceTimeError(from: string, until: string): string {
  for (const value of [from, until]) {
    if (!value) continue
    const timestamp = Date.parse(value)
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) || !Number.isFinite(timestamp) || new Date(timestamp).toISOString().slice(0, 19) !== value.slice(0, 19) || value.startsWith('0000-')) return 'Evidence times must be valid UTC RFC3339 timestamps ending in Z.'
  }
  if (from && until && elasticEvidenceTimestampKey(until) <= elasticEvidenceTimestampKey(from)) return 'Evidence until must be after from.'
  return ''
}

export async function readBoundedElasticArchive(response: Response): Promise<ElasticRecord[]> {
  if (!response.ok) throw new Error(`Monitoring record artifact unavailable (${response.status}).`)
  const reader = response.body?.getReader()
  if (!reader) throw new Error('Monitoring artifact streaming is unavailable.')
  const decoder = new TextDecoder(); let bytes = 0; let content = ''
  try { while (true) { const chunk = await reader.read(); if (chunk.done) break; bytes += chunk.value.byteLength; if (bytes > 8 * 1024 * 1024) throw new Error('Record artifact exceeds the 8 MiB preview limit. Download the complete retained projection.'); content += decoder.decode(chunk.value, { stream: true }) } content += decoder.decode() } finally { await reader.cancel() }
  const records = content.split('\n').filter(line => line.trim()).map(line => JSON.parse(line) as { source_id: string; diagnostic: ElasticRecord }).filter(row => row.diagnostic?.provider === 'elasticsearch' || row.diagnostic?.provider === 'opensearch').map(row => ({ ...row.diagnostic, source_id: row.source_id }))
  if (records.length > 10000) throw new Error('Record artifact exceeds the 10,000 record preview limit. Download the complete retained projection.')
  return records
}
