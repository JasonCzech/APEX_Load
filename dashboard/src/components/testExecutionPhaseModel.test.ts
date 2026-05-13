import { describe, expect, it } from 'vitest'

import { buildExecutionPhaseModel } from './testExecutionPhaseModel'

describe('testExecutionPhaseModel', () => {
  it('marks running and initializing tests as active live-control states', () => {
    expect(buildExecutionPhaseModel({ status: 'RUNNING' } as never, false)).toEqual({
      isRunning: true,
      isPaused: false,
      isStopping: false,
      showLiveControls: true,
      showArchiveSection: false,
      showArchiveDownload: false,
    })

    expect(buildExecutionPhaseModel({ status: 'INITIALIZING' } as never, true)).toEqual({
      isRunning: true,
      isPaused: false,
      isStopping: false,
      showLiveControls: true,
      showArchiveSection: false,
      showArchiveDownload: false,
    })
  })

  it('keeps paused tests in live-control mode while still allowing archive views', () => {
    expect(buildExecutionPhaseModel({ status: 'PAUSED' } as never, true)).toEqual({
      isRunning: false,
      isPaused: true,
      isStopping: false,
      showLiveControls: true,
      showArchiveSection: true,
      showArchiveDownload: true,
    })
  })

  it('keeps stopping tests out of both live controls and archive views until the run resolves', () => {
    expect(buildExecutionPhaseModel({ status: 'STOPPING' } as never, true)).toEqual({
      isRunning: false,
      isPaused: false,
      isStopping: true,
      showLiveControls: false,
      showArchiveSection: false,
      showArchiveDownload: false,
    })
  })

  it('shows archive state for non-running completed tests', () => {
    expect(buildExecutionPhaseModel({ status: 'COMPLETE' } as never, false)).toEqual({
      isRunning: false,
      isPaused: false,
      isStopping: false,
      showLiveControls: false,
      showArchiveSection: true,
      showArchiveDownload: false,
    })
  })
})