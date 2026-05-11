import { useState, useEffect, useCallback, useMemo, memo } from 'react'
import { useApi } from '../hooks/useApi'
import { Folder, Project, ScenarioDraft, TestSummary, TestStatus, StoredScript } from '../types'
import CreateTestModal from './CreateTestModal'
import FolderTree from './FolderTree'
import StatusBadge from './StatusBadge'
import './TestHub.css'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  activeProject?: Project | null
}

interface Scenario {
  name: string
  runs: TestSummary[]
  latestRun: TestSummary
  configRun: TestSummary
  hasActive: boolean
  totalRuns: number
}

function resolveStatus(test: TestSummary, overrides: Record<string, string>): TestStatus {
  return (overrides[test.id] || test.status) as TestStatus
}

function formatDate(iso: string): string {
  const d = new Date(iso)
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function resolveScenarioConfigRun(runs: TestSummary[]): TestSummary {
  return runs.find((run) => run.status === 'QUEUED') || runs[0]
}

function hasShakeoutFailure(test: TestSummary): boolean {
  return test.status === 'FAILED' && Boolean(test.script_shakeout) && Boolean(test.error?.toLowerCase().includes('shakeout'))
}

function buildScenarioDraft(test: TestSummary): ScenarioDraft {
  const groups = test.groups?.length
    ? test.groups.map((group) => ({
        name: group.name,
        scriptId: group.script_id,
        vusers: group.vusers,
        rampUp: group.ramp_up || '',
        rampDown: group.ramp_down || '',
        initialization: group.initialization || 'at_runtime',
      }))
    : [{
        name: '',
        scriptId: test.script_id || '',
        vusers: test.total_vusers || 10,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime' as const,
      }]

  return {
    name: test.name,
    duration: test.duration,
    scenarioMode: test.scenario_mode || 'vuser_driven',
    scriptShakeout: Boolean(test.script_shakeout),
    resourceBaseline: Boolean(test.resource_baseline),
    goalConfig: {
      target_average_tps: test.goal_config?.target_average_tps,
      p95_latency_ms: test.goal_config?.p95_latency_ms,
      max_error_pct: test.goal_config?.max_error_pct,
      min_vusers: test.goal_config?.min_vusers,
      max_vusers: test.goal_config?.max_vusers,
      stop_on_sla_breach: test.goal_config?.stop_on_sla_breach,
      sla_breach_window: test.goal_config?.sla_breach_window || '',
    },
    groups,
    runtimeSettings: {
      iterations: test.runtime_settings?.iterations,
      log_level: test.runtime_settings?.log_level || '',
      auto_transactions: test.runtime_settings?.auto_transactions || '',
      snapshot_on_error: Boolean(test.runtime_settings?.error_handling?.snapshot_on_error),
    },
  }
}

function TestHub({ testStatuses, onSelectTest, activeProject }: Props) {
  const api = useApi()
  const [tests, setTests] = useState<TestSummary[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [folders, setFolders] = useState<Folder[]>([])
  const [selectedFolderId, setSelectedFolderId] = useState<string | null | undefined>(undefined)
  const [showCreate, setShowCreate] = useState(false)
  const [editingScenario, setEditingScenario] = useState<ScenarioDraft | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [testRes, scriptRes] = await Promise.all([
        api.listTests(activeProject?.id),
        api.listScripts(activeProject?.id),
      ])
      const folderRes = activeProject?.id ? await api.listFolders(activeProject.id, 'scenarios') : { folders: [] }
      setTests(testRes.tests || [])
      setScripts(scriptRes.scripts || [])
      setFolders(folderRes.folders || [])
    } catch { /* API not available yet */ }
  }, [api, activeProject?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 5000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    setSelectedFolderId(undefined)
  }, [activeProject?.id])

  const scenarios = useMemo(() => {
    const map = new Map<string, TestSummary[]>()
    const visibleTests = tests.filter((test) => {
      if (selectedFolderId === undefined) return true
      if (selectedFolderId === null) return !test.folder_id
      return test.folder_id === selectedFolderId
    })

    for (const test of visibleTests) {
      const name = test.name
      if (!map.has(name)) map.set(name, [])
      map.get(name)!.push(test)
    }

    const result: Scenario[] = []
    for (const [name, runs] of map) {
      const sorted = [...runs].sort((a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      )
      const configRun = resolveScenarioConfigRun(sorted)
      const hasActive = sorted.some(r => {
        const s = resolveStatus(r, testStatuses)
        return s === 'RUNNING' || s === 'INITIALIZING' || s === 'STOPPING'
      })
      result.push({
        name,
        runs: sorted,
        latestRun: sorted[0],
        configRun,
        hasActive,
        totalRuns: sorted.length,
      })
    }

    result.sort((a, b) => {
      if (a.hasActive && !b.hasActive) return -1
      if (!a.hasActive && b.hasActive) return 1
      return new Date(b.latestRun.created_at).getTime() - new Date(a.latestRun.created_at).getTime()
    })

    return result
  }, [tests, testStatuses, selectedFolderId])

  const scenarioItems = useMemo(
    () => scenarios.map((scenario) => ({
      id: scenario.configRun.id,
      name: scenario.name,
      folder_id: scenario.configRun.folder_id,
    })),
    [scenarios],
  )

  const createFolder = async (parentId: string | null) => {
    if (!activeProject?.id) return
    const name = window.prompt('Folder name')
    if (!name?.trim()) return
    await api.createFolder(activeProject.id, { name: name.trim(), category: 'scenarios', parent_id: parentId })
    refresh()
  }

  const renameFolder = async (id: string, name: string) => {
    await api.updateFolder(id, { name })
    refresh()
  }

  const deleteFolder = async (id: string) => {
    await api.deleteFolder(id)
    if (selectedFolderId === id) setSelectedFolderId(undefined)
    refresh()
  }

  const moveScenario = async (id: string, folderId: string | null) => {
    await api.moveTest(id, folderId)
    refresh()
  }

  const openCreate = () => {
    setEditingScenario(null)
    setShowCreate(true)
  }

  const openEdit = (test: TestSummary) => {
    setEditingScenario(buildScenarioDraft(test))
    setShowCreate(true)
  }

    <div className="page">
      <FolderTree
        title="Scenarios"
        rootLabel={activeProject?.name || 'Root'}
        headerActions={<button className="btn btn-sm btn-primary" onClick={openCreate}>+ New Scenario</button>}
        category="scenarios"
        folders={folders}
        items={scenarioItems}
        selectedFolderId={selectedFolderId}
        onSelectFolder={setSelectedFolderId}
        onSelectItem={onSelectTest}
        onCreateFolder={createFolder}
        onRenameFolder={renameFolder}
        onDeleteFolder={deleteFolder}
        onMoveItem={moveScenario}
        renderItemExtra={(item) => {
          const scenario = scenarios.find((s) => s.configRun.id === item.id)
          if (!scenario) return null
          const latestStatus = resolveStatus(scenario.latestRun, testStatuses)
          return (
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginLeft: 'auto', flexShrink: 0 }}>
              <span className="scenario-meta" style={{ fontSize: 12 }}>
                {scenario.totalRuns} run{scenario.totalRuns !== 1 ? 's' : ''} · {scenario.latestRun.total_vusers} VUsers
              </span>
              <StatusBadge status={latestStatus} />
              <button
                className="btn btn-sm btn-ghost"
                onClick={(e) => { e.stopPropagation(); openEdit(scenario.latestRun) }}
              >
                Edit
              </button>
            </div>
          )
        }}
      />

      {showCreate && (
        <CreateTestModal
          scripts={scripts}
          projectId={activeProject?.id}
          folderId={selectedFolderId || null}
          initialScenario={editingScenario || undefined}
          onClose={() => {
            setShowCreate(false)
            setEditingScenario(null)
          }}
          onCreated={(test) => {
            setShowCreate(false)
            setEditingScenario(null)
            refresh()
            onSelectTest(test.id)
          }}
        />
      )}
    </div>
  )
}
export default memo(TestHub)
