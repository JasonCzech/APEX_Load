import type { SourceReviewMapping } from '../types/scripts'
import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import {
  buildImportedCorrelationFocusTargets,
  buildImportedDraftDiffSummary,
  buildImportedParameterFileMappings,
  buildWeb2ObjectDescriptorReview,
  findSourceMappingForAction,
  formatRuntimeSettingStatusLabel,
  getImportSummary,
  readSourceReview,
  readTrueClientPromotionReview,
} from './scriptEditorImportModel'
import { scriptHasImportMetadata, type EditorHistory } from './scriptEditorState'
import { summarizeInlineData } from './scriptEditorUtils'
import type { CenterWorkbenchTabId } from './scriptEditorWorkbench'

interface UseScriptImportReviewControllerOptions {
  activeCenterTabId: CenterWorkbenchTabId
  editorHistory: EditorHistory
  importBaselineScript: ScriptDefinition | null
  onWorkspaceMaximizedChange: (maximized: boolean) => void
  script: ScriptDefinition
  scriptId?: string | null
}

export function useScriptImportReviewController({
  activeCenterTabId,
  editorHistory,
  importBaselineScript,
  onWorkspaceMaximizedChange,
  script,
  scriptId,
}: UseScriptImportReviewControllerOptions) {
  const [importReviewCollapsed, setImportReviewCollapsed] = useState(Boolean(scriptId))
  const [showImportedParameterMappingsDialog, setShowImportedParameterMappingsDialog] = useState(false)
  const [showMetadataJsonDialog, setShowMetadataJsonDialog] = useState(false)
  const [sourceMappingWorkspaceMaximized, setSourceMappingWorkspaceMaximized] = useState(false)
  const [sourceMappingFocus, setSourceMappingFocus] = useState({ mappingId: '', requestId: 0 })

  const importMetadata = scriptHasImportMetadata(script) ? script.metadata?.import : undefined
  const importSummary = useMemo(() => getImportSummary(importMetadata), [importMetadata])
  const sourceReview = useMemo(() => readSourceReview(importMetadata), [importMetadata])
  const trueClientPromotionReview = useMemo(() => readTrueClientPromotionReview(importMetadata), [importMetadata])
  const objectDescriptorReview = useMemo(
    () => buildWeb2ObjectDescriptorReview(script, importMetadata),
    [importMetadata, script],
  )
  const importedCorrelationFocusTargets = useMemo(
    () => buildImportedCorrelationFocusTargets(script.actions, importSummary.correlationSuggestions, sourceReview),
    [importSummary.correlationSuggestions, script.actions, sourceReview],
  )
  const importedParameterFileMappings = useMemo(
    () => buildImportedParameterFileMappings(script, importSummary),
    [importSummary, script],
  )
  const importedDataSourceSummaries = useMemo(() => (script.data_sources || []).map((source, index) => {
    const summary = summarizeInlineData(source)
    const columnDetails = source.first_row_header === false ? source.columns || [] : []
    return {
      name: source.name || `source_${index + 1}`,
      type: source.type || 'csv',
      rows: summary.rows,
      columns: columnDetails.length > 0 ? columnDetails : summary.columns,
    }
  }), [script.data_sources])
  const runtimeSettingParitySummary = useMemo(() => Object.entries(importSummary.runtimeSettingStatusCounts)
    .filter(([, count]) => count > 0)
    .map(([status, count]) => `${count} ${formatRuntimeSettingStatusLabel(status)}`)
    .join(', '), [importSummary.runtimeSettingStatusCounts])
  const runtimeSettingReadinessSummary = importSummary.runtimeSettingReadiness
    ? importSummary.runtimeSettingReadiness.summary || `${importSummary.runtimeSettingReadiness.score_pct}% readiness`
    : ''
  const reviewBaselineScript = importBaselineScript || editorHistory.past[editorHistory.past.length - 1] || null
  const draftDiffSummary = useMemo(
    () => reviewBaselineScript ? buildImportedDraftDiffSummary(reviewBaselineScript, script) : null,
    [reviewBaselineScript, script],
  )

  useEffect(() => {
    if (importedParameterFileMappings.length > 0) return
    setShowImportedParameterMappingsDialog(false)
  }, [importedParameterFileMappings.length])

  useEffect(() => {
    if (!sourceMappingWorkspaceMaximized) return
    if (activeCenterTabId === 'imported-metadata' && sourceReview) return
    setSourceMappingWorkspaceMaximized(false)
    onWorkspaceMaximizedChange(false)
  }, [activeCenterTabId, onWorkspaceMaximizedChange, sourceMappingWorkspaceMaximized, sourceReview])

  const toggleSourceMappingWorkspaceMaximized = useCallback(() => {
    setSourceMappingWorkspaceMaximized(current => {
      const next = !current
      onWorkspaceMaximizedChange(next)
      return next
    })
  }, [onWorkspaceMaximizedChange])

  const focusSourceMapping = useCallback((mapping: SourceReviewMapping) => {
    const matches = sourceReview?.mappings?.filter(candidate => candidate.id === mapping.id) || []
    if (matches.length !== 1 || matches[0].source_file !== mapping.source_file || matches[0].target_path !== mapping.target_path) return
    setSourceMappingFocus(previous => ({ mappingId: mapping.id, requestId: previous.requestId + 1 }))
  }, [sourceReview])

  const focusSourceMappingForAction = useCallback((actionIndex: number) => {
    const mapping = findSourceMappingForAction(sourceReview, script.actions[actionIndex], actionIndex)
    if (!mapping) return
    setSourceMappingFocus(previous => ({
      mappingId: mapping?.id || '',
      requestId: previous.requestId + 1,
    }))
    window.requestAnimationFrame(() => {
      document.getElementById('imported-source-mapping')?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    })
  }, [script.actions, sourceReview])

  return {
    draftDiffSummary,
    focusSourceMappingForAction,
    focusSourceMapping,
    importMetadata,
    importedCorrelationFocusTargets,
    importedDataSourceSummaries,
    importedParameterFileMappings,
    importReviewCollapsed,
    importSummary,
    objectDescriptorReview,
    reviewBaselineScript,
    runtimeSettingParitySummary,
    runtimeSettingReadinessSummary,
    setImportReviewCollapsed,
    setShowImportedParameterMappingsDialog,
    setShowMetadataJsonDialog,
    showImportedParameterMappingsDialog,
    showMetadataJsonDialog,
    sourceMappingFocus,
    sourceMappingWorkspaceMaximized,
    sourceReview,
    toggleSourceMappingWorkspaceMaximized,
    trueClientPromotionReview,
  }
}
