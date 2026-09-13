export const executionChartMinuteRangeValues = [
  1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
  11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
  21, 22, 23, 24, 25, 26, 27, 28, 29, 30,
  31, 32, 33, 34, 35, 36, 37, 38, 39, 40,
  41, 42, 43, 44, 45, 46, 47, 48, 49, 50,
  51, 52, 53, 54, 55, 56, 57, 58, 59, 60,
] as const

export type ExecutionChartMinuteValue = typeof executionChartMinuteRangeValues[number]
export type ExecutionChartMinuteRange = `${ExecutionChartMinuteValue}m`
export type ExecutionChartTimeRange = ExecutionChartMinuteRange | 'all' | 'custom'

export const executionChartMinMinuteRange = executionChartMinuteRangeValues[0]
export const executionChartMaxMinuteRange = executionChartMinuteRangeValues[executionChartMinuteRangeValues.length - 1]

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
  if (!timeRange.endsWith('m')) return null
  const minutes = Number(timeRange.slice(0, -1))
  return executionChartMinuteRangeValues.includes(minutes as ExecutionChartMinuteValue)
    ? minutes as ExecutionChartMinuteValue
    : null
}

export function executionChartTimeRangeForMinutes(minutes: number): ExecutionChartMinuteRange {
  const clamped = Math.min(
    executionChartMaxMinuteRange,
    Math.max(executionChartMinMinuteRange, Math.round(minutes)),
  ) as ExecutionChartMinuteValue
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
