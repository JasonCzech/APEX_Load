import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import type {
  BrowserReplayEvent,
  BrowserReplayReport,
  ScriptDebugAction,
  ScriptDebugBreakpoint,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
} from '../types/replay'
import { actionRequestCount, httpSequenceRequests } from './scriptActionHelpers'
import {
  debugBreakpointKey,
  debugLogLevelWeight,
  formatDebugLogEntry,
  normalizeStoredBreakpoints,
} from './scriptEditorReplayModel'
import {
  DEBUGGER_LAYOUT_DEFAULTS,
  DEBUGGER_LAYOUT_LIMITS,
  clampDebuggerPaneSize,
  debugStepPathKey,
  resolveDebugRowForEvent,
  type DebuggerResizeKind,
  type ScriptPhaseActionRow,
} from './scriptEditorWorkbench'

export type DebuggerTab = 'request' | 'browser' | 'variables'
export type DebuggerBottomTab = 'console' | 'timeline' | 'problems'

export function selectDebugEvents(
  session: ScriptDebugSession | null,
  report: BrowserReplayReport | null,
): BrowserReplayEvent[] {
  // Indexed WebSocket deltas can overtake the start response or a polling
  // snapshot. Array spread materializes the resulting holes as undefined.
  // Display available evidence until the next full snapshot fills the gap.
  const dense = (events: BrowserReplayEvent[] | undefined) => {
    if (!events) return events
    const present = events.filter((event): event is BrowserReplayEvent => event != null)
    return present.length === events.length ? events : present
  }
  const sessionEvents = dense(session?.events)
  const reportEvents = dense(report?.events)
  if (sessionEvents && reportEvents) return sessionEvents.length >= reportEvents.length ? sessionEvents : reportEvents
  return sessionEvents ?? reportEvents ?? []
}

export function filterDebugLogs(
  logs: ScriptDebugLogEntry[],
  clearedAt: number,
  level: ScriptDebugLogLevel | 'all',
  search: string,
): ScriptDebugLogEntry[] {
  const searchText = search.trim().toLowerCase()
  return logs
    .slice(clearedAt)
    .filter(entry => level === 'all' || debugLogLevelWeight(entry.level) <= debugLogLevelWeight(level))
    .filter(entry => !searchText || formatDebugLogEntry(entry).toLowerCase().includes(searchText))
}

export function buildEventIndexByLocation(events: BrowserReplayEvent[]): Map<string, number> {
  const map = new Map<string, number>()
  events.forEach((event, index) => {
    if (typeof event.action_index !== 'number') return
    map.set(debugBreakpointKey(event.phase, event.action_index, event.request_index), index)
  })
  return map
}

export function buildDebugRowPositionByKey(events: BrowserReplayEvent[]): Map<string, number> {
  const map = new Map<string, number>()
  events.forEach((event, index) => {
    if (typeof event.action_index !== 'number') return
    const key = debugBreakpointKey(event.phase, event.action_index)
    if (!map.has(key)) map.set(key, index)
  })
  return map
}

export function buildEventsByStepPath(events: BrowserReplayEvent[]): Map<string, number[]> {
  const map = new Map<string, number[]>()
  events.forEach((event, index) => {
    if (typeof event.action_index !== 'number' || !event.step_path || event.step_path.length === 0) return
    const key = debugStepPathKey(event.phase, event.action_index, event.step_path)
    const existing = map.get(key)
    if (existing) existing.push(index)
    else map.set(key, [index])
  })
  return map
}

export function toggleExpandedSequence(current: Set<string>, key: string): Set<string> {
  const next = new Set(current)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  return next
}

export function deriveDebuggerStatus(
  replaying: boolean,
  paused: boolean,
  report: BrowserReplayReport | null,
): 'error' | 'success' | 'running' | 'paused' | 'idle' {
  if (replaying) return paused ? 'paused' : 'running'
  if (report?.stopped) return 'idle'
  if (report) return report.success ? 'success' : 'error'
  return 'idle'
}

