import type { RunArchiveReport, TestSummary } from '../types'

export interface ArchiveResourceInspectorReadScope { hasArchive: boolean; test: TestSummary; report: RunArchiveReport | null }
function scope(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && new TextEncoder().encode(value).length <= 128 && !/[\r\n\uD800-\uDFFF]/u.test(value) && !value.includes('\0')
}
/** The read owner comes from the selected canonical run and a paired report.
 * Report metadata only retires local reads; the server reopens and authenticates
 * the durable source on every page. No metadata here grants archive authority. */
export function archiveResourceInspectorOwner({ hasArchive, test, report }: ArchiveResourceInspectorReadScope) {
  if (!hasArchive || !scope(test.id) || !scope(test.project_id) || !report?.manifest || report.manifest.test_id !== test.id
    || report.manifest.provenance?.project_id && report.manifest.provenance.project_id !== test.project_id
    || test.archive?.test_id && test.archive.test_id !== test.id) return null
  return { testID: test.id, projectID: test.project_id, sourceIdentity: JSON.stringify([
    test.id, test.project_id, test.started_at, test.archive?.created_at, test.archive?.completed_at, test.archive?.archive_path, test.archive?.manifest_path,
    report.manifest.created_at, report.manifest.provenance?.requested_at, report.manifest.provenance?.started_at,
    report.manifest.transactions_file, report.manifest.transactions_parquet_file,
    report.manifest.artifacts?.map(artifact => [artifact.name, artifact.sha256, artifact.size_bytes, artifact.count]),
  ]) }
}
