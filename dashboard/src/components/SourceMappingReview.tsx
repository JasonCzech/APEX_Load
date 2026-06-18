import { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'

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
  onToggleWorkspaceMaximized?: () => void
  showTitle?: boolean
  sourceReview?: SourceReview | null
  script?: ScriptDefinition | null
  title?: string
  workspaceMaximized?: boolean
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

export function scrollMappingRowIntoTableView(container: HTMLElement, row: HTMLElement) {
  const rowTop = row.offsetTop
  const rowBottom = rowTop + row.offsetHeight
  const visibleTop = container.scrollTop
  const visibleBottom = visibleTop + container.clientHeight

  if (rowTop < visibleTop) {
    container.scrollTop = Math.max(0, rowTop)
  } else if (rowBottom > visibleBottom) {
    container.scrollTop = Math.max(0, rowBottom - container.clientHeight)
  }
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

export default function SourceMappingReview({
  focusRequestId = 0,
  focusedMappingId = '',
  id,
  onToggleWorkspaceMaximized,
  showTitle = true,
  sourceReview,
  script,
  title = 'Source Mapping',
  workspaceMaximized = false,
}: Props) {
  const files = useMemo(() => normalizeFiles(sourceReview), [sourceReview])
  const mappings = useMemo(() => normalizeMappings(sourceReview), [sourceReview])
  const sourceFileOptions = useMemo(() => {
    const mappedFiles = files.filter(file => mappings.some(mapping => mappingMatchesFile(mapping, file.path)))
    return mappedFiles.length > 0 ? mappedFiles : files
  }, [files, mappings])
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
  const tableWrapRef = useRef<HTMLDivElement | null>(null)
  const rowRefs = useRef<Record<string, HTMLTableRowElement | null>>({})
  const lastAppliedFocusRequestRef = useRef<number | null>(null)
  const lastTableScrollFocusRequestRef = useRef<number | null>(null)

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
    const selectedStillExists = selectedMappingId && mappings.some(mapping => mapping.id === selectedMappingId)
    const nextMapping = selectedStillExists
      ? mappings.find(mapping => mapping.id === selectedMappingId) || null
      : mappings[0] || null

    if ((nextMapping?.id || '') !== selectedMappingId) {
      setSelectedMappingId(nextMapping?.id || '')
    }

    const fileStillExists = selectedFilePath && sourceFileOptions.some(file => fileMatchesPath(file, selectedFilePath))
    if (!fileStillExists) {
      const nextFile = nextMapping?.source_file
        ? sourceFileOptions.find(file => fileMatchesPath(file, nextMapping.source_file)) || sourceFileOptions[0]
        : sourceFileOptions[0]
      setSelectedFilePath(nextFile?.path || nextMapping?.source_file || '')
    }
  }, [mappings, selectedFilePath, selectedMappingId, sourceFileOptions])

  useEffect(() => {
    if (!focusedMappingId) return
    if (lastAppliedFocusRequestRef.current === focusRequestId) return
    const nextMapping = mappings.find(mapping => mapping.id === focusedMappingId)
    if (!nextMapping) return
    lastAppliedFocusRequestRef.current = focusRequestId
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

  // Virtualize the mappings table — a large LoadRunner import can carry thousands of
  // mappings, and rendering one <tr> per mapping would commit thousands of DOM rows.
  // Only virtualize when there are many mappings; small imports (and test fixtures) render
  // the full table so behavior is unchanged. Large LoadRunner imports (thousands of
  // mappings) switch to windowed rendering.
  const virtualizeRows = filteredMappings.length > 100
  const rowVirtualizer = useVirtualizer({
    count: filteredMappings.length,
    getScrollElement: () => tableWrapRef.current,
    estimateSize: () => 44,
    overscan: 12,
  })
  const virtualRows = rowVirtualizer.getVirtualItems()
  const virtualTotalSize = rowVirtualizer.getTotalSize()
  const virtualPaddingTop = virtualRows.length > 0 ? virtualRows[0].start : 0
  const virtualPaddingBottom = virtualRows.length > 0 ? virtualTotalSize - virtualRows[virtualRows.length - 1].end : 0
  const mappingRowsToRender = virtualizeRows
    ? virtualRows.map(virtualRow => ({ mapping: filteredMappings[virtualRow.index], virtualItem: virtualRow }))
    : filteredMappings.map(mapping => ({ mapping, virtualItem: null as VirtualItem | null }))

  useEffect(() => {
    const hasNewFocusRequest = !!focusedMappingId && lastTableScrollFocusRequestRef.current !== focusRequestId
    const idToScroll = hasNewFocusRequest ? focusedMappingId : selectedMappingId
    if (hasNewFocusRequest) {
      lastTableScrollFocusRequestRef.current = focusRequestId
    }
    if (!idToScroll || isTableCollapsed) return
    if (virtualizeRows) {
      const index = filteredMappings.findIndex(mapping => mapping.id === idToScroll)
      // Bring the row into the virtual window first; once rendered, the rAF below
      // fine-tunes with the existing centering helper.
      if (index >= 0) rowVirtualizer.scrollToIndex(index, { align: 'auto' })
    }
    window.requestAnimationFrame(() => {
      const tableWrap = tableWrapRef.current
      const row = rowRefs.current[idToScroll]
      if (tableWrap && row) {
        scrollMappingRowIntoTableView(tableWrap, row)
      }
    })
  }, [focusedMappingId, focusRequestId, isTableCollapsed, selectedMappingId, filteredMappings, rowVirtualizer])

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

  // csvCell quotes a value, doubling interior quotes (so embedded `"` in
  // imported source fields can't break the row) and prefixing a `'` when the
  // value starts with a spreadsheet formula trigger (CSV/Excel formula injection).
  const csvCell = (value: unknown): string => {
    let s = String(value ?? '')
    if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
    return `"${s.replace(/"/g, '""')}"`
  }

  const exportCsv = () => {
    const header = ['Source File', 'Start Line', 'Function', 'Target Path', 'Status', 'Approved', 'Support Mode', 'Notes'].join(',')
    const rows = filteredMappings.map(m => {
      const isApproved = approvedMappingIds.has(m.id)
      return [
        csvCell(m.source_file || ''),
        m.start_line || '',
        csvCell(m.function || ''),
        csvCell(m.target_path || m.target_name || ''),
        csvCell(m.status || ''),
        isApproved ? 'Yes' : 'No',
        csvCell(m.support_mode || ''),
        csvCell(m.notes || '')
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
      <div className={`source-map-header ${showTitle ? '' : 'title-hidden'}`}>
        {showTitle && (
          <div>
            <h4>{title}</h4>
            <p>Validate the conversion line by line — select on either side and the match is highlighted on the other.</p>
          </div>
        )}
        <div className="source-map-header-controls">
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
          {onToggleWorkspaceMaximized && (
            <div className="source-map-header-actions">
              <button
                type="button"
                className="btn btn-sm btn-ghost source-map-workspace-toggle"
                aria-pressed={workspaceMaximized}
                title={workspaceMaximized ? 'Restore side panels' : 'Maximize source mapping'}
                onClick={onToggleWorkspaceMaximized}
              >
                {workspaceMaximized ? 'Restore' : 'Maximize'}
              </button>
            </div>
          )}
        </div>
      </div>

      <div className="source-map-toolbar">
        <label>
          <span>Source File</span>
          <select value={selectedFile?.path || ''} onChange={event => selectSourceFile(event.target.value)}>
            {sourceFileOptions.map(file => (
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

      <div ref={tableWrapRef} className="source-map-table-wrap" tabIndex={0} onKeyDown={handleTableKeyDown} style={{ display: isTableCollapsed ? 'none' : 'block' }}>
        <table className="source-map-table" aria-label="Source conversion mappings">
          <colgroup>
            <col className="source-map-col-source" />
            <col className="source-map-col-function" />
            <col className="source-map-col-target" />
            <col className="source-map-col-status" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col"><button type="button" onClick={() => handleSort('source')}>Source</button></th>
              <th scope="col"><button type="button" onClick={() => handleSort('function')}>Function</button></th>
              <th scope="col"><button type="button" onClick={() => handleSort('target')}>Generated DSL</button></th>
              <th scope="col"><button type="button" onClick={() => handleSort('status')}>Status</button></th>
            </tr>
          </thead>
          <tbody>
            {virtualizeRows && virtualPaddingTop > 0 && (
              <tr aria-hidden="true" className="source-map-virtual-spacer">
                <td colSpan={4} style={{ height: virtualPaddingTop, padding: 0, border: 0 }} />
              </tr>
            )}
            {mappingRowsToRender.map(({ mapping, virtualItem }) => (
              <tr
                key={mapping.id}
                data-index={virtualItem ? virtualItem.index : undefined}
                ref={node => { rowRefs.current[mapping.id] = node; if (virtualItem) rowVirtualizer.measureElement(node) }}
                className={mapping.id === selectedMapping?.id ? 'active' : ''}
                data-mapping-id={mapping.id}
                data-target-path={mapping.target_path || ''}
                onClick={() => selectMapping(mapping)}
              >
                <td className="cell-mono source-map-source-cell" title={formatSourceRange(mapping)}>{formatSourceRange(mapping)}</td>
                <td className="source-map-function-cell" title={mapping.function || '-'}>{mapping.function || '-'}</td>
                <td className="cell-mono source-map-target-cell" title={mapping.target_path || mapping.target_name || 'review-only'}>{mapping.target_path || mapping.target_name || 'review-only'}</td>
                <td className="source-map-status-cell">
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
                        className="btn btn-sm btn-ghost source-map-approve-button"
                        onClick={(e) => { e.stopPropagation(); toggleApprove(mapping.id); }}
                      >
                        {approvedMappingIds.has(mapping.id) ? 'Undo' : 'Approve'}
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
            {virtualizeRows && virtualPaddingBottom > 0 && (
              <tr aria-hidden="true" className="source-map-virtual-spacer">
                <td colSpan={4} style={{ height: virtualPaddingBottom, padding: 0, border: 0 }} />
              </tr>
            )}
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
                {maximizedPane === 'source' ? 'Reset' : 'Maximize'}
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
                {maximizedPane === 'dsl' ? 'Reset' : 'Maximize'}
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
