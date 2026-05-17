export type ExecutionChartTimeRange = '3m' | '10m' | 'all' | 'custom'

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