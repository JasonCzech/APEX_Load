import { useCallback, useMemo } from 'react'

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

  const getScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}`),
    [],
  )

  const updateScript = useCallback(
    (id: string, script: any) =>
      request<any>(`/scripts/${id}`, { method: 'PUT', body: JSON.stringify({ script }) }),
    [],
  )

  const deleteScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}`, { method: 'DELETE' }),
    [],
  )

  const validateScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}/validate`, { method: 'POST' }),
    [],
  )

  const exportScript = useCallback(
    (id: string, outputDir?: string, filename?: string) =>
      request<any>(`/scripts/${id}/export`, {
        method: 'POST',
        body: JSON.stringify({ output_dir: outputDir, filename }),
      }),
    [],
  )

  const importLoadRunner = useCallback(async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean }) => {
    const formData = new FormData()
    formData.append('file', file)
    if (options?.name) formData.append('name', options.name)
    if (options?.base_url) formData.append('base_url', options.base_url)
    if (options?.include_assertions) formData.append('include_assertions', 'true')

    const res = await fetch(`${BASE}/scripts/import/loadrunner`, {
      method: 'POST',
      body: formData,
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error || res.statusText)
    }
    return res.json()
  }, [])

  const getHealth = useCallback(() => request<any>('/system/health'), [])

  return useMemo(
    () => ({
      listTests,
      getTest,
      createTest,
      startTest,
      stopTest,
      abortTest,
      getMetrics,
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      validateScript,
      exportScript,
      importLoadRunner,
      getHealth,
    }),
    [
      listTests,
      getTest,
      createTest,
      startTest,
      stopTest,
      abortTest,
      getMetrics,
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      validateScript,
      exportScript,
      importLoadRunner,
      getHealth,
    ],
  )
}

