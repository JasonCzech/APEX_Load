import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { buildLiveControlPreview, LiveControlPreview, VUserGroupAction } from './testExecutionLiveControlModel'

interface ExecutionLiveControlsModel {
  showControls: boolean
  onToggleControls: () => void
  vuserTargets: Record<string, number>
  onVuserTargetChange: (groupName: string, value: number) => void
  vuserGroupActions: Record<string, VUserGroupAction>
  onVuserGroupActionChange: (groupName: string, value: VUserGroupAction) => void
  vuserRampEnabled: boolean
  onVuserRampEnabledChange: (value: boolean) => void
  vuserRampDuration: string
  onVuserRampDurationChange: (value: string) => void
  durationInput: string
  onDurationInputChange: (value: string) => void
  onScaleVUsers: (groupName: string) => Promise<void>
  onStopAllVUsers: () => Promise<void>
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
  const [vuserGroupActions, setVuserGroupActions] = useState<Record<string, VUserGroupAction>>({})
  const syncedVuserTargetsRef = useRef<Record<string, number>>({})
  const syncedTestIdRef = useRef<string | null>(null)
  const [vuserRampEnabled, setVuserRampEnabled] = useState(false)
  const [vuserRampDuration, setVuserRampDuration] = useState('1m')
  const [durationInput, setDurationInput] = useState('')

  useEffect(() => {
    if (!test?.groups) return

    const isNewTest = syncedTestIdRef.current !== test.id
    setVuserTargets((prev) => {
      const next: Record<string, number> = {}
      const nextSynced: Record<string, number> = {}
      test.groups.forEach((group) => {
        const syncedTarget = test.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers
        const previousValue = isNewTest ? undefined : prev[group.name]
        const previousSyncedValue = isNewTest ? undefined : syncedVuserTargetsRef.current[group.name]
        nextSynced[group.name] = syncedTarget
        next[group.name] = previousValue === undefined || previousValue === previousSyncedValue
          ? syncedTarget
          : previousValue
      })
      syncedVuserTargetsRef.current = nextSynced
      syncedTestIdRef.current = test.id
      return next
    })
    setVuserGroupActions((prev) => {
      const next: Record<string, VUserGroupAction> = {}
      test.groups.forEach((group) => {
        next[group.name] = isNewTest ? 'run' : prev[group.name] ?? 'run'
      })
      return next
    })
    setDurationInput(test.duration || '')
  }, [test?.duration, test?.groups, test?.id, test?.vuser_ramps])

  const onScaleVUsers = useCallback(async (groupName: string) => {
    const action = vuserGroupActions[groupName] ?? 'run'
    const configuredTarget = vuserTargets[groupName]
    if (action === 'run' && (configuredTarget === undefined || configuredTarget < 0)) return
    const target = action === 'run' ? configuredTarget : 0

    try {
      const rampDuration = vuserRampEnabled ? vuserRampDuration.trim() : ''
      const options = rampDuration
        ? { action, ramp_duration: rampDuration }
        : { action }
      if (rampDuration) {
        await api.scaleVUsers(testId, groupName, target, options)
      } else {
        await api.scaleVUsers(testId, groupName, target, options)
      }
      if (action === 'pause') {
        showControlFeedback(rampDuration
          ? `Pausing ${groupName} VUsers over ${rampDuration}`
          : `Paused ${groupName} VUsers`)
      } else if (action === 'stop') {
        showControlFeedback(rampDuration
          ? `Stopping ${groupName} VUsers over ${rampDuration}`
          : `Stopped ${groupName} VUsers`)
      } else {
        showControlFeedback(rampDuration
          ? `Ramping ${groupName} to ${target} VUsers over ${rampDuration}`
          : `Scaled ${groupName} to ${target} VUsers`)
      }
    } catch (error: unknown) {
      showControlFeedback(`Error: ${getErrorMessage(error, 'Unable to scale VUsers')}`)
    }
  }, [api, showControlFeedback, testId, vuserGroupActions, vuserRampDuration, vuserRampEnabled, vuserTargets])

  const onStopAllVUsers = useCallback(async () => {
    if (!test?.groups?.length) return

    const activeGroups = test.groups.filter((group) => {
      const currentVUsers = test.live_metrics?.groups?.[group.name]?.vusers ?? group.vusers
      return currentVUsers > 0 || (test.vuser_ramps?.[group.name]?.target_vusers ?? 0) > 0
    })
    if (activeGroups.length === 0) return

    try {
      const rampDuration = vuserRampEnabled ? vuserRampDuration.trim() : ''
      await Promise.all(activeGroups.map((group) => (
        rampDuration
          ? api.scaleVUsers(testId, group.name, 0, { action: 'stop', ramp_duration: rampDuration })
          : api.scaleVUsers(testId, group.name, 0, { action: 'stop' })
      )))
      setVuserTargets((prev) => {
        const next = { ...prev }
        activeGroups.forEach((group) => {
          next[group.name] = 0
        })
        return next
      })
      showControlFeedback(rampDuration
        ? `Ramping all VUsers to 0 over ${rampDuration}`
        : 'Stopped all VUsers')
    } catch (error: unknown) {
      showControlFeedback(`Error: ${getErrorMessage(error, 'Unable to stop VUsers')}`)
    }
  }, [api, showControlFeedback, test, testId, vuserRampDuration, vuserRampEnabled])

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
      vuserGroupActions,
      vuserRampEnabled,
      vuserRampDurationInput: vuserRampDuration,
    })
  }, [durationInput, test, vuserGroupActions, vuserRampDuration, vuserRampEnabled, vuserTargets])

  const onToggleControls = useCallback(() => {
    setShowControls((current) => !current)
  }, [])

  const onVuserTargetChange = useCallback((groupName: string, value: number) => {
    setVuserTargets((prev) => ({ ...prev, [groupName]: value }))
  }, [])

  const onVuserGroupActionChange = useCallback((groupName: string, value: VUserGroupAction) => {
    setVuserGroupActions((prev) => ({ ...prev, [groupName]: value }))
  }, [])

  return {
    showControls,
    onToggleControls,
    vuserTargets,
    onVuserTargetChange,
    vuserGroupActions,
    onVuserGroupActionChange,
    vuserRampEnabled,
    onVuserRampEnabledChange: setVuserRampEnabled,
    vuserRampDuration,
    onVuserRampDurationChange: setVuserRampDuration,
    durationInput,
    onDurationInputChange: setDurationInput,
    onScaleVUsers,
    onStopAllVUsers,
    onAdjustDuration,
    liveControlPreview,
  }
}
