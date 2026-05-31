import { Suspense, lazy, useEffect, useMemo, useState } from 'react'

import { type LoadRunnerSourceReview, type LoadRunnerSourceReviewFile, type LoadRunnerSourceReviewMapping, type ScriptDefinition } from '../types/scripts'
import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'
import {
  computeCoverage,
  dslLineStatus,
  lineStatusForSource,
  mappingForDslLine,
  mappingForSourceLine,
  mappingMatchesFile,
  statusCategory,
} from './sourceMappingModel'
import './SourceMappingReview.css'

// CodeMirror is only needed inside this review surface, so keep it out of the
// main bundle.
const LinkedCodePane = lazy(() => import('./LinkedCodePane'))

type SortField = 'source' | 'function' | 'target' | 'status'
type SortDirection = 'asc' | 'desc'

interface Props {
  sourceReview?: LoadRunnerSourceReview | null
  script?: ScriptDefinition | null
  title?: string
}

// CSS class names for category chips/gutters (reuses the existing status styles).
const CATEGORY_CLASS: Record<string, string> = {
  converted: 'converted',
  preserved: 'preserved',
  review: 'review_only',
  unsupported: 'unsupported',
}

function normalizeMappings(sourceReview?: LoadRunnerSourceReview | null): LoadRunnerSourceReviewMapping[] {
  return (sourceReview?.mappings || []).filter(mapping => mapping && mapping.id)
}

function normalizeFiles(sourceReview?: LoadRunnerSourceReview | null): LoadRunnerSourceReviewFile[] {
  return (sourceReview?.source_files || []).filter(file => file && file.path)
}

function fileMatchesPath(file: LoadRunnerSourceReviewFile, path?: string) {
  if (!path) return false
  const filePath = file.path.toLowerCase()
  const requested = path.toLowerCase()
  return filePath === requested || filePath.endsWith(`/${requested}`) || filePath.split('/').pop() === requested.split('/').pop()
}

function findSourceFile(files: LoadRunnerSourceReviewFile[], path?: string) {
  return files.find(file => fileMatchesPath(file, path)) || files[0] || null
}

function findFirstMappingForFile(mappings: LoadRunnerSourceReviewMapping[], filePath?: string) {
  return mappings.find(mapping => mappingMatchesFile(mapping, filePath)) || null
}

function compareText(a: string, b: string, direction: SortDirection) {
  const result = a.localeCompare(b)
  return direction === 'asc' ? result : -result
}

function mappingSortValue(mapping: LoadRunnerSourceReviewMapping, field: SortField) {
  switch (field) {
    case 'function':
      return mapping.function || ''
    case 'target':
      return mapping.target_path || mapping.target_name || ''
    case 'status':
      return `${mapping.status || ''}:${mapping.support_mode || ''}`
    default:
      return `${mapping.source_file || ''}:${String(mapping.start_line || 0).padStart(8, '0')}`
  }
}

function formatStatusLabel(status?: string) {
  return (status || 'review').replace(/_/g, ' ')
}

function formatSupportModeLabel(mode?: string) {
  return (mode || '').replace(/_/g, ' ')
}

function formatSourceRange(mapping?: LoadRunnerSourceReviewMapping | null) {
  if (!mapping) return 'No mapping selected'
  if (!mapping.start_line) return mapping.source_file || 'Source'
  if (mapping.end_line && mapping.end_line !== mapping.start_line) {
    return `${mapping.source_file}:${mapping.start_line}-${mapping.end_line}`
  }
  return `${mapping.source_file}:${mapping.start_line}`
}

function excerptFromText(text: string, startLine?: number, endLine?: number) {
  if (!text || !startLine) return ''
  const lines = text.split('\n')
  const start = Math.max(1, startLine)
  const end = Math.max(start, endLine || startLine)
  return lines.slice(start - 1, end).join('\n')
}

function PaneFallback() {
  return <div className="source-map-empty">Loading code view…</div>
}

