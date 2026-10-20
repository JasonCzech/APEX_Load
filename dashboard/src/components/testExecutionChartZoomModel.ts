// A display window is any whole number of minutes. The slider spans up to the
// run's scheduled length (an hour when it has none) and then "All".
export type ExecutionChartMinuteValue = number
export type ExecutionChartMinuteRange = `${number}m`
export type ExecutionChartTimeRange = ExecutionChartMinuteRange | 'all' | 'custom'

export const executionChartMinMinuteRange = 1
export const executionChartMaxMinuteRange = 60

export interface ExecutionChartZoomAnchor {
  label: string | number
  timestampMs: number
}

export interface ExecutionChartZoomRange {
  startTimestampMs: number
  endTimestampMs: number
}

export interface ExecutionChartZoomState {
  timeRange: ExecutionChartTimeRange
  zoomRange: ExecutionChartZoomRange | null
  refAreaLeft: ExecutionChartZoomAnchor | null
  refAreaRight: ExecutionChartZoomAnchor | null
}

export type ExecutionChartZoomAction =
  | { type: 'setTimeRange'; value: ExecutionChartTimeRange }
  | { type: 'beginSelection'; anchor?: ExecutionChartZoomAnchor }
  | { type: 'updateSelection'; anchor?: ExecutionChartZoomAnchor }
  | { type: 'applySelection' }
  | { type: 'setZoomRange'; zoomRange: ExecutionChartZoomRange | null }

export const initialExecutionChartZoomState: ExecutionChartZoomState = {
  timeRange: 'all',
  zoomRange: null,
  refAreaLeft: null,
  refAreaRight: null,
}

export function executionChartMinuteRangeValue(timeRange: ExecutionChartTimeRange): ExecutionChartMinuteValue | null {
  if (!/^\d+m$/.test(timeRange)) return null
  const minutes = Number(timeRange.slice(0, -1))
  return Number.isSafeInteger(minutes) && minutes >= executionChartMinMinuteRange ? minutes : null
}

/** The slider's longest minute value: the run's scheduled length, else an hour. */
export function executionChartMaxMinutesForSchedule(scheduledSeconds?: number): number {
  return typeof scheduledSeconds === 'number' && Number.isFinite(scheduledSeconds) && scheduledSeconds > 0
    ? Math.max(executionChartMinMinuteRange, Math.ceil(scheduledSeconds / 60))
    : executionChartMaxMinuteRange
}

export function executionChartTimeRangeForMinutes(minutes: number, maxMinutes = executionChartMaxMinuteRange): ExecutionChartMinuteRange {
  const clamped = Math.min(
    Math.max(executionChartMinMinuteRange, Math.round(maxMinutes)),
    Math.max(executionChartMinMinuteRange, Math.round(minutes)),
  )
  return `${clamped}m`
}

export function executionChartZoomReducer(
  state: ExecutionChartZoomState,
  action: ExecutionChartZoomAction,
): ExecutionChartZoomState {
  switch (action.type) {
    case 'setTimeRange':
      return {
        ...state,
        timeRange: action.value,
      }
    case 'setZoomRange':
      return {
        ...state,
        timeRange: action.zoomRange ? 'custom' : 'all',
        zoomRange: action.zoomRange,
      }
    case 'beginSelection':
      if (!action.anchor) {
        return state
      }

      return {
        ...state,
        refAreaLeft: action.anchor,
        refAreaRight: action.anchor,
      }
    case 'updateSelection':
      if (!action.anchor || !state.refAreaLeft) {
        return state
      }

      return {
        ...state,
        refAreaRight: action.anchor,
      }
    case 'applySelection':
      if (!state.refAreaLeft || !state.refAreaRight || state.refAreaLeft.timestampMs === state.refAreaRight.timestampMs) {
        return {
          ...state,
          refAreaLeft: null,
          refAreaRight: null,
        }
      }

      return {
        ...state,
        timeRange: 'custom',
        zoomRange: {
          startTimestampMs: Math.min(state.refAreaLeft.timestampMs, state.refAreaRight.timestampMs),
          endTimestampMs: Math.max(state.refAreaLeft.timestampMs, state.refAreaRight.timestampMs),
        },
        refAreaLeft: null,
        refAreaRight: null,
      }
    default:
      return state
  }
}

export function getExecutionChartRefArea(state: ExecutionChartZoomState) {
  return {
    showRefArea: state.refAreaLeft !== null,
    refAreaLeft: state.refAreaLeft?.label ?? '',
    refAreaEnd: (state.refAreaRight ?? state.refAreaLeft)?.label ?? '',
  }
}
