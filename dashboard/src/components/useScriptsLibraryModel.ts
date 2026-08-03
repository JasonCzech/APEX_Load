import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { StoredScript, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { loadAllTestSummaries } from './testCatalogPagination'

type SortValue = string | number

export type ScriptLibrarySortField = 'name' | 'protocol' | 'version' | 'authoring_health' | 'created_at' | 'scenario_count' | 'associated_releases' | 'scenario_run_releases' | 'last_run_at'
export type ScriptRunSortField = 'name' | 'status' | 'scenario_id' | 'release_id' | 'start_time' | 'duration'

interface UseScriptsLibraryModelOptions {
  api: ReturnType<typeof useApi>
  scripts: StoredScript[]
  projectId?: string
}

interface ScriptsLibraryModel {
  sortField: ScriptLibrarySortField
  sortDirection: 'asc' | 'desc'
  sortedScripts: StoredScript[]
  onSort: (field: ScriptLibrarySortField) => void
  expandedScriptId: string | null
  scriptRuns: Record<string, TestSummary[]>
  scriptRunErrors: Record<string, string>
  loadingRunsFor: string | null
  onToggleScriptRuns: (script: StoredScript) => Promise<void>
  onRetryScriptRuns: (script: StoredScript) => Promise<void>
  innerSortField: ScriptRunSortField
  innerSortDirection: 'asc' | 'desc'
  onInnerSort: (field: ScriptRunSortField) => void
  getSortedInnerRuns: (runs: TestSummary[]) => TestSummary[]
}

function getScriptSortValue(script: StoredScript, field: ScriptLibrarySortField): SortValue {
  if (field === 'created_at' || field === 'last_run_at') {
    return new Date(script[field] || 0).getTime()
  }
  if (field === 'scenario_count') {
    return script.scenario_count || 0
  }
  if (field === 'associated_releases') {
    return (script.associated_releases || []).length
  }
  if (field === 'scenario_run_releases') {
    return (script.scenario_run_releases || []).length
  }
  if (field === 'authoring_health') {
    return script.authoring_health?.score ?? -1
  }
  return String(script[field] || '').toLowerCase()
}

function getRunSortValue(run: TestSummary, field: ScriptRunSortField): SortValue {
  if (field === 'start_time') {
    return new Date(run.start_time || run.created_at || 0).getTime()
  }
  return String(run[field] || '').toLowerCase()
}

export function useScriptsLibraryModel({ api, scripts, projectId }: UseScriptsLibraryModelOptions): ScriptsLibraryModel {
  const [sortField, setSortField] = useState<ScriptLibrarySortField>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [expandedScriptId, setExpandedScriptId] = useState<string | null>(null)
  const [scriptRuns, setScriptRuns] = useState<Record<string, TestSummary[]>>({})
  const [scriptRunErrors, setScriptRunErrors] = useState<Record<string, string>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<ScriptRunSortField>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')
  const currentProjectIDRef = useRef(projectId)
  const projectScopeGenerationRef = useRef(0)
  currentProjectIDRef.current = projectId

  useEffect(() => {
    projectScopeGenerationRef.current += 1
    setExpandedScriptId(null)
    setScriptRuns({})
    setScriptRunErrors({})
    setLoadingRunsFor(null)
  }, [projectId])

  const sortedScripts = useMemo(() => {
    return [...scripts].sort((a, b) => {
      const aValue = getScriptSortValue(a, sortField)
      const bValue = getScriptSortValue(b, sortField)
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scripts, sortDirection, sortField])

  const onSort = useCallback((field: ScriptLibrarySortField) => {
    if (sortField === field) {
      setSortDirection((prev) => (prev === 'asc' ? 'desc' : 'asc'))
      return
    }

    setSortField(field)
    setSortDirection('asc')
  }, [sortField])

  const loadScriptRuns = useCallback(async (script: StoredScript) => {
    if (loadingRunsFor === script.id) return
    const requestProjectID = projectId
    const requestScopeGeneration = projectScopeGenerationRef.current
    const isCurrentProjectScope = () => (
      currentProjectIDRef.current === requestProjectID
      && projectScopeGenerationRef.current === requestScopeGeneration
    )
    setLoadingRunsFor(script.id)
    setScriptRunErrors(prev => ({ ...prev, [script.id]: '' }))
    try {
      const res = await loadAllTestSummaries(api.listTests, projectId ? { project_id: projectId, summary: true } : { summary: true })
      if (!isCurrentProjectScope()) return
      const linkedRuns = (res.tests || [])
        .filter((test: TestSummary) => !!test.scenario_id && (test.groups || []).some((group) => group.script_id === script.id))
        .sort((a: TestSummary, b: TestSummary) => {
          const aTime = new Date(a.start_time || a.created_at || 0).getTime()
          const bTime = new Date(b.start_time || b.created_at || 0).getTime()
          return bTime - aTime
        })
      setScriptRuns((prev) => ({ ...prev, [script.id]: linkedRuns }))
    } catch (error) {
      if (!isCurrentProjectScope()) return
      setScriptRunErrors(prev => ({
        ...prev,
        [script.id]: getErrorMessage(error, 'Unable to load linked scenario runs'),
      }))
    } finally {
      if (isCurrentProjectScope()) setLoadingRunsFor(null)
    }
  }, [api.listTests, loadingRunsFor, projectId])

  const onToggleScriptRuns = useCallback(async (script: StoredScript) => {
    if (expandedScriptId === script.id) {
      setExpandedScriptId(null)
      return
    }

    setExpandedScriptId(script.id)
    if (scriptRuns[script.id] || scriptRunErrors[script.id]) return
    await loadScriptRuns(script)
  }, [expandedScriptId, loadScriptRuns, scriptRunErrors, scriptRuns])

  const onRetryScriptRuns = useCallback(async (script: StoredScript) => {
    setExpandedScriptId(script.id)
    await loadScriptRuns(script)
  }, [loadScriptRuns])

  const onInnerSort = useCallback((field: ScriptRunSortField) => {
    if (innerSortField === field) {
      setInnerSortDirection((prev) => (prev === 'asc' ? 'desc' : 'asc'))
      return
    }

    setInnerSortField(field)
    setInnerSortDirection('asc')
  }, [innerSortField])

  const getSortedInnerRuns = useCallback((runs: TestSummary[]) => {
    return [...runs].sort((a, b) => {
      const aValue = getRunSortValue(a, innerSortField)
      const bValue = getRunSortValue(b, innerSortField)
      if (aValue < bValue) return innerSortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return innerSortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [innerSortDirection, innerSortField])

  return {
    sortField,
    sortDirection,
    sortedScripts,
    onSort,
    expandedScriptId,
    scriptRuns,
    scriptRunErrors,
    loadingRunsFor,
    onToggleScriptRuns,
    onRetryScriptRuns,
    innerSortField,
    innerSortDirection,
    onInnerSort,
    getSortedInnerRuns,
  }
}
