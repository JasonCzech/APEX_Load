import { useCallback } from 'react'

const BASE = '/api/v1'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options?.headers },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.json()
}

export function useApi() {
  const listTests = useCallback(() => request<{ tests: any[]; count: number }>('/tests'), [])

  const getTest = useCallback((id: string) => request<any>(`/tests/${id}`), [])

  const createTest = useCallback(
    (data: { name: string; script_id: string; vusers: number; duration: string; ramp_up?: string }) =>
      request<any>('/tests', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const startTest = useCallback(
    (id: string) => request<any>(`/tests/${id}/start`, { method: 'POST' }),
    [],
  )

  const stopTest = useCallback(
    (id: string) => request<any>(`/tests/${id}/stop`, { method: 'POST' }),
    [],
  )

  const abortTest = useCallback(
    (id: string) => request<any>(`/tests/${id}/abort`, { method: 'POST' }),
    [],
  )

  const getMetrics = useCallback((id: string) => request<any>(`/tests/${id}/metrics`), [])

  const listScripts = useCallback(() => request<{ scripts: any[]; count: number }>('/scripts'), [])

  const createScript = useCallback(
    (script: any) => request<any>('/scripts', { method: 'POST', body: JSON.stringify({ script }) }),
    [],
  )

  const getHealth = useCallback(() => request<any>('/system/health'), [])

  return {
    listTests,
    getTest,
    createTest,
    startTest,
    stopTest,
    abortTest,
    getMetrics,
    listScripts,
    createScript,
    getHealth,
  }
}