export default function SourceMappingReview({ sourceReview, script, title = 'Source Mapping' }: Props) {
  const files = useMemo(() => normalizeFiles(sourceReview), [sourceReview])
  const mappings = useMemo(() => normalizeMappings(sourceReview), [sourceReview])
  const [selectedMappingId, setSelectedMappingId] = useState('')
  const [selectedFilePath, setSelectedFilePath] = useState('')
  const [filter, setFilter] = useState('')
  const [sortField, setSortField] = useState<SortField>('source')
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc')
  const [copied, setCopied] = useState<'source' | 'dsl' | ''>('')

  const selectedMapping = useMemo(
    () => mappings.find(mapping => mapping.id === selectedMappingId) || null,
    [mappings, selectedMappingId],
  )
  const selectedFile = useMemo(
    () => findSourceFile(files, selectedFilePath || selectedMapping?.source_file),
    [files, selectedFilePath, selectedMapping?.source_file],
  )

  const dslDocument = useMemo(() => (script ? buildDslDocument(script) : null), [script])
  const dslStatus = useMemo(
    () => (dslDocument ? dslLineStatus(dslDocument.ranges, mappings) : { lines: new Map<number, never>(), withoutOrigin: [] }),
    [dslDocument, mappings],
  )
  const coverage = useMemo(
    () => computeCoverage(mappings, dslStatus.withoutOrigin.length),
    [mappings, dslStatus.withoutOrigin.length],
  )
  const sourceGutter = useMemo(() => lineStatusForSource(mappings, selectedFile?.path), [mappings, selectedFile])

  useEffect(() => {
    const nextMapping = mappings[0]
    setSelectedMappingId(nextMapping?.id || '')
    setSelectedFilePath(nextMapping?.source_file || files[0]?.path || '')
    setCopied('')
  }, [files, mappings])

  const filteredMappings = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    const next = needle
      ? mappings.filter(mapping => [
        mapping.source_file,
        mapping.function,
        mapping.target_path,
        mapping.target_name,
        mapping.status,
        mapping.support_mode,
        mapping.notes,
      ].some(value => String(value || '').toLowerCase().includes(needle)))
      : mappings
    return [...next].sort((a, b) => compareText(mappingSortValue(a, sortField), mappingSortValue(b, sortField), sortDirection))
  }, [filter, mappings, sortDirection, sortField])

  if (files.length === 0 && mappings.length === 0) {
    return null
  }

  const selectMapping = (mapping: LoadRunnerSourceReviewMapping | null) => {
    if (!mapping) return
    setSelectedMappingId(mapping.id)
    if (mapping.source_file) setSelectedFilePath(mapping.source_file)
    setCopied('')
  }

  const selectSourceFile = (filePath: string) => {
    const nextMapping = findFirstMappingForFile(mappings, filePath)
    setSelectedFilePath(filePath)
    setSelectedMappingId(nextMapping?.id || '')
    setCopied('')
  }

  const sourceActiveRange = selectedMapping && selectedFile && mappingMatchesFile(selectedMapping, selectedFile.path)
    ? { startLine: selectedMapping.start_line, endLine: selectedMapping.end_line || selectedMapping.start_line }
    : null
  const dslActiveRange = dslDocument ? lineRangeForTargetPath(dslDocument.ranges, selectedMapping?.target_path) : null

  const sourceExcerpt = excerptFromText(selectedFile?.content || '', sourceActiveRange?.startLine, sourceActiveRange?.endLine)
  const dslExcerpt = excerptFromText(dslDocument?.text || '', dslActiveRange?.startLine, dslActiveRange?.endLine)
  const copySupported = canCopyTextToClipboard()

  const handleSort = (field: SortField) => {
    if (field === sortField) {
      setSortDirection(current => current === 'asc' ? 'desc' : 'asc')
      return
    }
    setSortField(field)
    setSortDirection('asc')
  }

  const copyValue = async (kind: 'source' | 'dsl', value: string) => {
    if (!value || !copySupported) return
    const ok = await copyTextToClipboard(value)
    setCopied(ok ? kind : '')
  }

  const handleSourceLineClick = (line: number) => {
    selectMapping(mappingForSourceLine(mappings, selectedFile?.path, line))
  }
  const handleDslLineClick = (line: number) => {
    if (!dslDocument) return
    selectMapping(mappingForDslLine(dslDocument.ranges, mappings, line))
  }

  const coverageChips: Array<{ key: string; label: string; value: number }> = [
    { key: 'converted', label: 'Converted', value: coverage.converted },
    { key: 'preserved', label: 'Preserved', value: coverage.preserved },
    { key: 'review', label: 'Needs review', value: coverage.review },
    { key: 'unsupported', label: 'Unsupported', value: coverage.unsupported },
  ]

  return (
    <section className="source-map-review">
      <div className="source-map-header">
        <div>
          <h4>{title}</h4>
          <p>Validate the conversion line by line — select on either side and the match is highlighted on the other.</p>
        </div>
        <div className="source-map-summary">
          <span>{files.length} file{files.length === 1 ? '' : 's'}</span>
          <span>{mappings.length} mapping{mappings.length === 1 ? '' : 's'}</span>
        </div>
      </div>

      <div className="source-map-coverage" role="group" aria-label="Conversion coverage">
        {coverageChips.map(chip => (
          <span key={chip.key} className={`source-map-status ${CATEGORY_CLASS[chip.key]}`}>
            {chip.label}: {chip.value}
          </span>
        ))}
        {dslDocument && (
          <span
            className={`source-map-status ${coverage.dslWithoutOrigin > 0 ? 'unsupported' : 'converted'}`}
            title="DSL elements (actions, parameters, data sources) with no traceable source origin"
          >
            DSL without source origin: {coverage.dslWithoutOrigin}
          </span>
        )}
      </div>

      <div className="source-map-toolbar">
        <label>
          <span>Source File</span>
          <select value={selectedFile?.path || ''} onChange={event => selectSourceFile(event.target.value)}>
            {files.map(file => (
              <option key={file.path} value={file.path}>
                {file.path}{file.truncated ? ' (truncated)' : ''}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Filter Mappings</span>
          <input value={filter} onChange={event => setFilter(event.target.value)} placeholder="Function, target, status..." />
        </label>
      </div>

      <div className="source-map-table-wrap">
        <table className="source-map-table">
          <thead>
            <tr>
              <th><button type="button" onClick={() => handleSort('source')}>Source</button></th>
              <th><button type="button" onClick={() => handleSort('function')}>Function</button></th>
              <th><button type="button" onClick={() => handleSort('target')}>Generated DSL</button></th>
              <th><button type="button" onClick={() => handleSort('status')}>Status</button></th>
            </tr>
          </thead>
          <tbody>
            {filteredMappings.map(mapping => (
              <tr
                key={mapping.id}
                className={mapping.id === selectedMapping?.id ? 'active' : ''}
                onClick={() => selectMapping(mapping)}
              >
                <td className="cell-mono">{formatSourceRange(mapping)}</td>
                <td>{mapping.function || '-'}</td>
                <td className="cell-mono">{mapping.target_path || mapping.target_name || 'review-only'}</td>
                <td>
                  <div className="source-map-status-stack">
                    <span className={`source-map-status ${CATEGORY_CLASS[statusCategory(mapping.status)]}`}>{formatStatusLabel(mapping.status)}</span>
                    {mapping.support_mode && <span className="source-map-support-mode">{formatSupportModeLabel(mapping.support_mode)}</span>}
                  </div>
                </td>
              </tr>
            ))}
            {filteredMappings.length === 0 && (
              <tr>
                <td colSpan={4} className="source-map-empty">No source mappings match this filter.</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="source-map-panels">
        <div className="source-map-panel">
          <div className="source-map-panel-header">
            <strong>{selectedFile?.path || 'Source'}</strong>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('source', sourceExcerpt)} disabled={!copySupported || !sourceExcerpt}>
              {copied === 'source' ? 'Copied' : 'Copy Source'}
            </button>
          </div>
          {selectedFile ? (
            <Suspense fallback={<PaneFallback />}>
              <LinkedCodePane
                value={selectedFile.content}
                language={selectedFile.language}
                gutterStatuses={sourceGutter}
                activeRange={sourceActiveRange}
                onLineClick={handleSourceLineClick}
                ariaLabel={`${selectedFile.path} source`}
              />
            </Suspense>
          ) : (
            <div className="source-map-empty">No source file content was captured.</div>
          )}
        </div>

        <div className="source-map-panel">
          <div className="source-map-panel-header">
            <strong>{selectedMapping?.target_path || 'Generated DSL'}</strong>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('dsl', dslExcerpt)} disabled={!copySupported || !dslExcerpt}>
              {copied === 'dsl' ? 'Copied' : 'Copy DSL'}
            </button>
          </div>
          {dslDocument ? (
            <Suspense fallback={<PaneFallback />}>
              <LinkedCodePane
                value={dslDocument.text}
                language="json"
                gutterStatuses={dslStatus.lines}
                activeRange={dslActiveRange}
                onLineClick={handleDslLineClick}
                ariaLabel="Generated DSL"
              />
            </Suspense>
          ) : (
            <div className="source-map-empty">DSL has not been generated yet. Complete the import to compare the native script.</div>
          )}
        </div>
      </div>
    </section>
  )
}
