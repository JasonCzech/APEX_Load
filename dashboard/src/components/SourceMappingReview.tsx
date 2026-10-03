import TooltipButton from './ux/TooltipButton'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { MappingReviewDetails } from './MappingReviewDetails'
import { useMappingReviewState } from './useMappingReviewState'
import { scriptHasSourceOnlyReview, sourceReviewIsSourceOnly } from './sourceReviewDomain'
import { decisionMatches, type MappingReviewState } from './sourceMappingReviewState'
import { Suspense, lazy, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer, type VirtualItem } from '@tanstack/react-virtual'

import {
  type JavaVuserSourceManifest,
  type ScriptDefinition,
  type SourceReview,
  type SourceReviewFile,
  type SourceReviewMapping,
} from '../types/scripts'
import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import { downloadTextFile } from '../utils/fileUtils'
import CodeLineWrapToggle from './CodeLineWrapToggle'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'
import { scrollMappingRowIntoTableView } from './sourceMappingScroll'
import { buildDslDocument, lineRangeForTargetPath } from './dslJsonPointers'
import { ImportSourceIssueFocusContext, type ImportSourceIssueFocus } from './ImportSourceIssueFocus'
import {
  type MappingCategory,
  type SourceFilePathResolver,
  computeCoverage,
  createSourceFilePathResolver,
  dslLineStatus,
  lineStatusForSource,
  mappingForDslLine,
  mappingForSourceLine,
  mappingMatchesFile,
  resolveSourceFilePath,
  statusCategory,
} from './sourceMappingModel'
import './SourceMappingReview.css'

// CodeMirror is only needed inside this review surface, so keep it out of the
// main bundle.
const LinkedCodePane = lazy(() => import('./LinkedCodePane'))

type SortField = 'source' | 'function' | 'target' | 'status'
type SortDirection = 'asc' | 'desc'

