export interface TimelineZoomWindow {
  startIndex: number
  endIndex: number
}

export interface TimelineZoomState {
  window: TimelineZoomWindow | null
}

export type TimelineZoomAction =
  | { type: 'set-window'; startIndex: number; endIndex: number; pointCount: number }
  | { type: 'reset' }

export const initialTimelineZoomState: TimelineZoomState = {
  window: null,
}

function normalizeBrushWindow(startIndex: number, endIndex: number, pointCount: number): TimelineZoomWindow | null {
  if (pointCount <= 0) return null
  const maxIndex = pointCount - 1
  const start = Math.max(0, Math.min(Math.trunc(startIndex), maxIndex))
  const end = Math.max(0, Math.min(Math.trunc(endIndex), maxIndex))
  const normalizedStart = Math.min(start, end)
  const normalizedEnd = Math.max(start, end)
  if (normalizedStart === 0 && normalizedEnd === maxIndex) return null
  return { startIndex: normalizedStart, endIndex: normalizedEnd }
}

export function timelineZoomReducer(state: TimelineZoomState, action: TimelineZoomAction): TimelineZoomState {
  switch (action.type) {
    case 'set-window':
      return {
        window: normalizeBrushWindow(action.startIndex, action.endIndex, action.pointCount),
      }
    case 'reset':
      return initialTimelineZoomState
    default:
      return state
  }
}
