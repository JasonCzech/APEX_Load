import type { BrowserResourcePage, BrowserResourcePageItem } from '../types/browserResourcePage'
import { browserResourceEvidenceView } from './browserResourceEvidenceModel'

const pageFields = ['version', 'test_id', 'project_id', 'source_format', 'source_revision', 'source_integrity',
  'source_timestamp_precision', 'population', 'capture_coverage', 'items', 'next_cursor']
const requiredPageFields = pageFields.filter(key => key !== 'next_cursor')
const itemFields = ['locator', 'timestamp', 'timestamp_precision', 'action_name', 'group', 'region', 'page_url', 'url',
  'method', 'status_code', 'resource_type', 'failure_kind', 'duration_ms', 'bytes_received', 'device_profile',
  'device_platform', 'browser_engine', 'evidence_state', 'browser_evidence']
const precision = ['rfc3339_nanosecond', 'unix_millisecond', 'unavailable']
function object(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === 'object' && !Array.isArray(value) }
function owns(value: Record<string, unknown>, key: string): boolean { return Object.prototype.hasOwnProperty.call(value, key) }
function keys(value: Record<string, unknown>, allowed: string[], required: string[] = []): boolean {
  return Object.keys(value).every(key => allowed.includes(key)) && required.every(key => owns(value, key))
}
function text(value: unknown, max: number, nonempty = false): value is string {
  return typeof value === 'string' && (!nonempty || value.length > 0) && new TextEncoder().encode(value).length <= max
    && !value.includes('\0') && !/[\r\n\uD800-\uDFFF]/u.test(value)
}
function integer(value: unknown, max: number, min = 0): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max }
function utc(value: unknown): boolean {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value)
    || /^0001-01-01T00:00:00(?:\.0{1,9})?Z$/.test(value)) return false
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) && new Date(parsed).toISOString().slice(0, 19) === value.slice(0, 19)
}
function itemValid(item: unknown, transactions: number): item is BrowserResourcePageItem {
  if (!object(item) || !keys(item, itemFields, ['locator', 'timestamp_precision', 'evidence_state'])
    || !object(item.locator) || !keys(item.locator, ['transaction_ordinal', 'resource_ordinal', 'branch'], ['transaction_ordinal', 'resource_ordinal', 'branch'])
    || !integer(item.locator.transaction_ordinal, transactions, 1) || !integer(item.locator.resource_ordinal, 4096, 1)
    || !['network_entries', 'snapshot_network_entries'].includes(item.locator.branch as string)
    || typeof item.timestamp_precision !== 'string' || !precision.includes(item.timestamp_precision)) return false
  if (owns(item, 'timestamp') && item.timestamp !== null ? !utc(item.timestamp) || item.timestamp_precision === 'unavailable' : item.timestamp_precision !== 'unavailable') return false
  for (const [key, max] of [['action_name', 256], ['group', 256], ['region', 256], ['page_url', 2048], ['url', 2048], ['method', 32],
    ['resource_type', 64], ['device_profile', 128], ['device_platform', 32], ['browser_engine', 32]] as const) {
    if (owns(item, key) && !text(item[key], max)) return false
  }
  if (owns(item, 'status_code') && item.status_code !== null && !integer(item.status_code, 999) || owns(item, 'bytes_received') && item.bytes_received !== null && !integer(item.bytes_received, 2 ** 50)
    || owns(item, 'duration_ms') && item.duration_ms !== null && (typeof item.duration_ms !== 'number' || !Number.isFinite(item.duration_ms) || item.duration_ms < 0 || item.duration_ms > 86400000)
    || owns(item, 'failure_kind') && (typeof item.failure_kind !== 'string' || !['', 'http', 'transport', 'timeout', 'canceled', 'body_limit'].includes(item.failure_kind))) return false
  if (item.evidence_state === 'available') return owns(item, 'browser_evidence') && browserResourceEvidenceView(item.browser_evidence).evidence !== null
  return typeof item.evidence_state === 'string' && ['not_captured', 'malformed', 'unsupported_version'].includes(item.evidence_state) && (!owns(item, 'browser_evidence') || item.browser_evidence === null)
}
/** Closed bounded page admission. Raw duplicate-key rejection belongs to the
 * server/CLI wire decoder; this guard validates the parsed browser projection. */
export function decodeBrowserResourcePage(value: unknown, expected: { testID: string; projectID: string }): BrowserResourcePage {
  const invalid = () => { throw new Error('The archive resource response is invalid or does not match this run.') }
  if (!object(value) || !keys(value, pageFields, requiredPageFields) || value.version !== 1
    || !text(value.test_id, 128, true) || value.test_id !== expected.testID || !text(value.project_id, 128, true) || value.project_id !== expected.projectID
    || !['jsonl', 'parquet'].includes(value.source_format as string) || typeof value.source_revision !== 'string' || !/^[a-f0-9]{64}$/.test(value.source_revision)
    || !['verified_manifest_and_artifact', 'legacy_scan_fingerprint'].includes(value.source_integrity as string)
    || typeof value.source_timestamp_precision !== 'string' || ![...precision, 'mixed'].includes(value.source_timestamp_precision)
    || !object(value.capture_coverage) || !keys(value.capture_coverage, ['availability', 'reason'], ['availability', 'reason'])
    || value.capture_coverage.availability !== 'unavailable' || value.capture_coverage.reason !== 'retained_observations_are_not_capture_coverage'
    || !object(value.population) || !keys(value.population, ['availability', 'transaction_records', 'retained_entry_observations', 'matched_retained_observations'], ['availability', 'transaction_records', 'retained_entry_observations', 'matched_retained_observations'])
    || !integer(value.population.transaction_records, 2_000_000) || !Array.isArray(value.items) || value.items.length > 100) return invalid()
  if (owns(value, 'next_cursor') && (!text(value.next_cursor, 4096, true) || !/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(value.next_cursor))) return invalid()
  const p = value.population
  const transactionRecords = p.transaction_records as number // bounded above before the alias
  if (p.availability === 'unavailable') {
    if (p.retained_entry_observations !== null || p.matched_retained_observations !== null || value.items.length !== 0 || owns(value, 'next_cursor')) return invalid()
  } else if (p.availability !== 'retained_observations' || !integer(p.retained_entry_observations, 2_000_000)
    || !integer(p.matched_retained_observations, p.retained_entry_observations) || value.items.length > p.matched_retained_observations) return invalid()
  const locators = new Set<string>()
  for (const item of value.items) {
    if (!itemValid(item, transactionRecords)) return invalid()
    const key = JSON.stringify([item.locator.transaction_ordinal, item.locator.resource_ordinal, item.locator.branch])
    if (locators.has(key)) return invalid()
    locators.add(key)
  }
  const page = structuredClone(value) as unknown as BrowserResourcePage
  // Optional pointer nulls carry absence, as in the Go wire projection. The
  // separately required population and availability fields keep their meaning.
  for (const item of page.items) for (const key of ['timestamp', 'status_code', 'duration_ms', 'bytes_received', 'browser_evidence'] as const) {
    if ((item as unknown as Record<string, unknown>)[key] === null) delete item[key]
  }
  return page
}
