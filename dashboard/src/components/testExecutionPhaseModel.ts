import { TestSummary } from '../types'

interface ExecutionPhaseModel {
  isRunning: boolean
  isPaused: boolean
  isStopping: boolean
  showLiveControls: boolean
  showArchiveSection: boolean
  showArchiveDownload: boolean
}

export function buildExecutionPhaseModel(test: TestSummary, hasArchive: boolean): ExecutionPhaseModel {
  const isRunning = test.status === 'RUNNING' || test.status === 'INITIALIZING'
  const isPaused = test.status === 'PAUSED'
  const isStopping = test.status === 'STOPPING'

  return {
    isRunning,
    isPaused,
    isStopping,
    showLiveControls: isRunning || isPaused,
    showArchiveSection: !isRunning && !isStopping,
    showArchiveDownload: !isRunning && !isStopping && hasArchive,
  }
}