import { useCallback, useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { buildLiveControlPreview, LiveControlPreview } from './testExecutionLiveControlModel'

interface ExecutionLiveControlsModel {
  showControls: boolean
  onToggleControls: () => void
  vuserTargets: Record<string, number>
  onVuserTargetChange: (groupName: string, value: number) => void
  durationInput: string
  onDurationInputChange: (value: string) => void
  onScaleVUsers: (groupName: string) => Promise<void>
  onAdjustDuration: () => Promise<void>
  liveControlPreview: LiveControlPreview
}

export function useExecutionLiveControls(
  testId: string,
  test: TestSummary | null,
  showControlFeedback: (message: string) => void,
): ExecutionLiveControlsModel {
  const api = useApi()
  const [showControls, setShowControls] = useState(false)
  const [vuserTargets, setVuserTargets] = useState<Record<string, number>>({})
  const [durationInput, setDurationInput] = useState('')

  useEffect(() => {
    if (!test?.groups) return

    const targets: Record<string, number> = {}
    test.groups.forEach((group) => {
      targets[group.name] = group.vusers
    })

    setVuserTargets(targets)
    setDurationInput(test.duration || '')
  }, [test?.duration, test?.groups, test?.id])

  const onScaleVUsers = useCallback(async (groupName: string) => {
    const target = vuserTargets[groupName]
    if (target === undefined || target < 0) return

    try {
      await api.scaleVUsers(testId, groupName, target)
      showControlFeedback(`Scaled ${groupName} to ${target} VUsers`)
    } catch (error: unknown) {
      showControlFeedback(`Error: ${getErrorMessage(error, 'Unable to scale VUsers')}`)
    }
  }, [api, showControlFeedback, testId, vuserTargets])

  const onAdjustDuration = useCallback(async () => {
    if (!durationInput) return

    try {
      await api.adjustDuration(testId, durationInput)
      showControlFeedback(`Duration adjusted to ${durationInput}`)
    } catch (error: unknown) {
      showControlFeedback(`Error: ${getErrorMessage(error, 'Unable to adjust duration')}`)
    }
  }, [api, durationInput, showControlFeedback, testId])

  const liveControlPreview = useMemo(() => {
    return buildLiveControlPreview({
      test,
      durationInput,
      vuserTargets,
    })
  }, [durationInput, test, vuserTargets])

  const onToggleControls = useCallback(() => {
    setShowControls((current) => !current)
  }, [])

  const onVuserTargetChange = useCallback((groupName: string, value: number) => {
    setVuserTargets((prev) => ({ ...prev, [groupName]: value }))
  }, [])

  return {
    showControls,
    onToggleControls,
    vuserTargets,
    onVuserTargetChange,
    durationInput,
    onDurationInputChange: setDurationInput,
    onScaleVUsers,
    onAdjustDuration,
    liveControlPreview,
  }
}