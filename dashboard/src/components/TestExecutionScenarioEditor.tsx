import { useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { ScenarioDraft, StoredScript, TestSummary } from '../types'
import CreateTestModal from './CreateTestModal'

interface Props {
  open: boolean
  test: TestSummary | null
  onClose: () => void
  onSelectTest: (testId: string) => void
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

export default function TestExecutionScenarioEditor({ open, test, onClose, onSelectTest }: Props) {
  const api = useApi()
  const [availableScripts, setAvailableScripts] = useState<StoredScript[]>([])
  const initialScenario = useMemo(() => (test ? buildScenarioDraft(test) : undefined), [test])

  useEffect(() => {
    if (!open) return

    let cancelled = false

    const loadScripts = async () => {
      try {
        const response = await api.listScripts()
        if (!cancelled) {
          setAvailableScripts(response.scripts || [])
        }
      } catch {
        if (!cancelled) {
          setAvailableScripts([])
        }
      }
    }

    loadScripts()
    return () => {
      cancelled = true
    }
  }, [api, open])

  if (!open || !test || !initialScenario) {
    return null
  }

  return (
    <CreateTestModal
      scripts={availableScripts}
      initialScenario={initialScenario}
      onClose={onClose}
      onCreated={(createdTest) => {
        onClose()
        if (createdTest?.id) {
          onSelectTest(createdTest.id)
        }
      }}
    />
  )
}