import { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react'

import { type ScriptDefinition, type SourceReview, type SourceReviewFile, type SourceReviewMapping } from '../types/scripts'
import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'
import {
  type MappingCategory,
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
  focusRequestId?: number
  focusedMappingId?: string
  id?: string
  sourceReview?: SourceReview | null
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

function normalizeMappings(sourceReview?: SourceReview | null): SourceReviewMapping[] {
  return (sourceReview?.mappings || []).filter(mapping => mapping && mapping.id)
}

function normalizeFiles(sourceReview?: SourceReview | null): SourceReviewFile[] {
  return (sourceReview?.source_files || []).filter(file => file && file.path)
}

function fileMatchesPath(file: SourceReviewFile, path?: string) {
  if (!path) return false
  const filePath = file.path.toLowerCase()
  const requested = path.toLowerCase()
  return filePath === requested || filePath.endsWith(`/${requested}`) || filePath.split('/').pop() === requested.split('/').pop()
}

function findSourceFile(files: SourceReviewFile[], path?: string) {
  return files.find(file => fileMatchesPath(file, path)) || files[0] || null
}

function findFirstMappingForFile(mappings: SourceReviewMapping[], filePath?: string) {
  return mappings.find(mapping => mappingMatchesFile(mapping, filePath)) || null
}

function compareText(a: string, b: string, direction: SortDirection) {
  const result = a.localeCompare(b)
  return direction === 'asc' ? result : -result
}

function mappingSortValue(mapping: SourceReviewMapping, field: SortField) {
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

function supportModeClass(mode?: string) {
  switch ((mode || '').trim().toLowerCase()) {
    case 'external_required':
      return 'external-required'
    default:
      return ''
  }
}

function formatSourceRange(mapping?: SourceReviewMapping | null) {
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

const COVERAGE_TOOLTIPS: Record<string, string> = {
  converted: 'Source mappings that were converted into executable generated DSL.',
  preserved: 'Source mappings preserved as supporting evidence, contributors, metadata, assertions, extractors, headers, timers, or data elements.',
  review: 'Source mappings kept for reviewer attention, including review-only constructs or coarse ranges where exact child spans were unavailable.',
  unsupported: 'Source mappings that could not be converted into executable DSL and need manual review before relying on the script.',
  dslWithoutOrigin: 'Generated DSL actions, parameters, or data sources with no traceable source mapping.',
}

export default function SourceMappingReview({ focusRequestId = 0, focusedMappingId = '', id, sourceReview, script, title = 'Source Mapping' }: Props) {
  const files = useMemo(() => normalizeFiles(sourceReview), [sourceReview])
  const mappings = useMemo(() => normalizeMappings(sourceReview), [sourceReview])
  const [selectedMappingId, setSelectedMappingId] = useState('')
  const [selectedFilePath, setSelectedFilePath] = useState('')
  const [filter, setFilter] = useState('')
  const [categoryFilter, setCategoryFilter] = useState<MappingCategory | ''>('')
  const [sortField, setSortField] = useState<SortField>('source')
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc')
  const [copied, setCopied] = useState<'source' | 'dsl' | ''>('')
  const [isTableCollapsed, setIsTableCollapsed] = useState(false)
  const [maximizedPane, setMaximizedPane] = useState<'source' | 'dsl' | null>(null)
  const [approvedMappingIds, setApprovedMappingIds] = useState<Set<string>>(new Set())
  const rowRefs = useRef<Record<string, HTMLTableRowElement | null>>({})

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
    setCategoryFilter('')
    setCopied('')
  }, [files, mappings])

  useEffect(() => {
    if (!focusedMappingId) return
    const nextMapping = mappings.find(mapping => mapping.id === focusedMappingId)
    if (!nextMapping) return
    setSelectedMappingId(nextMapping.id)
    if (nextMapping.source_file) setSelectedFilePath(nextMapping.source_file)
    setCategoryFilter('')
    setFilter('')
    setCopied('')
  }, [focusedMappingId, focusRequestId, mappings])

  const filteredMappings = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    let next = mappings
    if (categoryFilter) {
      next = next.filter(mapping => statusCategory(mapping.status) === categoryFilter)
    }
    if (needle) {
      next = next.filter(mapping => [
        mapping.source_file,
        mapping.function,
        mapping.target_path,
        mapping.target_name,
        mapping.status,
        mapping.support_mode,
        mapping.notes,
        approvedMappingIds.has(mapping.id) ? 'approved' : ''
      ].some(value => String(value || '').toLowerCase().includes(needle)))
    }
    return [...next].sort((a, b) => compareText(mappingSortValue(a, sortField), mappingSortValue(b, sortField), sortDirection))
  }, [filter, mappings, sortDirection, sortField, categoryFilter, approvedMappingIds])

  useEffect(() => {
    const idToScroll = focusedMappingId || selectedMappingId
    if (!idToScroll) return
    window.requestAnimationFrame(() => {
      const row = rowRefs.current[idToScroll]
      if (typeof row?.scrollIntoView === 'function') {
        row.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
      }
    })
  }, [focusedMappingId, focusRequestId, selectedMappingId])

  const selectMapping = (mapping: SourceReviewMapping | null) => {
    if (!mapping) return
    setSelectedMappingId(mapping.id)
    if (mapping.source_file) setSelectedFilePath(mapping.source_file)
    setCopied('')
  }

  const toggleCategoryFilter = (category: MappingCategory) => {
    setCategoryFilter(current => (current === category ? '' : category))
  }

  const selectSourceFile = (filePath: string) => {
    const nextMapping = findFirstMappingForFile(mappings, filePath)
    setSelectedFilePath(filePath)
    setSelectedMappingId(nextMapping?.id || '')
    setCopied('')
  }

  const isNeedsReview = (mapping: SourceReviewMapping) => {
    return statusCategory(mapping.status) === 'review' || mapping.status === 'review_only'
  }

  const toggleApprove = (mappingId: string) => {
    setApprovedMappingIds(prev => {
      const next = new Set(prev)
      if (next.has(mappingId)) {
        next.delete(mappingId)
      } else {
        next.add(mappingId)
      }
      return next
    })
  }

  const exportCsv = () => {
    const header = ['Source File', 'Start Line', 'Function', 'Target Path', 'Status', 'Approved', 'Support Mode', 'Notes'].join(',')
    const rows = filteredMappings.map(m => {
      const isApproved = approvedMappingIds.has(m.id)
      return [
        `"${m.source_file || ''}"`,
        m.start_line || '',
        `"${m.function || ''}"`,
        `"${m.target_path || m.target_name || ''}"`,
        `"${m.status || ''}"`,
        isApproved ? 'Yes' : 'No',
        `"${m.support_mode || ''}"`,
        `"${(m.notes || '').replace(/"/g, '""')}"`
      ].join(',')
    })
    const csvContent = [header, ...rows].join('\n')
    const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.setAttribute('href', url)
    link.setAttribute('download', `source_mapping_review_${script?.id || 'export'}.csv`)
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
  }

  useEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (document.activeElement?.tagName === 'INPUT' || document.activeElement?.tagName === 'TEXTAREA') {
        return
      }

      if (e.key === '[' || e.key === ']') {
        const reviewItems = filteredMappings.filter(m => isNeedsReview(m) && !approvedMappingIds.has(m.id))
        if (reviewItems.length === 0) return

        const currentIndex = reviewItems.findIndex(m => m.id === selectedMappingId)
        let nextIndex = 0

        if (e.key === ']') {
          nextIndex = currentIndex === -1 ? 0 : (currentIndex + 1) % reviewItems.length
        } else {
          nextIndex = currentIndex === -1 ? reviewItems.length - 1 : (currentIndex - 1 + reviewItems.length) % reviewItems.length
        }
        selectMapping(reviewItems[nextIndex])
      }
    }
    window.addEventListener('keydown', handleGlobalKeyDown)
    return () => window.removeEventListener('keydown', handleGlobalKeyDown)
  }, [filteredMappings, selectedMappingId, approvedMappingIds])

  if (files.length === 0 && mappings.length === 0) {
    return null
  }

  const handleTableKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      e.preventDefault()
      if (filteredMappings.length === 0) return

      const currentIndex = filteredMappings.findIndex(m => m.id === selectedMappingId)
      let nextIndex = 0

      if (currentIndex !== -1) {
        if (e.key === 'ArrowUp') {
          nextIndex = currentIndex > 0 ? currentIndex - 1 : 0
        } else {
          nextIndex = currentIndex < filteredMappings.length - 1 ? currentIndex + 1 : filteredMappings.length - 1
        }
      }

      selectMapping(filteredMappings[nextIndex])
    }
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

  const coverageChips: Array<{ key: string; label: string; value: number; tooltip: string }> = [
    { key: 'converted', label: 'Converted', value: coverage.converted, tooltip: COVERAGE_TOOLTIPS.converted },
    { key: 'preserved', label: 'Preserved', value: coverage.preserved, tooltip: COVERAGE_TOOLTIPS.preserved },
    { key: 'review', label: 'Needs review', value: coverage.review, tooltip: COVERAGE_TOOLTIPS.review },
    { key: 'unsupported', label: 'Unsupported', value: coverage.unsupported, tooltip: COVERAGE_TOOLTIPS.unsupported },
  ]

  return (
    <section id={id} className="source-map-review">
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
        {coverageChips.map(chip => {
          const category = chip.key as MappingCategory
          const active = categoryFilter === category
          return (
            <button
              key={chip.key}
              type="button"
              className={`source-map-status source-map-status-filter ${CATEGORY_CLASS[chip.key]}${active ? ' active' : ''}`}
              title={chip.tooltip}
              data-tooltip={chip.tooltip}
              aria-pressed={active}
              disabled={chip.value === 0}
              onClick={() => toggleCategoryFilter(category)}
            >
              {chip.label}: {chip.value}
            </button>
          )
        })}
        {dslDocument && (
          <span
            className={`source-map-status ${coverage.dslWithoutOrigin > 0 ? 'unsupported' : 'converted'}`}
            title={COVERAGE_TOOLTIPS.dslWithoutOrigin}
            data-tooltip={COVERAGE_TOOLTIPS.dslWithoutOrigin}
            tabIndex={0}
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
        <div style={{ alignSelf: 'flex-end', marginLeft: 'auto', display: 'flex', gap: '8px' }}>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            style={{ border: '1px solid var(--border)' }}
            onClick={exportCsv}
          >
            Export CSV
          </button>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            style={{ border: '1px solid var(--border)' }}
            onClick={() => setIsTableCollapsed(prev => !prev)}
          >
            {isTableCollapsed ? 'Show Table' : 'Collapse Table'}
          </button>
        </div>
      </div>

      <div className="source-map-table-wrap" tabIndex={0} onKeyDown={handleTableKeyDown} style={{ display: isTableCollapsed ? 'none' : 'block' }}>
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
                ref={node => { rowRefs.current[mapping.id] = node }}
                className={mapping.id === selectedMapping?.id ? 'active' : ''}
                data-mapping-id={mapping.id}
                data-target-path={mapping.target_path || ''}
                onClick={() => selectMapping(mapping)}
              >
                <td className="cell-mono">{formatSourceRange(mapping)}</td>
                <td>{mapping.function || '-'}</td>
                <td className="cell-mono">{mapping.target_path || mapping.target_name || 'review-only'}</td>
                <td>
                  <div className="source-map-status-stack">
                    <span className={`source-map-status ${approvedMappingIds.has(mapping.id) ? 'converted' : CATEGORY_CLASS[statusCategory(mapping.status)]}`}>
                      {approvedMappingIds.has(mapping.id) ? 'Approved' : formatStatusLabel(mapping.status)}
                    </span>
                    {mapping.support_mode && (
                      <span className={`source-map-support-mode ${supportModeClass(mapping.support_mode)}`.trim()}>
                        {formatSupportModeLabel(mapping.support_mode)}
                      </span>
                    )}
                    {isNeedsReview(mapping) && (
                      <button 
                        type="button" 
                        className="btn btn-sm btn-ghost"
                        style={{ padding: '0 4px', height: 'auto', minHeight: '0', fontSize: '11px', border: '1px solid var(--border)' }}
                        onClick={(e) => { e.stopPropagation(); toggleApprove(mapping.id); }}
                      >
                        {approvedMappingIds.has(mapping.id) ? 'Undo' : 'Approve'}
                      </button>
                    )}
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
        <div className={`source-map-panel ${maximizedPane === 'dsl' ? 'hidden' : maximizedPane === 'source' ? 'maximized' : ''}`}>
          <div className="source-map-panel-header">
            <strong>{selectedFile?.path || 'Source'}</strong>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('source', sourceExcerpt)} disabled={!copySupported || !sourceExcerpt}>
                {copied === 'source' ? 'Copied' : 'Copy Source'}
              </button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMaximizedPane(maximizedPane === 'source' ? null : 'source')}>
                {maximizedPane === 'source' ? 'Restore' : 'Maximize'}
              </button>
            </div>
          </div>
          {selectedFile ? (
            <Suspense fallback={<PaneFallback />}>
              <LinkedCodePane
                value={selectedFile.content}
                language={selectedFile.language}
                searchQuery={filter}
                gutterStatuses={sourceGutter}
                activeRange={sourceActiveRange}
                onLineClick={handleSourceLineClick}
                ariaLabel={`${selectedFile.path} source`}
                maxHeight="100%"
              />
            </Suspense>
          ) : (
            <div className="source-map-empty">No source file content was captured.</div>
          )}
        </div>

        <div className={`source-map-panel ${maximizedPane === 'source' ? 'hidden' : maximizedPane === 'dsl' ? 'maximized' : ''}`}>
          <div className="source-map-panel-header">
            <strong>{selectedMapping?.target_path || 'Generated DSL'}</strong>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('dsl', dslExcerpt)} disabled={!copySupported || !dslExcerpt}>
                {copied === 'dsl' ? 'Copied' : 'Copy DSL'}
              </button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMaximizedPane(maximizedPane === 'dsl' ? null : 'dsl')}>
                {maximizedPane === 'dsl' ? 'Restore' : 'Maximize'}
              </button>
            </div>
          </div>
          {dslDocument ? (
            <Suspense fallback={<PaneFallback />}>
              <LinkedCodePane
                value={dslDocument.text}
                language="json"
                semanticMode="apex-dsl"
                searchQuery={filter}
                gutterStatuses={dslStatus.lines}
                activeRange={dslActiveRange}
                onLineClick={handleDslLineClick}
                ariaLabel="Generated DSL"
                maxHeight="100%"
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
