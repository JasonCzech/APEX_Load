import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react'

import type { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import {
  API_ORIGIN,
  API_ORIGIN_CHANGED_EVENT,
  DEV_VIEW_MODE_CHANGED_EVENT,
  readStoredDevViewMode,
} from './apiClient'

export interface SharedTestListParams {
  project_id?: string
  folder_id?: string | null
  application_id?: string
  release_id?: string
  scenario_id?: string
  summary?: boolean
  active_only?: boolean
  limit?: number
  offset?: number
}

export interface SharedTestListSnapshot {
  tests: TestSummary[]
  count: number
  total: number
  limit: number
  offset: number
  error: string
  loading: boolean
  refreshing: boolean
  updatedAt: number | null
}

interface TestListResponseLike {
  tests?: TestSummary[]
  count?: number
  total?: number
  limit?: number
  offset?: number
}

export type TestListLoader = (params: SharedTestListParams) => Promise<TestListResponseLike>

interface StoreEntry {
  key: string
  params: SharedTestListParams
  loader: TestListLoader
  snapshot: SharedTestListSnapshot
  listeners: Set<() => void>
  consumers: Map<symbol, { interval: number; pollWhenHidden: boolean }>
  timer: number | null
  inFlight: Promise<void> | null
  forcedRefreshPending: boolean
  lastAccessedAt: number
}

const EMPTY_SNAPSHOT: SharedTestListSnapshot = {
  tests: [],
  count: 0,
  total: 0,
  limit: 0,
  offset: 0,
  error: '',
  loading: false,
  refreshing: false,
  updatedAt: null,
}

const MAX_CACHED_TEST_QUERIES = 16
const entries = new Map<string, StoreEntry>()

function queryKey(params: SharedTestListParams): string {
  const folderScope = params.folder_id === undefined
    ? { kind: 'omitted' }
    : params.folder_id === null
      ? { kind: 'root' }
      : { kind: 'value', value: params.folder_id }
  return JSON.stringify({
    api_origin: API_ORIGIN,
    dev_view: readStoredDevViewMode(),
    project_id: params.project_id || '',
    application_id: params.application_id || '',
    release_id: params.release_id || '',
    scenario_id: params.scenario_id || '',
    summary: Boolean(params.summary),
    active_only: Boolean(params.active_only),
    limit: params.limit ?? null,
    offset: params.offset ?? null,
    folder_scope: folderScope,
  })
}

function notify(entry: StoreEntry) {
  entry.listeners.forEach(listener => listener())
}

function replaceSnapshot(entry: StoreEntry, patch: Partial<SharedTestListSnapshot>) {
  entry.snapshot = { ...entry.snapshot, ...patch }
  entry.lastAccessedAt = Date.now()
  notify(entry)
}

function canPollDocument() {
  return typeof document === 'undefined' || document.visibilityState === 'visible'
}

function canPollEntry(entry: StoreEntry) {
  return canPollDocument() || Array.from(entry.consumers.values()).some(consumer => consumer.pollWhenHidden)
}

async function loadEntry(entry: StoreEntry, force = false): Promise<void> {
  const activeRequest = entry.inFlight
  if (activeRequest) {
    if (!force) return activeRequest
    entry.forcedRefreshPending = true
    return activeRequest.then(() => {
      if (entries.get(entry.key) !== entry) return
      if (entry.forcedRefreshPending) {
        entry.forcedRefreshPending = false
        return loadEntry(entry, true)
      }
      return entry.inFlight || undefined
    })
  }
  if (!force && !canPollEntry(entry)) return

  replaceSnapshot(entry, {
    loading: entry.snapshot.updatedAt === null,
    refreshing: entry.snapshot.updatedAt !== null,
  })

  const request = entry.loader(entry.params)
    .then(response => {
      if (entries.get(entry.key) !== entry) return
      const tests = response.tests || []
      const count = response.count ?? tests.length
      replaceSnapshot(entry, {
        tests,
        count,
        total: response.total ?? count,
        limit: response.limit ?? entry.params.limit ?? count,
        offset: response.offset ?? entry.params.offset ?? 0,
        error: '',
        loading: false,
        refreshing: false,
        updatedAt: Date.now(),
      })
    })
    .catch(error => {
      if (entries.get(entry.key) !== entry) return
      replaceSnapshot(entry, {
        error: getErrorMessage(error, 'Unable to load tests'),
        loading: false,
        refreshing: false,
      })
    })
    .finally(() => {
      if (entry.inFlight === request) entry.inFlight = null
    })

  entry.inFlight = request
  return request
}

function restartPolling(entry: StoreEntry) {
  if (entry.timer !== null && typeof window !== 'undefined') {
    window.clearInterval(entry.timer)
  }
  entry.timer = null

  const intervals = Array.from(entry.consumers.values())
    .map(consumer => consumer.interval)
    .filter(interval => interval > 0)
  if (intervals.length === 0 || typeof window === 'undefined') return
  const interval = Math.min(...intervals)
  entry.timer = window.setInterval(() => {
    void loadEntry(entry)
  }, interval)
}

function getOrCreateEntry(key: string, params: SharedTestListParams, loader: TestListLoader): StoreEntry {
  const existing = entries.get(key)
  if (existing) {
    existing.params = params
    existing.loader = loader
    existing.lastAccessedAt = Date.now()
    return existing
  }

  const entry: StoreEntry = {
    key,
    params,
    loader,
    snapshot: { ...EMPTY_SNAPSHOT, loading: true },
    listeners: new Set(),
    consumers: new Map(),
    timer: null,
    inFlight: null,
    forcedRefreshPending: false,
    lastAccessedAt: Date.now(),
  }
  entries.set(key, entry)
  return entry
}

function trimCache() {
  if (entries.size <= MAX_CACHED_TEST_QUERIES) return
  const inactive = Array.from(entries.values())
    .filter(entry => entry.consumers.size === 0 && entry.listeners.size === 0 && !entry.inFlight)
    .sort((left, right) => left.lastAccessedAt - right.lastAccessedAt)
  while (entries.size > MAX_CACHED_TEST_QUERIES && inactive.length > 0) {
    const entry = inactive.shift()
    if (entry) entries.delete(entry.key)
  }
}

export function useSharedTestList(
  params: SharedTestListParams | null,
  options: { loader: TestListLoader; pollIntervalMs?: number; enabled?: boolean; pollWhenHidden?: boolean },
): SharedTestListSnapshot & { refresh: () => Promise<void> } {
  const [contextGeneration, setContextGeneration] = useState(0)
  const enabled = options.enabled !== false && Boolean(params)
  const key = useMemo(() => {
    void contextGeneration
    return params ? queryKey(params) : ''
  }, [contextGeneration, params])
  const entry = useMemo(
    () => enabled && params ? getOrCreateEntry(key, params, options.loader) : null,
    [enabled, key, options.loader, params],
  )

  const subscribe = useCallback((listener: () => void) => {
    if (!entry) return () => undefined
    entry.listeners.add(listener)
    return () => entry.listeners.delete(listener)
  }, [entry])
  const getSnapshot = useCallback(() => entry?.snapshot || EMPTY_SNAPSHOT, [entry])
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)

  useEffect(() => {
    if (typeof window === 'undefined') return undefined
    const invalidateContext = () => {
      clearSharedTestListStore()
      setContextGeneration(generation => generation + 1)
    }
    window.addEventListener(API_ORIGIN_CHANGED_EVENT, invalidateContext)
    window.addEventListener(DEV_VIEW_MODE_CHANGED_EVENT, invalidateContext)
    return () => {
      window.removeEventListener(API_ORIGIN_CHANGED_EVENT, invalidateContext)
      window.removeEventListener(DEV_VIEW_MODE_CHANGED_EVENT, invalidateContext)
    }
  }, [])

  useEffect(() => {
    if (!entry) return undefined
    const alreadyActive = entry.consumers.size > 0
    const consumer = Symbol(entry.key)
    entry.consumers.set(consumer, {
      interval: Math.max(0, options.pollIntervalMs || 0),
      pollWhenHidden: options.pollWhenHidden === true,
    })
    restartPolling(entry)
    if (!alreadyActive || entry.snapshot.updatedAt === null) {
      void loadEntry(entry)
    }

    const handleVisibilityChange = () => {
      if (canPollDocument()) void loadEntry(entry)
    }
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      entry.consumers.delete(consumer)
      restartPolling(entry)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
      trimCache()
    }
  }, [entry, options.pollIntervalMs, options.pollWhenHidden])

  const refresh = useCallback(() => entry ? loadEntry(entry, true) : Promise.resolve(), [entry])
  return { ...snapshot, refresh }
}

function clearSharedTestListStore() {
  entries.forEach(entry => {
    if (entry.timer !== null && typeof window !== 'undefined') window.clearInterval(entry.timer)
  })
  entries.clear()
}

export function resetSharedTestListStoreForTests() {
  clearSharedTestListStore()
}