function loadPaneSize(kind: DebuggerResizeKind): number {
  try {
    const raw = window.localStorage.getItem(`debugger${kind === 'rail' ? 'RailWidth' : kind === 'vars' ? 'VarsWidth' : 'BottomHeight'}`)
    const parsed = raw ? Number.parseInt(raw, 10) : Number.NaN
    return Number.isFinite(parsed) ? clampDebuggerPaneSize(kind, parsed) : DEBUGGER_LAYOUT_DEFAULTS[kind]
  } catch {
    return DEBUGGER_LAYOUT_DEFAULTS[kind]
  }
}

function currentDebugAncestorKeys(action?: ScriptDebugAction): Set<string> {
  const keys = new Set<string>()
  if (!action) return keys
  const isLeaf = action.request_index != null || (action.step_path?.length ?? 0) > 0
  if (!isLeaf) return keys
  const base = debugBreakpointKey(action.phase, action.action_index)
  keys.add(base)
  const path = action.step_path ?? []
  for (let index = 1; index < path.length; index += 1) {
    keys.add(`${base}|${path.slice(0, index).join('/')}`)
  }
  if (path.length > 0) keys.add(debugStepPathKey(action.phase, action.action_index, path))
  return keys
}

function filterForcedDebugSequences(
  rows: ScriptPhaseActionRow[],
  events: BrowserReplayEvent[],
  filterText: string,
  failuresOnly: boolean,
): Set<string> {
  const keys = new Set<string>()
  if (!filterText && !failuresOnly) return keys
  const addAncestors = (phase: string, actionIndex: number, stepPath?: string[]) => {
    const base = debugBreakpointKey(phase, actionIndex)
    keys.add(base)
    const path = stepPath ?? []
    for (let index = 1; index < path.length; index += 1) {
      keys.add(`${base}|${path.slice(0, index).join('/')}`)
    }
  }
  if (failuresOnly) {
    for (const event of events) {
      if (typeof event.action_index === 'number' && !event.success) {
        addAncestors(event.phase, event.action_index, event.step_path)
      }
    }
  } else {
    for (const row of rows) {
      if (actionRequestCount(row.action) === 0) continue
      if (httpSequenceRequests(row.action).some(child => `${child.name || ''} ${child.target || ''}`.toLowerCase().includes(filterText))) {
        keys.add(debugBreakpointKey(row.phase, row.actionIndex))
      }
    }
  }
  return keys
}

interface UseScriptReplayDebuggerControllerOptions {
  browserReplaySession: ScriptDebugSession | null
  debugActionRows: ScriptPhaseActionRow[]
  expandedReplayEvent: number | null
  replayReport: BrowserReplayReport | null
  replaying: boolean
  scriptId?: string | null
}