interface Props {
  baselineScript?: ScriptDefinition | null
  onChangeReview?: (state: MappingReviewState) => void
  draftDirty?: boolean
  repairPaths?: string[]
  onRepairTarget?: (path: string) => void
  onEditTarget?: (path: string) => void
  focusRequestId?: number
  focusedSourceFile?: string
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

function objectValue(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

function javaVuserSourceManifest(script?: ScriptDefinition | null): JavaVuserSourceManifest | null {
  const preserved = script?.metadata?.import?.preserved
  const reviewArtifact = objectValue(preserved?.review_artifact)
  const metadata = objectValue(reviewArtifact?.metadata)
  const manifest = objectValue(metadata?.java_source_manifest)
  if (
    manifest?.contract !== 'apexload.java_vuser.source_manifest.v1' ||
    typeof manifest.digest !== 'string' ||
    typeof manifest.file_count !== 'number' ||
    typeof manifest.total_bytes !== 'number'
  ) {
    return null
  }
  return manifest as unknown as JavaVuserSourceManifest
}

function findSourceFile(
  files: SourceReviewFile[],
  path: string | undefined,
  pathResolver: SourceFilePathResolver,
) {
  if (!path) return files[0] || null
  const resolvedPath = resolveSourceFilePath(path, pathResolver)
  return resolvedPath ? files.find(file => file.path === resolvedPath) || null : null
}

function findExactSourceFile(files: SourceReviewFile[], path: string | undefined) {
  if (!path) return null
  return files.find(file => file.path === path) || null
}

function findFirstMappingForFile(
  mappings: SourceReviewMapping[],
  filePath: string | undefined,
  pathResolver: SourceFilePathResolver,
) {
  return mappings.find(mapping => mappingMatchesFile(mapping, filePath, pathResolver)) || null
}

function issueSourceTarget(
  focus: ImportSourceIssueFocus,
  files: SourceReviewFile[],
  mappings: SourceReviewMapping[],
  pathResolver: SourceFilePathResolver,
) {
  const evidence = `${focus.source} ${focus.description}`
  const source = focus.source.toLowerCase()
  const exactFile = files.find(file => source.includes(file.path.toLowerCase()))
  const basenameMatches = files.filter(file => source.includes(file.path.split('/').pop()!.toLowerCase()))
  const file = exactFile || (basenameMatches.length === 1 ? basenameMatches[0] : files.length === 1 ? files[0] : null)
  const lineText = /\bline\s*:?\s*(\d+)\b/i.exec(evidence)?.[1] || /:(\d+)\b/.exec(focus.source)?.[1]
  const line = lineText ? Number(lineText) : 0
  const validLine = file && line > 0 && line <= file.content.split('\n').length ? line : 0
  // Several unrelated mappings can share a source line. Only select a mapping
  // when the finding explicitly identifies it; otherwise show the source line.
  const mapping = mappings.find(item =>
    (focus.source === item.id || focus.source.includes(`mapping:${item.id}`))
    && (!file || mappingMatchesFile(item, file.path, pathResolver))
  )
  const mappedFile = mapping?.source_file ? findSourceFile(files, mapping.source_file, pathResolver) : null
  return { file: file || mappedFile, line: validLine, mapping }
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

export default function SourceMappingReview({
  baselineScript, onChangeReview, draftDirty, onEditTarget, onRepairTarget, repairPaths,
  focusRequestId = 0,
  focusedMappingId = '',
  focusedSourceFile = '',
  id,
  onToggleWorkspaceMaximized,
  showTitle = true,
  sourceReview,
  script: providedScript,
  title = 'Source Mapping',
  workspaceMaximized = false,
}: Props) {
  const sourceOnly = sourceReviewIsSourceOnly(sourceReview) || scriptHasSourceOnlyReview(providedScript)
  const script = sourceOnly ? null : providedScript
  const [wrapLines, setWrapLines] = useCodeLineWrapPreference()
  const files = useMemo(() => normalizeFiles(sourceReview), [sourceReview])
  const mappings = useMemo(() => normalizeMappings(sourceReview), [sourceReview])
  const omittedFiles = useMemo(
    () => (sourceReview?.omitted_source_files || []).filter(path => path),
    [sourceReview],
  )
  const truncatedFiles = files.filter(file => file.truncated)
  const isJavaVuserReview = script?.protocol?.trim().toLowerCase() === 'java_vuser'
  const javaSourceManifest = javaVuserSourceManifest(script)
  const sourceFilePaths = useMemo(() => files.map(file => file.path), [files])
  // Omitted paths still participate in identity resolution. Otherwise a
  // mapping whose canonical file was omitted from the bounded preview could
  // basename-fallback to a captured backup and display the wrong source.
  const resolutionFilePaths = useMemo(
    () => [...sourceFilePaths, ...omittedFiles],
    [omittedFiles, sourceFilePaths],
  )
  const sourcePathResolver = useMemo(
    () => createSourceFilePathResolver(resolutionFilePaths),
    [resolutionFilePaths],
  )
  const omittedJavaFiles = omittedFiles.filter(path => path.trim().toLowerCase().endsWith('.java'))
  const truncatedJavaFiles = truncatedFiles.filter(file => file.path.trim().toLowerCase().endsWith('.java'))
  const unaccountedFiles = Math.max(0, (sourceReview?.total_source_files || 0) - files.length - omittedFiles.length)
  const isBoundedJavaPreview = isJavaVuserReview && (
    omittedJavaFiles.length > 0 ||
    truncatedJavaFiles.length > 0
  )
  const sourceFileOptions = files
  const [selectedMappingId, setSelectedMappingId] = useState('')
  const [selectedFilePath, setSelectedFilePath] = useState('')
  const sourceIssueFocus = useContext(ImportSourceIssueFocusContext)
  const [unmappedIssueFocus, setUnmappedIssueFocus] = useState<{ filePath: string; line: number; title: string } | null>(null)
  // A reviewer may explicitly inspect a captured file even when an ambiguous
  // mapping cannot be attributed to it. Keep that choice separate from the
  // mapping-driven selection so the synchronization effect does not
  // immediately snap back to the first unresolved mapping.
  const [inspectedUnmappedFilePath, setInspectedUnmappedFilePath] = useState('')
  const [filter, setFilter] = useState('')
  const [categoryFilter, setCategoryFilter] = useState<MappingCategory | ''>('')
  const [sortField, setSortField] = useState<SortField>('source')
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc')
  const [copied, setCopied] = useState<'source' | 'dsl' | ''>('')
  const [exportError, setExportError] = useState('')
  const [isTableCollapsed, setIsTableCollapsed] = useState(true)
  const [storedMaximizedPane, setMaximizedPane] = useState<'source' | 'dsl' | null>(null)
  const maximizedPane = sourceOnly ? null : storedMaximizedPane
  const [destinationView, setDestinationView] = useState<'current' | 'baseline'>('current')
  const [contextOnly, setContextOnly] = useState(false)
  const [revealRequestId, setRevealRequestId] = useState(1)
  const [storedNoOriginTarget, setNoOriginTarget] = useState('')
  const noOriginTarget = sourceOnly ? '' : storedNoOriginTarget
  const [showNoOrigin, setShowNoOrigin] = useState(false)
  const keyboardSelection = useRef(false)
  const openingScript = useRef(script)
  const identity = useMappingReviewState(script, baselineScript || openingScript.current, sourceReview)
  const effectiveMappings = useMemo(() => sourceOnly ? mappings : mappings.map(mapping => ({ ...mapping, target_path: identity.resolved[mapping.id]?.path })), [mappings, identity.resolved, sourceOnly])
  const acknowledgedMappingIds = useMemo(() => new Set(mappings.filter(mapping => identity.state && decisionMatches(identity.state.decisions[mapping.id], identity.state, identity.resolved[mapping.id]) && identity.state.decisions[mapping.id]?.outcome === 'acknowledged').map(mapping => mapping.id)), [mappings, identity.state, identity.resolved])
  const tableWrapRef = useRef<HTMLDivElement | null>(null)
  const reviewRootRef = useRef<HTMLElement | null>(null)
  const rowRefs = useRef<Record<string, HTMLTableRowElement | null>>({})
  const lastAppliedFocusRequestRef = useRef<number | null>(null)
  const lastAppliedIssueFocusRef = useRef<number | null>(null)
  const lastTableScrollFocusRequestRef = useRef<number | null>(null)

  const selectedMapping = useMemo(
    () => mappings.find(mapping => mapping.id === selectedMappingId) || null,
    [mappings, selectedMappingId],
  )
  const selectedFile = useMemo(
    () => unmappedIssueFocus && !unmappedIssueFocus.filePath ? null : selectedFilePath
      ? findExactSourceFile(files, selectedFilePath)
      : findSourceFile(files, selectedMapping?.source_file, sourcePathResolver),
    [files, selectedFilePath, selectedMapping?.source_file, sourcePathResolver, unmappedIssueFocus],
  )

  const destinationScript = destinationView === 'baseline' ? identity.state?.snapshot : script
  const dslDocument = useMemo(() => (destinationScript ? buildDslDocument(destinationScript) : null), [destinationScript])
  const dslStatus = useMemo(
    () => (dslDocument ? dslLineStatus(dslDocument.ranges, destinationView === 'baseline' ? mappings : effectiveMappings) : { lines: new Map<number, never>(), withoutOrigin: [] }),
    [dslDocument, mappings, effectiveMappings, destinationView],
  )
  const coverage = useMemo(
    () => computeCoverage(mappings, dslStatus.withoutOrigin.length),
    [mappings, dslStatus.withoutOrigin.length],
  )
  const sourceGutter = useMemo(
    () => lineStatusForSource(mappings, selectedFile?.path, sourcePathResolver),
    [mappings, selectedFile, sourcePathResolver],
  )

  useEffect(() => {
    if (unmappedIssueFocus) {
      if (selectedMappingId) setSelectedMappingId('')
      if (selectedFilePath !== unmappedIssueFocus.filePath) setSelectedFilePath(unmappedIssueFocus.filePath)
      return
    }
    const inspectedFile = inspectedUnmappedFilePath
      ? findExactSourceFile(sourceFileOptions, inspectedUnmappedFilePath)
      : null
    const inspectedFileMapping = inspectedFile
      ? findFirstMappingForFile(mappings, inspectedFile.path, sourcePathResolver)
      : null

    if (inspectedFile && !inspectedFileMapping) {
      if (selectedMappingId) {
        setSelectedMappingId('')
      }
      if (selectedFilePath !== inspectedFile.path) {
        setSelectedFilePath(inspectedFile.path)
      }
      return
    }

    if (inspectedUnmappedFilePath) {
      setInspectedUnmappedFilePath('')
    }

    const selectedStillExists = selectedMappingId && mappings.some(mapping => mapping.id === selectedMappingId)
    const nextMapping = inspectedFileMapping || (selectedStillExists
      ? mappings.find(mapping => mapping.id === selectedMappingId) || null
      : mappings.find(mapping => mapping.id === focusedMappingId) || mappings[0] || null)

    if ((nextMapping?.id || '') !== selectedMappingId) {
      setSelectedMappingId(nextMapping?.id || '')
    }

    const selectedOption = selectedFilePath
      ? findExactSourceFile(sourceFileOptions, selectedFilePath)
      : null
    const nextMappingFile = nextMapping?.source_file
      ? findSourceFile(sourceFileOptions, nextMapping.source_file, sourcePathResolver)
      : null
    const selectionIsAligned = nextMapping
      ? selectedOption !== null && nextMappingFile !== null && selectedOption.path === nextMappingFile.path
      : selectedOption !== null
    if (!selectionIsAligned) {
      const nextFile = nextMapping?.source_file
        ? nextMappingFile || undefined
        : sourceFileOptions[0]
      setSelectedFilePath(nextFile?.path || '')
    }
  }, [
    inspectedUnmappedFilePath,
    focusedMappingId,
    mappings,
    selectedFilePath,
    selectedMappingId,
    unmappedIssueFocus,
    sourcePathResolver,
    sourceFileOptions,
  ])

  useEffect(() => {
    if (!sourceIssueFocus || lastAppliedIssueFocusRef.current === sourceIssueFocus.revision) return
    lastAppliedIssueFocusRef.current = sourceIssueFocus.revision
    const target = issueSourceTarget(sourceIssueFocus, files, mappings, sourcePathResolver)
    setUnmappedIssueFocus(target.mapping ? null : { filePath: target.file?.path || '', line: target.line, title: sourceIssueFocus.title })
    setInspectedUnmappedFilePath('')
    setSelectedMappingId(target.mapping?.id || '')
    setSelectedFilePath(target.file?.path || '')
    setCategoryFilter('')
    setFilter('')
    setNoOriginTarget('')
    setRevealRequestId(value => value + 1)
  }, [files, mappings, sourceIssueFocus, sourcePathResolver])

  useEffect(() => {
    if (sourceIssueFocus) return
    if (!focusedMappingId && !focusedSourceFile) return
    if (lastAppliedFocusRequestRef.current === focusRequestId) return
    const requestedFile = focusedSourceFile ? findSourceFile(files, focusedSourceFile, sourcePathResolver) : null
    const nextMapping = focusedMappingId
      ? mappings.find(mapping => mapping.id === focusedMappingId)
      : requestedFile ? findFirstMappingForFile(mappings, requestedFile.path, sourcePathResolver) : undefined
    if (!nextMapping && !requestedFile) return
    lastAppliedFocusRequestRef.current = focusRequestId
    const nextFile = requestedFile || (nextMapping?.source_file ? findSourceFile(files, nextMapping.source_file, sourcePathResolver) : null)
    setInspectedUnmappedFilePath(nextMapping ? '' : nextFile?.path || '')
    setSelectedMappingId(nextMapping?.id || '')
    setSelectedFilePath(nextFile?.path || '')
    setCategoryFilter('')
    setFilter('')
    setNoOriginTarget('')
    setRevealRequestId(value => value + 1)
    setCopied('')
  }, [files, focusedMappingId, focusedSourceFile, focusRequestId, mappings, sourcePathResolver, sourceIssueFocus])

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
        acknowledgedMappingIds.has(mapping.id) ? 'acknowledged' : '',
        identity.state?.decisions[mapping.id]?.outcome,
        identity.resolved[mapping.id]?.path
      ].some(value => String(value || '').toLowerCase().includes(needle)))
    }
    return [...next].sort((a, b) => compareText(mappingSortValue(a, sortField), mappingSortValue(b, sortField), sortDirection))
  }, [filter, mappings, sortDirection, sortField, categoryFilter, acknowledgedMappingIds, identity.state, identity.resolved])

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

  const selectMapping = useCallback((mapping: SourceReviewMapping | null) => {
    if (!mapping) return
    setUnmappedIssueFocus(null)
    setInspectedUnmappedFilePath('')
    setSelectedMappingId(mapping.id)
    setNoOriginTarget('')
    setRevealRequestId(value => value + 1)
    if (mapping.source_file) {
      const nextFile = findSourceFile(files, mapping.source_file, sourcePathResolver)
      setSelectedFilePath(nextFile?.path || '')
    }
    setCopied('')
  }, [files, sourcePathResolver])

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
        if (keyboardSelection.current) { row.querySelector<HTMLButtonElement>('[data-mapping-select]')?.focus({ preventScroll: true }); keyboardSelection.current = false }
      }
    })
  }, [focusedMappingId, focusRequestId, isTableCollapsed, selectedMappingId, filteredMappings, rowVirtualizer, virtualizeRows])

  const toggleCategoryFilter = (category: MappingCategory) => {
    setIsTableCollapsed(false)
    setCategoryFilter(current => (current === category ? '' : category))
  }

  const selectSourceFile = (filePath: string) => {
    setUnmappedIssueFocus(null)
    const nextMapping = findFirstMappingForFile(mappings, filePath, sourcePathResolver)
    setInspectedUnmappedFilePath(nextMapping ? '' : filePath)
    setSelectedFilePath(filePath)
    setSelectedMappingId(nextMapping?.id || '')
    setNoOriginTarget('')
    setRevealRequestId(value => value + 1)
    setCopied('')
  }

  const isNeedsReview = (mapping: SourceReviewMapping) => {
    return ['review', 'unsupported'].includes(statusCategory(mapping.status))
  }

  // csvCell quotes a value, doubling interior quotes (so embedded `"` in
  // imported source fields can't break the row) and prefixing a `'` when the
  // value starts with a spreadsheet formula trigger (CSV/Excel formula injection).
  const csvCell = (value: unknown): string => {
    let s = String(value ?? '')
    if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
    return `"${s.replace(/"/g, '""')}"`
  }

  const exportCsv = async () => {
    setExportError('')
    const header = (sourceOnly
      ? ['Source File', 'Start Line', 'Function', 'Original Import Path', 'Original Conversion Status', 'Original Support Mode', 'Notes']
      : ['Source File', 'Start Line', 'Function', 'Target Path', 'Status', 'Review Decision', 'Reviewer', 'Reviewed At', 'Review Note', 'Current Target Path', 'Target Identity', 'Support Mode', 'Notes']).join(',')
    const rows = filteredMappings.map(m => {
      const isApproved = acknowledgedMappingIds.has(m.id)
      if (sourceOnly) return [csvCell(m.source_file), m.start_line || '', csvCell(m.function), csvCell(m.target_path), csvCell(m.status), csvCell(m.support_mode), csvCell(m.notes)].join(',')
      return [
        csvCell(m.source_file || ''),
        m.start_line || '',
        csvCell(m.function || ''),
        csvCell(m.target_path || m.target_name || ''),
        csvCell(m.status || ''),
        csvCell(isApproved ? 'acknowledged' : identity.state?.decisions[m.id] ? decisionMatches(identity.state.decisions[m.id], identity.state, identity.resolved[m.id]) ? identity.state.decisions[m.id].outcome : 'stale' : ''),
        csvCell(identity.state?.decisions[m.id]?.reviewer || ''),
        csvCell(identity.state?.decisions[m.id]?.reviewedAt || ''),
        csvCell(identity.state?.decisions[m.id]?.note || ''),
        csvCell(identity.resolved[m.id]?.path || ''),
        csvCell(identity.resolved[m.id]?.status || 'unverified'),
        csvCell(m.support_mode || ''),
        csvCell(m.notes || '')
      ].join(',')
    })
    const csvContent = [header, ...rows].join('\n')
    try {
      await downloadTextFile(
        csvContent,
        `source_mapping_review_${script?.id || 'export'}.csv`,
        'text/csv;charset=utf-8;',
      )
    } catch (cause: unknown) {
      setExportError(cause instanceof Error ? cause.message : 'Unable to export the source mapping CSV.')
    }
  }

  useEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (!reviewRootRef.current?.contains(document.activeElement) || reviewRootRef.current?.closest('[hidden], [aria-hidden="true"]')) {
        return
      }
      const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
      if (
        activeElement?.isContentEditable ||
        activeElement?.closest('input, textarea, select, [contenteditable="true"]')
      ) {
        return
      }

      if (e.key === '[' || e.key === ']') {
        const reviewItems = filteredMappings.filter(m => isNeedsReview(m) && !acknowledgedMappingIds.has(m.id))
        if (reviewItems.length === 0) return

        const currentIndex = reviewItems.findIndex(m => m.id === selectedMappingId)
        let nextIndex = 0

        if (e.key === ']') {
          nextIndex = currentIndex === -1 ? 0 : (currentIndex + 1) % reviewItems.length
        } else {
          nextIndex = currentIndex === -1 ? reviewItems.length - 1 : (currentIndex - 1 + reviewItems.length) % reviewItems.length
        }
        e.preventDefault()
        selectMapping(reviewItems[nextIndex])
      }
    }
    window.addEventListener('keydown', handleGlobalKeyDown)
    return () => window.removeEventListener('keydown', handleGlobalKeyDown)
  }, [acknowledgedMappingIds, filteredMappings, selectMapping, selectedMappingId])

  if (files.length === 0 && mappings.length === 0) {
    return null
  }

  const handleTableKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('input, textarea, select, [contenteditable="true"]')) return
    if (['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(e.key)) {
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

      if (e.key === 'Home') nextIndex = 0
      if (e.key === 'End') nextIndex = filteredMappings.length - 1
      keyboardSelection.current = true
      selectMapping(filteredMappings[nextIndex])
    }
  }

  const sourceActiveRange = unmappedIssueFocus && selectedFile?.path === unmappedIssueFocus.filePath && unmappedIssueFocus.line
    ? { startLine: unmappedIssueFocus.line, endLine: unmappedIssueFocus.line }
    : !noOriginTarget && selectedMapping && selectedFile && mappingMatchesFile(selectedMapping, selectedFile.path, sourcePathResolver)
      ? { startLine: selectedMapping.start_line, endLine: selectedMapping.end_line || selectedMapping.start_line }
      : null
  const destinationPath = noOriginTarget || (destinationView === 'baseline' ? selectedMapping?.target_path : selectedMapping ? identity.resolved[selectedMapping.id]?.path : undefined)
  const dslActiveRange = dslDocument ? lineRangeForTargetPath(dslDocument.ranges, destinationPath) : null

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
    selectMapping(mappingForSourceLine(mappings, selectedFile?.path, line, sourcePathResolver))
  }
  const handleDslLineClick = (line: number) => {
    if (!dslDocument) return
    selectMapping(mappingForDslLine(dslDocument.ranges, destinationView === 'baseline' ? mappings : effectiveMappings, line))
  }

  const coverageChips: Array<{ key: string; label: string; value: number; tooltip: string }> = [
    { key: 'converted', label: 'Converted mappings', value: coverage.converted, tooltip: COVERAGE_TOOLTIPS.converted },
    { key: 'preserved', label: 'Preserved mappings', value: coverage.preserved, tooltip: COVERAGE_TOOLTIPS.preserved },
    { key: 'review', label: 'Needs review', value: coverage.review, tooltip: COVERAGE_TOOLTIPS.review },
    { key: 'unsupported', label: 'Unsupported', value: coverage.unsupported, tooltip: COVERAGE_TOOLTIPS.unsupported },
  ]

  return (
    <section ref={reviewRootRef} id={id} className={`source-map-review${sourceOnly ? ' source-map-original-only' : ''}`} aria-label={sourceOnly ? 'Original import source review' : undefined}>
      <div className={`source-map-header ${showTitle ? '' : 'title-hidden'}`}>
        {showTitle && (
          <ImportReviewSectionHeader title={title} description={sourceOnly ? 'Inspect captured source and original import paths. Native transformations can change action positions.' : 'Compare source and draft code. Select either side to highlight the corresponding mapping.'} />
        )}
        <div className="source-map-header-controls">
          <div className="source-map-coverage" role="group" aria-label={sourceOnly ? 'Original import conversion coverage' : 'Conversion coverage'}>
            {coverageChips.map(chip => {
              const category = chip.key as MappingCategory
              const active = categoryFilter === category
              return (
                <TooltipButton
                  key={chip.key}
                  type="button"
                  className={`source-map-status source-map-status-filter ${CATEGORY_CLASS[chip.key]}${active ? ' active' : ''}`}
                  title={chip.tooltip}
                  aria-pressed={active}
                  disabled={chip.value === 0}
                  onClick={() => toggleCategoryFilter(category)}
                >
                  {chip.label}: {chip.value}
                </TooltipButton>
              )
            })}
            {dslDocument && (
              <TooltipButton type="button" disabled={identity.checking} aria-expanded={showNoOrigin} onClick={() => setShowNoOrigin(value => !value)}
                className={`source-map-status ${coverage.dslWithoutOrigin > 0 ? 'unsupported' : 'converted'}`}
                title={COVERAGE_TOOLTIPS.dslWithoutOrigin}
              >
                DSL without source origin: {identity.checking ? 'Verifying…' : coverage.dslWithoutOrigin}
              </TooltipButton>
            )}
          </div>
          {onToggleWorkspaceMaximized && (
            <div className="source-map-header-actions">
              <TooltipButton
                type="button"
                className="btn btn-sm btn-ghost source-map-workspace-toggle"
                aria-pressed={workspaceMaximized}
                title={workspaceMaximized ? 'Restore side panels' : 'Expand source mapping workspace'}
                onClick={onToggleWorkspaceMaximized}
              >
                {workspaceMaximized ? 'Restore workspace' : 'Expand workspace'}
              </TooltipButton>
            </div>
          )}
        </div>
      </div>

      {sourceOnly && <p role="note">These mappings describe the original import before native transformations. Source navigation and conversion coverage refer to that captured import.</p>}

      {(omittedFiles.length > 0 || truncatedFiles.length > 0 || unaccountedFiles > 0) && (
        <aside className="source-map-incomplete" role="note" aria-label="Source preview limits">
          <strong>Preview limits only — not an import issue</strong>
          <p>Preview limits do not truncate the imported script or remove data from the import. Any conversion issues are reported separately.</p>
          {unaccountedFiles > 0 && (
            <p className="source-map-preview-integrity" role="alert">{unaccountedFiles} source file{unaccountedFiles === 1 ? ' is' : 's are'} not accounted for by the review payload. Review file counts are inconsistent; re-import before approval.</p>
          )}
          {isBoundedJavaPreview && (
            <p className="source-map-preview-integrity" role={javaSourceManifest ? undefined : 'alert'}>
              {javaSourceManifest
                ? 'Approval is bound to the immutable full Java source manifest. Content outside this preview requires out-of-band review of the governed original archive before approval.'
                : 'The immutable full Java source manifest is missing; re-import before approval.'}
            </p>
          )}
          {(omittedFiles.length > 0 || truncatedFiles.length > 0) && (
            <details className="source-map-preview-files">
              <summary>View files with limited previews ({omittedFiles.length + truncatedFiles.length})</summary>
              <ul aria-label="Files with limited previews">
                {truncatedFiles.map(file => (
                  <li key={file.path}>
                    <code className="source-map-preview-path">{file.path}</code>
                    <span className="source-map-preview-status">Shortened in preview</span>
                    <button className="btn btn-sm btn-secondary" type="button" aria-label={`View preview of ${file.path}`} onClick={() => selectSourceFile(file.path)}>View preview</button>
                  </li>
                ))}
                {omittedFiles.map(path => (
                  <li key={path}>
                    <code className="source-map-preview-path">{path}</code>
                    <span className="source-map-preview-status">Not shown in preview</span>
                    <span className="source-map-preview-original">See original archive</span>
                  </li>
                ))}
              </ul>
            </details>
          )}
        </aside>
      )}

      {isJavaVuserReview && javaSourceManifest && (
        <div className="source-map-manifest" aria-label="Java full-source manifest">
          <strong>Full-source manifest</strong>
          <span>{javaSourceManifest.file_count} Java file{javaSourceManifest.file_count === 1 ? '' : 's'}</span>
          <span>{javaSourceManifest.total_bytes.toLocaleString()} bytes</span>
          <code title={javaSourceManifest.digest}>{javaSourceManifest.digest}</code>
        </div>
      )}

      <div className="source-map-toolbar import-review-toolbar">
        <label>
          <span>Captured source file ({files.length})</span>
          <select value={selectedFile?.path || ''} onChange={event => selectSourceFile(event.target.value)}>
            {!selectedFile && <option value="">No unambiguous source file</option>}
            {sourceFileOptions.map(file => (
              <option key={file.path} value={file.path}>
                {file.path}{file.truncated ? ' (truncated)' : ''}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>Filter Mappings</span>
          <input value={filter} onChange={event => { setFilter(event.target.value); setIsTableCollapsed(false) }} placeholder="Function, target, status..." />
        </label>
        <div style={{ alignSelf: 'flex-end', marginLeft: 'auto', display: 'flex', gap: '8px' }}>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            style={{ border: '1px solid var(--border)' }}
            onClick={() => setIsTableCollapsed(prev => !prev)}
          >
            {isTableCollapsed ? `Show mappings (${filteredMappings.length})` : 'Hide mappings'}
          </button>
        </div>
      </div>
      <div className="source-map-filter-summary">
        <CodeLineWrapToggle wrapLines={wrapLines} onChange={setWrapLines} />
        <span>{filteredMappings.length} of {mappings.length} mappings</span>
        {(filter || categoryFilter !== '') && <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setFilter(''); setCategoryFilter('') }}>Clear filters</button>}
      <details className="source-map-view-options"><summary>View options & export</summary>
      <div className="source-map-review-controls import-review-toolbar">
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            style={{ border: '1px solid var(--border)' }}
            onClick={() => { void exportCsv() }}
          >
            Export filtered mappings
          </button>

        <label>{sourceOnly ? 'Source scope' : 'Comparison scope'}<select value={contextOnly ? 'context' : 'full'} onChange={event => { setContextOnly(event.target.value === 'context'); setRevealRequestId(value => value + 1) }}><option value="context">Mapped context</option><option value="full">Full document</option></select></label>
        {!sourceOnly && <label>Destination version<select value={destinationView} onChange={event => { setDestinationView(event.target.value as typeof destinationView); setNoOriginTarget(''); setRevealRequestId(value => value + 1) }}><option value="current">Current draft</option><option value="baseline" disabled={!identity.state?.snapshot}>Review baseline</option></select></label>}
        <details><summary>Keyboard shortcuts</summary><p>In the mapping list, use ↑/↓ and Home/End to select. Use [ and ] to move between unacknowledged review items while focus is in this workspace.</p></details>
      </div>
      </details>
      </div>
      {!sourceOnly && destinationView === 'baseline' && <p>Review baseline captured {identity.state?.capturedAt ? new Date(identity.state.capturedAt).toLocaleString() : 'when this script was opened'}. This is a review reference, not a verified original import snapshot.</p>}
      {identity.error && <p role="alert">{identity.error}</p>}

      {!sourceOnly && showNoOrigin && !identity.checking && <div className="source-map-origin-list" aria-label="Destination items without source origin">{dslStatus.withoutOrigin.length ? dslStatus.withoutOrigin.map(path => <button className="btn btn-sm btn-ghost" type="button" key={path} onClick={() => { setNoOriginTarget(path); setRevealRequestId(value => value + 1) }}>{path}</button>) : <p>Every tracked destination item has a source mapping.</p>}</div>}
      <div className={`source-map-workspace ${isTableCollapsed ? '' : 'with-navigator'}`}>
      <div ref={tableWrapRef} className="source-map-table-wrap" data-table-pagination="off" role="region" aria-label="Source mapping table keyboard navigation" tabIndex={0} onKeyDownCapture={handleTableKeyDown} style={{ display: isTableCollapsed ? 'none' : 'block' }}>
        <table className="source-map-table" aria-label="Source conversion mappings">
          <colgroup>
            <col className="source-map-col-source" />
            <col className="source-map-col-function" />
            <col className="source-map-col-target" />
            <col className="source-map-col-status" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" aria-sort={sortField === 'source' ? sortDirection === 'asc' ? 'ascending' : 'descending' : 'none'}><button type="button" onClick={() => handleSort('source')}>Source</button></th>
              <th scope="col" aria-sort={sortField === 'function' ? sortDirection === 'asc' ? 'ascending' : 'descending' : 'none'}><button type="button" onClick={() => handleSort('function')}>Function</button></th>
              <th scope="col" aria-sort={sortField === 'target' ? sortDirection === 'asc' ? 'ascending' : 'descending' : 'none'}><button type="button" onClick={() => handleSort('target')}>{sourceOnly ? 'Original import path' : 'Imported target'}</button></th>
              <th scope="col" aria-sort={sortField === 'status' ? sortDirection === 'asc' ? 'ascending' : 'descending' : 'none'}><button type="button" onClick={() => handleSort('status')}>Status</button></th>
            </tr>
          </thead>
          <tbody>
            {virtualizeRows && virtualPaddingTop > 0 && (
              <tr aria-hidden="true" className="source-map-virtual-spacer">
              <td colSpan={4} aria-hidden="true" style={{ height: virtualPaddingTop, padding: 0, border: 0 }} />
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
                <td className="cell-mono source-map-source-cell" title={formatSourceRange(mapping)}><button type="button" data-mapping-select aria-current={mapping.id === selectedMappingId ? 'true' : undefined} tabIndex={mapping.id === selectedMappingId ? 0 : -1} onClick={event => { event.stopPropagation(); selectMapping(mapping) }}>{formatSourceRange(mapping)}</button></td>
                <td className="source-map-function-cell" title={mapping.function || '-'}>{mapping.function || '-'}</td>
                <td className="cell-mono source-map-target-cell" title={mapping.target_path || mapping.target_name || 'review-only'}>{mapping.target_path || mapping.target_name || 'review-only'}{identity.resolved[mapping.id]?.status === 'moved' && <small>Current: {identity.resolved[mapping.id].path}</small>}{['changed', 'missing', 'ambiguous'].includes(identity.resolved[mapping.id]?.status) && <small>Identity: {identity.resolved[mapping.id].status}</small>}</td>
                <td className="source-map-status-cell">
                  <div className="source-map-status-stack">
                    <span className={`source-map-status ${CATEGORY_CLASS[statusCategory(mapping.status)]}`}>
                      {formatStatusLabel(mapping.status)}
                    </span>
                    {mapping.support_mode && (
                      <span className={`source-map-support-mode ${supportModeClass(mapping.support_mode)}`.trim()}>
                        {formatSupportModeLabel(mapping.support_mode)}
                      </span>
                    )}
                    {!sourceOnly && <span>{identity.state?.decisions[mapping.id] ? acknowledgedMappingIds.has(mapping.id) ? 'Acknowledged' : 'Review decision recorded — inspect details' : 'Not reviewed'}</span>}
                  </div>
                </td>
              </tr>
            ))}
            {virtualizeRows && virtualPaddingBottom > 0 && (
              <tr aria-hidden="true" className="source-map-virtual-spacer">
              <td colSpan={4} aria-hidden="true" style={{ height: virtualPaddingBottom, padding: 0, border: 0 }} />
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

      <div className="source-map-comparison">
      <nav className="lr-mapping-navigation" aria-label="Review mapping navigation">{[-1, 1].map(direction => <button key={direction} type="button" className="btn btn-secondary btn-sm" disabled={!mappings.some(m => ['review', 'review_only', 'unsupported', 'blocked', 'external_required'].includes(m.status))} onClick={() => { const reviewItems = mappings.filter(m => ['review', 'review_only', 'unsupported', 'blocked', 'external_required'].includes(m.status)); const index = reviewItems.findIndex(m => m.id === selectedMappingId); selectMapping(reviewItems[index < 0 ? direction < 0 ? reviewItems.length - 1 : 0 : (index + direction + reviewItems.length) % reviewItems.length]) }}>{direction < 0 ? 'Previous review mapping' : 'Next review mapping'}</button>)}<details className="source-map-status-help"><summary>About review status</summary><p>{sourceOnly ? 'Conversion coverage describes original import mappings. It does not establish native replay or current draft readiness.' : 'Conversion coverage describes source mappings, not import issues or execution readiness. Not reviewed means no acknowledgement has been recorded. Review acknowledgement does not resolve blockers.'}</p></details></nav>
      {unmappedIssueFocus && <p className="source-map-issue-focus" role="status">No direct source mapping was identified for “{unmappedIssueFocus.title}”. {unmappedIssueFocus.filePath ? `Showing ${unmappedIssueFocus.filePath}${unmappedIssueFocus.line ? `:${unmappedIssueFocus.line}` : ''} for inspection.` : 'Select a captured source file to inspect this finding.'}</p>}
      {selectedMapping && !noOriginTarget && <div className="source-map-selected-details">
        <strong>{selectedMapping.function || formatSourceRange(selectedMapping)}</strong>
        <span>{sourceOnly ? 'Original conversion' : 'Conversion'}: {formatStatusLabel(selectedMapping.status)} · {sourceOnly ? 'Original support mode' : 'Runtime support'}: {formatSupportModeLabel(selectedMapping.support_mode) || 'Not specified'}</span>
        {sourceOnly ? <span>Original import path: <code>{selectedMapping.target_path || 'Unavailable'}</code></span> : <span>Destination identity: {identity.checking ? 'Verifying…' : identity.resolved[selectedMapping.id]?.status || 'Unavailable'}</span>}
        {selectedMapping.notes && <p>{selectedMapping.notes}</p>}{!sourceOnly && onRepairTarget && selectedMapping.target_path && (!repairPaths || repairPaths.includes(selectedMapping.target_path)) && <button type="button" className="btn btn-primary btn-sm" onClick={() => onRepairTarget(selectedMapping.target_path!)}>Review related import issue</button>}
        {!filteredMappings.some(mapping => mapping.id === selectedMapping.id) && <p role="status">The selected mapping is outside the current filters. Clear filters to reveal it in the list.</p>}
        {!sourceOnly && <MappingReviewDetails key={selectedMapping.id + (identity.state?.decisions[selectedMapping.id]?.reviewedAt || '') + (identity.resolved[selectedMapping.id]?.path || '')} mapping={selectedMapping} script={script} state={identity.state} resolution={identity.resolved[selectedMapping.id]} onChange={onChangeReview} dirty={draftDirty} />}
      </div>}
      {!sourceOnly && onEditTarget && destinationPath && destinationView === 'current' && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onEditTarget(destinationPath)}>Edit destination in Author</button>}
      {exportError && <div className="scenario-script-status error" role="alert">{exportError}</div>}

      <div className="source-map-panels">
        <div className={`source-map-panel ${maximizedPane === 'dsl' ? 'hidden' : maximizedPane === 'source' ? 'maximized' : ''}`}>
          <div className="source-map-panel-header">
            <strong title={selectedFile?.path}>Original source · {selectedFile?.path || 'Unavailable'}</strong>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('source', sourceExcerpt)} disabled={!copySupported || !sourceExcerpt}>
                {copied === 'source' ? 'Copied' : 'Copy mapped source'}
              </button>
              <button type="button" className="btn btn-sm btn-ghost" disabled={!copySupported || !selectedFile} onClick={() => void copyValue('source', selectedFile?.content || '')}>Copy whole source file</button>
              {!sourceOnly && <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMaximizedPane(maximizedPane === 'source' ? null : 'source')}>
                {maximizedPane === 'source' ? 'Return to comparison' : 'Expand source'}
              </button>}
            </div>
          </div>
          {selectedFile && !noOriginTarget ? (
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
                wrapLines={wrapLines}
                contextLines={contextOnly ? 4 : undefined}
                revealRequestId={revealRequestId}
                revealOnMount
              />
            </Suspense>
          ) : (
            <div className="source-map-empty">{noOriginTarget ? 'This destination item has no source mapping.' : 'No source file content was captured.'}</div>
          )}
        </div>

        {!sourceOnly && <div className={`source-map-panel ${maximizedPane === 'source' ? 'hidden' : maximizedPane === 'dsl' ? 'maximized' : ''}`}>
          <div className="source-map-panel-header">
            <strong title={destinationPath}>Destination · {destinationView === 'baseline' ? 'Review baseline' : 'Current draft'} · {destinationPath || 'No verified mapping'}</strong>
            <div style={{ display: 'flex', gap: '8px' }}>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('dsl', dslExcerpt)} disabled={!copySupported || !dslExcerpt}>
                {copied === 'dsl' ? 'Copied' : 'Copy mapped destination'}
              </button>
              <button type="button" className="btn btn-sm btn-ghost" disabled={!copySupported || !dslDocument} onClick={() => void copyValue('dsl', dslDocument?.text || '')}>Copy whole destination</button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setMaximizedPane(maximizedPane === 'dsl' ? null : 'dsl')}>
                {maximizedPane === 'dsl' ? 'Return to comparison' : 'Expand destination'}
              </button>
            </div>
          </div>
          {dslDocument && destinationPath && dslActiveRange ? (
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
                wrapLines={wrapLines}
                contextLines={contextOnly ? 4 : undefined}
                revealRequestId={revealRequestId}
                revealOnMount
              />
            </Suspense>
          ) : (
            <div className="source-map-empty">{!dslDocument ? 'Destination is not available yet.' : identity.checking ? 'Verifying mapping identity…' : !selectedMapping ? unmappedIssueFocus ? 'No destination mapping is linked to this finding. Other mappings for this source file are listed above.' : 'This captured file has no destination mappings. Select a mapped file or inspect destination items without source origin.' : !selectedMapping.target_path && !identity.resolved[selectedMapping.id]?.path ? 'This source construct has no mapped destination. Its conversion status and review notes explain what needs attention.' : 'No verified destination. Inspect mapping details to re-link an edited, removed, or ambiguous target.'}</div>
          )}
        </div>}
      </div>
      </div>
      </div>
    </section>
  )
}
