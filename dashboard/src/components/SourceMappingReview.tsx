import { useEffect, useMemo, useState } from 'react'

import { type LoadRunnerSourceReview, type LoadRunnerSourceReviewFile, type LoadRunnerSourceReviewMapping, type ScriptDefinition } from '../types/scripts'
import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import './SourceMappingReview.css'

type SortField = 'source' | 'function' | 'target' | 'status'
type SortDirection = 'asc' | 'desc'

interface Props {
  sourceReview?: LoadRunnerSourceReview | null
  script?: ScriptDefinition | null
  title?: string
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
      return mapping.status || ''
    default:
      return `${mapping.source_file || ''}:${String(mapping.start_line || 0).padStart(8, '0')}`
  }
}

function formatStatusLabel(status?: string) {
  return (status || 'review').replace(/_/g, ' ')
}

function formatSourceRange(mapping?: LoadRunnerSourceReviewMapping | null) {
  if (!mapping) return 'No mapping selected'
  if (!mapping.start_line) return mapping.source_file || 'Source'
  if (mapping.end_line && mapping.end_line !== mapping.start_line) {
    return `${mapping.source_file}:${mapping.start_line}-${mapping.end_line}`
  }
  return `${mapping.source_file}:${mapping.start_line}`
}

function resolvePathValue(script: ScriptDefinition | null | undefined, targetPath?: string): unknown {
  if (!script || !targetPath) return null
  const normalized = targetPath.replace(/\[(\d+)\]/g, '.$1')
  const parts = normalized.split('.').filter(Boolean)
  let current: unknown = script
  for (const part of parts) {
    if (current == null) return null
    if (Array.isArray(current)) {
      const index = Number(part)
      current = Number.isInteger(index) ? current[index] : undefined
      continue
    }
    if (typeof current === 'object') {
      current = (current as Record<string, unknown>)[part]
      continue
    }
    return null
  }
  return current ?? null
}

function buildDslSnippet(script: ScriptDefinition | null | undefined, mapping?: LoadRunnerSourceReviewMapping | null) {
  if (!mapping) return ''
  const value = resolvePathValue(script, mapping.target_path)
  if (value !== null && value !== undefined) {
    return JSON.stringify(value, null, 2)
  }
  return JSON.stringify({
    target_path: mapping.target_path || 'review-only',
    target_name: mapping.target_name || '',
    status: mapping.status,
    notes: mapping.notes || '',
  }, null, 2)
}

function buildSourceExcerpt(file: LoadRunnerSourceReviewFile | null, mapping?: LoadRunnerSourceReviewMapping | null) {
  if (!file || !mapping) return ''
  const lines = file.content.split('\n')
  const start = Math.max(1, mapping.start_line || 1)
  const end = Math.max(start, mapping.end_line || start)
  return lines.slice(start - 1, end).join('\n')
}

function SourcePreview({ file, selectedMapping }: { file: LoadRunnerSourceReviewFile | null; selectedMapping?: LoadRunnerSourceReviewMapping | null }) {
  if (!file) {
    return <div className="source-map-empty">No source file content was captured.</div>
  }
  const start = selectedMapping && fileMatchesPath(file, selectedMapping.source_file) ? selectedMapping.start_line : 0
  const end = selectedMapping && fileMatchesPath(file, selectedMapping.source_file) ? selectedMapping.end_line || selectedMapping.start_line : 0
  return (
    <div className="source-map-code" role="region" aria-label={`${file.path} source`}>
      {file.content.split('\n').map((line, index) => {
        const lineNumber = index + 1
        const highlighted = start > 0 && lineNumber >= start && lineNumber <= end
        return (
          <div key={`${file.path}:${lineNumber}`} className={`source-map-code-line ${highlighted ? 'highlighted' : ''}`}>
            <span className="source-map-line-number">{lineNumber}</span>
            <code>{line || ' '}</code>
          </div>
        )
      })}
    </div>
  )
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
    () => mappings.find(mapping => mapping.id === selectedMappingId) || mappings[0] || null,
    [mappings, selectedMappingId],
  )
  const selectedFile = useMemo(
    () => findSourceFile(files, selectedFilePath || selectedMapping?.source_file),
    [files, selectedFilePath, selectedMapping?.source_file],
  )

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
        mapping.notes,
      ].some(value => String(value || '').toLowerCase().includes(needle)))
      : mappings
    return [...next].sort((a, b) => compareText(mappingSortValue(a, sortField), mappingSortValue(b, sortField), sortDirection))
  }, [filter, mappings, sortDirection, sortField])

  if (files.length === 0 && mappings.length === 0) {
    return null
  }

  const dslSnippet = buildDslSnippet(script, selectedMapping)
  const sourceExcerpt = buildSourceExcerpt(selectedFile, selectedMapping)
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

  return (
    <section className="source-map-review">
      <div className="source-map-header">
        <div>
          <h4>{title}</h4>
          <p>Compare redacted VuGen source with the generated APEX Load DSL.</p>
        </div>
        <div className="source-map-summary">
          <span>{files.length} file{files.length === 1 ? '' : 's'}</span>
          <span>{mappings.length} mapping{mappings.length === 1 ? '' : 's'}</span>
        </div>
      </div>

      <div className="source-map-toolbar">
        <label>
          <span>Source File</span>
          <select value={selectedFile?.path || ''} onChange={event => setSelectedFilePath(event.target.value)}>
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
                onClick={() => {
                  setSelectedMappingId(mapping.id)
                  setSelectedFilePath(mapping.source_file)
                  setCopied('')
                }}
              >
                <td className="cell-mono">{formatSourceRange(mapping)}</td>
                <td>{mapping.function || '-'}</td>
                <td className="cell-mono">{mapping.target_path || mapping.target_name || 'review-only'}</td>
                <td><span className={`source-map-status ${mapping.status || 'review'}`}>{formatStatusLabel(mapping.status)}</span></td>
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
          <SourcePreview file={selectedFile} selectedMapping={selectedMapping} />
        </div>

        <div className="source-map-panel">
          <div className="source-map-panel-header">
            <strong>{selectedMapping?.target_path || 'Generated DSL'}</strong>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => void copyValue('dsl', dslSnippet)} disabled={!copySupported || !dslSnippet}>
              {copied === 'dsl' ? 'Copied' : 'Copy DSL'}
            </button>
          </div>
          <pre className="source-map-dsl">{dslSnippet || 'No generated DSL target selected.'}</pre>
        </div>
      </div>
    </section>
  )
}
