import { useCallback, useState } from 'react'

interface ExecutionViewStateModel {
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  expandedScenarioScript: string | null
  onExpandedScenarioScriptChange: (value: string | null) => void
}

export function useExecutionViewState(): ExecutionViewStateModel {
  const [expandedChart, setExpandedChart] = useState<string | null>(null)
  const [expandedScenarioScript, setExpandedScenarioScript] = useState<string | null>(null)

  const onExpandedChartChange = useCallback((value: string | null) => {
    setExpandedChart(value)
  }, [])

  const onExpandedScenarioScriptChange = useCallback((value: string | null) => {
    setExpandedScenarioScript(value)
  }, [])

  return {
    expandedChart,
    onExpandedChartChange,
    expandedScenarioScript,
    onExpandedScenarioScriptChange,
  }
}