export function useScriptReplayDebuggerController({
  browserReplaySession,
  debugActionRows,
  expandedReplayEvent,
  replayReport,
  replaying,
  scriptId,
}: UseScriptReplayDebuggerControllerOptions) {
  const [debugBreakpoints, setDebugBreakpoints] = useState<ScriptDebugBreakpoint[]>([])
  const [debugActionListCollapsed, setDebugActionListCollapsed] = useState(false)
  const [debugParameterListCollapsed, setDebugParameterListCollapsed] = useState(false)
  const [debuggerTab, setDebuggerTab] = useState<DebuggerTab>('request')
  const [debuggerSettingsOpen, setDebuggerSettingsOpen] = useState(false)
  const [debugDetailCollapsed, setDebugDetailCollapsed] = useState(false)
  const [debuggerBottomTab, setDebuggerBottomTab] = useState<DebuggerBottomTab>('console')
  const [debugRawLevel, setDebugRawLevel] = useState<ScriptDebugLogLevel | 'all'>('all')
  const [debugRawSearch, setDebugRawSearch] = useState('')
  const [debugRawAutoScroll, setDebugRawAutoScroll] = useState(true)
  const [debugRawClearedAt, setDebugRawClearedAt] = useState(0)
  const [debugRailFilter, setDebugRailFilter] = useState('')
  const [debugRailFailuresOnly, setDebugRailFailuresOnly] = useState(false)
  const [expandedDebugSequences, setExpandedDebugSequences] = useState<Set<string>>(() => new Set())
  const [debuggerRailWidth, setDebuggerRailWidth] = useState(() => loadPaneSize('rail'))
  const [debuggerVarsWidth, setDebuggerVarsWidth] = useState(() => loadPaneSize('vars'))
  const [debuggerBottomHeight, setDebuggerBottomHeight] = useState(() => loadPaneSize('bottom'))
  const [activeDebuggerResize, setActiveDebuggerResize] = useState<DebuggerResizeKind | null>(null)
  const debugRawOutputRef = useRef<HTMLDivElement | null>(null)
  const debuggerSettingsRef = useRef<HTMLDivElement | null>(null)
  const debuggerSettingsBtnRef = useRef<HTMLButtonElement | null>(null)
  const breakpointsLoadedRef = useRef(false)

  const debugEvents = useMemo(
    () => selectDebugEvents(browserReplaySession, replayReport),
    [browserReplaySession, replayReport],
  )
  const selectedReplayEventIndex = expandedReplayEvent != null && expandedReplayEvent >= 0 && expandedReplayEvent < debugEvents.length
    ? expandedReplayEvent
    : null
  const selectedReplayEvent = selectedReplayEventIndex != null ? debugEvents[selectedReplayEventIndex] : null
  const selectedDebugRow = useMemo(
    () => resolveDebugRowForEvent(debugActionRows, selectedReplayEvent),
    [debugActionRows, selectedReplayEvent],
  )
  const currentDebugAction = replaying ? browserReplaySession?.current_action : undefined
  const currentDebugActionKey = currentDebugAction
    ? debugBreakpointKey(currentDebugAction.phase, currentDebugAction.action_index, currentDebugAction.request_index)
    : null
  const currentDebugStepKey = currentDebugAction && (currentDebugAction.step_path?.length ?? 0) > 0
    ? debugStepPathKey(currentDebugAction.phase, currentDebugAction.action_index, currentDebugAction.step_path ?? [])
    : null
  const debugRailFilterText = debugRailFilter.trim().toLowerCase()
  const debugFilteredLogs = useMemo(
    () => filterDebugLogs(browserReplaySession?.logs || [], debugRawClearedAt, debugRawLevel, debugRawSearch),
    [browserReplaySession?.logs, debugRawClearedAt, debugRawLevel, debugRawSearch],
  )
  const debugRawOutputText = useMemo(() => debugFilteredLogs.map(formatDebugLogEntry).join('\n\n'), [debugFilteredLogs])
  const debuggerStatusState = deriveDebuggerStatus(replaying, browserReplaySession?.status === 'paused', replayReport)
  const eventIndexByLocation = useMemo(() => buildEventIndexByLocation(debugEvents), [debugEvents])
  const debugRowPositionByKey = useMemo(() => buildDebugRowPositionByKey(debugEvents), [debugEvents])
  const eventsByStepPath = useMemo(() => buildEventsByStepPath(debugEvents), [debugEvents])
  const currentDebugAncestorKeySet = useMemo(() => currentDebugAncestorKeys(currentDebugAction), [currentDebugAction])
  const filterForcedDebugSequenceSet = useMemo(
    () => filterForcedDebugSequences(debugActionRows, debugEvents, debugRailFilterText, debugRailFailuresOnly),
    [debugActionRows, debugEvents, debugRailFailuresOnly, debugRailFilterText],
  )
  const effectiveExpandedDebugSequences = useMemo(() => {
    const next = new Set(expandedDebugSequences)
    currentDebugAncestorKeySet.forEach(key => next.add(key))
    filterForcedDebugSequenceSet.forEach(key => next.add(key))
    return next
  }, [currentDebugAncestorKeySet, expandedDebugSequences, filterForcedDebugSequenceSet])

  const toggleDebugSequenceExpanded = useCallback((key: string) => {
    setExpandedDebugSequences(current => toggleExpandedSequence(current, key))
  }, [])

  useEffect(() => {
    if (selectedReplayEvent || (replaying && currentDebugAction)) {
      setDebugDetailCollapsed(false)
    }
  }, [currentDebugAction, replaying, selectedReplayEvent])

  useEffect(() => {
    try {
      window.localStorage.setItem('debuggerRailWidth', debuggerRailWidth.toString())
      window.localStorage.setItem('debuggerVarsWidth', debuggerVarsWidth.toString())
      window.localStorage.setItem('debuggerBottomHeight', debuggerBottomHeight.toString())
    } catch { /* storage may be unavailable */ }
  }, [debuggerBottomHeight, debuggerRailWidth, debuggerVarsWidth])

  useEffect(() => {
    if (!debugRawAutoScroll || !debugRawOutputRef.current) return
    debugRawOutputRef.current.scrollTop = debugRawOutputRef.current.scrollHeight
  }, [debugRawAutoScroll, debugRawOutputText])

  const breakpointStorageKey = scriptId ? `apexload.debug.breakpoints.${scriptId}` : null
  useEffect(() => {
    breakpointsLoadedRef.current = false
    if (!breakpointStorageKey) return
    try {
      const raw = window.localStorage.getItem(breakpointStorageKey)
      if (raw) setDebugBreakpoints(normalizeStoredBreakpoints(JSON.parse(raw)))
    } catch { /* ignore malformed persisted breakpoints */ }
    breakpointsLoadedRef.current = true
  }, [breakpointStorageKey])
  useEffect(() => {
    if (!breakpointStorageKey || !breakpointsLoadedRef.current) return
    try { window.localStorage.setItem(breakpointStorageKey, JSON.stringify(debugBreakpoints.filter(bp => !bp.once))) } catch { /* storage may be unavailable */ }
  }, [breakpointStorageKey, debugBreakpoints])

  useEffect(() => {
    if (!debuggerSettingsOpen) return
    const onDown = (event: globalThis.MouseEvent) => {
      const target = event.target as Node
      if (debuggerSettingsRef.current?.contains(target)) return
      if (debuggerSettingsBtnRef.current?.contains(target)) return
      setDebuggerSettingsOpen(false)
    }
    window.addEventListener('mousedown', onDown)
    return () => window.removeEventListener('mousedown', onDown)
  }, [debuggerSettingsOpen])

  const handleDebuggerResizeKeyDown = useCallback((kind: DebuggerResizeKind, event: ReactKeyboardEvent<HTMLDivElement>) => {
    const limits = DEBUGGER_LAYOUT_LIMITS[kind]
    const step = limits.step * (event.shiftKey ? 2 : 1)
    const currentValue = kind === 'rail' ? debuggerRailWidth : kind === 'vars' ? debuggerVarsWidth : debuggerBottomHeight
    let nextValue: number | null = null
    if (kind === 'bottom') {
      if (event.key === 'ArrowUp') nextValue = currentValue + step
      else if (event.key === 'ArrowDown') nextValue = currentValue - step
    } else if (kind === 'rail') {
      if (event.key === 'ArrowRight') nextValue = currentValue + step
      else if (event.key === 'ArrowLeft') nextValue = currentValue - step
    } else {
      if (event.key === 'ArrowLeft') nextValue = currentValue + step
      else if (event.key === 'ArrowRight') nextValue = currentValue - step
    }
    if (event.key === 'Home') nextValue = limits.min
    if (event.key === 'End') nextValue = limits.max
    if (nextValue === null) return
    event.preventDefault()
    if (kind === 'rail') setDebuggerRailWidth(clampDebuggerPaneSize('rail', nextValue))
    else if (kind === 'vars') setDebuggerVarsWidth(clampDebuggerPaneSize('vars', nextValue))
    else setDebuggerBottomHeight(clampDebuggerPaneSize('bottom', nextValue))
  }, [debuggerBottomHeight, debuggerRailWidth, debuggerVarsWidth])

  const startDebuggerDrag = useCallback((kind: DebuggerResizeKind, event: ReactMouseEvent) => {
    event.preventDefault()
    const startX = event.clientX
    const startY = event.clientY
    const startRail = debuggerRailWidth
    const startVars = debuggerVarsWidth
    const startBottom = debuggerBottomHeight
    setActiveDebuggerResize(kind)
    const onMove = (moveEvent: globalThis.MouseEvent) => {
      if (kind === 'rail') setDebuggerRailWidth(clampDebuggerPaneSize('rail', startRail + (moveEvent.clientX - startX)))
      else if (kind === 'vars') setDebuggerVarsWidth(clampDebuggerPaneSize('vars', startVars - (moveEvent.clientX - startX)))
      else setDebuggerBottomHeight(clampDebuggerPaneSize('bottom', startBottom - (moveEvent.clientY - startY)))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      window.removeEventListener('blur', onUp)
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
      setActiveDebuggerResize(null)
    }
    document.body.style.userSelect = 'none'
    document.body.style.cursor = kind === 'bottom' ? 'row-resize' : 'col-resize'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    window.addEventListener('blur', onUp)
  }, [debuggerBottomHeight, debuggerRailWidth, debuggerVarsWidth])

  const resetLayout = useCallback(() => {
    setDebuggerRailWidth(DEBUGGER_LAYOUT_DEFAULTS.rail)
    setDebuggerVarsWidth(DEBUGGER_LAYOUT_DEFAULTS.vars)
    setDebuggerBottomHeight(DEBUGGER_LAYOUT_DEFAULTS.bottom)
    setDebugDetailCollapsed(false)
  }, [])

  return {
    resetLayout,
    activeDebuggerResize,
    currentDebugAction,
    currentDebugAncestorKeys: currentDebugAncestorKeySet,
    currentDebugActionKey,
    currentDebugStepKey,
    debugActionListCollapsed,
    debugBreakpoints,
    debugDetailCollapsed,
    debugEvents,
    debugFilteredLogs,
    debuggerBottomHeight,
    debuggerBottomTab,
    debuggerRailWidth,
    debuggerSettingsBtnRef,
    debuggerSettingsOpen,
    debuggerSettingsRef,
    debuggerStatusState,
    debuggerTab,
    debuggerVarsWidth,
    debugRailFailuresOnly,
    debugRailFilter,
    debugRailFilterText,
    debugRawAutoScroll,
    debugRawLevel,
    debugRawOutputRef,
    debugRawOutputText,
    debugRawSearch,
    debugRowPositionByKey,
    debugParameterListCollapsed,
    effectiveExpandedDebugSequences,
    eventIndexByLocation,
    eventsByStepPath,
    filterForcedDebugSequences: filterForcedDebugSequenceSet,
    handleDebuggerResizeKeyDown,
    selectedDebugRow,
    selectedReplayEvent,
    selectedReplayEventIndex,
    setDebugBreakpoints,
    setDebugActionListCollapsed,
    setDebugDetailCollapsed,
    setDebuggerBottomTab,
    setDebuggerSettingsOpen,
    setDebuggerTab,
    setDebugRailFailuresOnly,
    setDebugRailFilter,
    setDebugRawAutoScroll,
    setDebugRawClearedAt,
    setDebugRawLevel,
    setDebugRawSearch,
    setDebugParameterListCollapsed,
    startDebuggerDrag,
    toggleDebugSequenceExpanded,
  }
}
