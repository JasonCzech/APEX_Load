import { useEffect, useMemo, useState } from 'react'

import { useApi } from '../../hooks/useApi'
import type { RunArchiveReport } from '../../types'
import type { RcaDetection } from '../../types/rca'
import { rcaLoadSteps, rcaPhaseMarkers, rcaTimelinePhases, type RcaPhaseMarker } from './rcaModel'

// useRcaPhases reads a run's archived load-phase events for the root-cause
// timelines. A run without level events falls back to steps inferred from
// the detection levels; while the archived events load, no steps are shown.
export function useRcaPhases(testId: string, report: RunArchiveReport | null, detection?: RcaDetection, enabled = true): { phases: RcaPhaseMarker[]; inferred: boolean } {
  const api = useApi()
  const inferredSteps = useMemo(() => rcaLoadSteps(detection?.levels || [], detection?.params?.level_settle_ns), [detection])
  const phaseFile = enabled ? report?.manifest?.phase_events_file || '' : ''
  // undefined while the archived events load; null when the run has none or
  // they could not be read.
  const [phases, setPhases] = useState<RcaPhaseMarker[] | null | undefined>(undefined)
  useEffect(() => {
    if (!phaseFile) {
      setPhases(null)
      return
    }
    setPhases(undefined)
    const controller = new AbortController()
    api.downloadTestArchiveArtifact(testId, phaseFile, { signal: controller.signal })
      .then(blob => blob.text())
      .then(text => { if (!controller.signal.aborted) setPhases(rcaPhaseMarkers(text)) })
      .catch(() => { if (!controller.signal.aborted) setPhases(null) })
    return () => controller.abort()
  }, [api, phaseFile, testId])
  return useMemo(() => (phases === undefined ? { phases: [], inferred: false } : rcaTimelinePhases(phases, inferredSteps)), [inferredSteps, phases])
}
