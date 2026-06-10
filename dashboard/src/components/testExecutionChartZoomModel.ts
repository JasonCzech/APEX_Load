export const executionChartMinuteRangeValues = [
  1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
  11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
  21, 22, 23, 24, 25, 26, 27, 28, 29, 30,
] as const

export type ExecutionChartMinuteValue = typeof executionChartMinuteRangeValues[number]
export type ExecutionChartMinuteRange = `${ExecutionChartMinuteValue}m`
export type ExecutionChartTimeRange = ExecutionChartMinuteRange | 'all' | 'custom'

export const executionChartMinMinuteRange = executionChartMinuteRangeValues[0]
export const executionChartMaxMinuteRange = executionChartMinuteRangeValues[executionChartMinuteRangeValues.length - 1]

export interface ExecutionChartZoomRange {
  left: string | number
  right: string | number
}

export interface ExecutionChartZoomState {
  timeRange: ExecutionChartTimeRange
  zoomRange: ExecutionChartZoomRange | null
  refAreaLeft: string | number
  refAreaRight: string | number
}

export type ExecutionChartZoomAction =
  | { type: 'setTimeRange'; value: ExecutionChartTimeRange }
  | { type: 'beginSelection'; activeLabel?: string | number }
  | { type: 'updateSelection'; activeLabel?: string | number }
  | { type: 'applySelection' }
  | { type: 'setZoomRange'; zoomRange: ExecutionChartZoomRange | null }

export const initialExecutionChartZoomState: ExecutionChartZoomState = {
  timeRange: 'all',
  zoomRange: null,
  refAreaLeft: '',
  refAreaRight: '',
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
      if (action.activeLabel === undefined) {
        return state
      }

      return {
        ...state,
        refAreaLeft: action.activeLabel,
        refAreaRight: action.activeLabel,
      }
    case 'updateSelection':
      if (action.activeLabel === undefined || state.refAreaLeft === '') {
        return state
      }

      return {
        ...state,
        refAreaRight: action.activeLabel,
      }
    case 'applySelection':
      if (state.refAreaLeft === state.refAreaRight || state.refAreaRight === '') {
        return {
          ...state,
          refAreaLeft: '',
          refAreaRight: '',
        }
      }

      return {
        ...state,
        timeRange: 'custom',
        zoomRange: {
          left: state.refAreaLeft,
          right: state.refAreaRight,
        },
        refAreaLeft: '',
        refAreaRight: '',
      }
    default:
      return state
  }
}

export function getExecutionChartRefArea(state: ExecutionChartZoomState) {
  return {
    showRefArea: state.refAreaLeft !== '',
    refAreaEnd: state.refAreaRight === '' ? state.refAreaLeft : state.refAreaRight,
  }
}
