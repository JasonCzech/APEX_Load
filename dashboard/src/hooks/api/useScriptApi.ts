import { useCallback, useMemo } from 'react'

import { BASE, request } from '../apiClient'

function buildScriptQuery(projectId?: string, folderId?: string | null) {
  const query = new URLSearchParams()
  if (projectId) query.set('project_id', projectId)
  if (folderId !== undefined) query.set('folder_id', folderId || '')
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

export function useScriptApi() {
  const listScripts = useCallback(
    (projectId?: string, folderId?: string | null) =>
      request<{ scripts: any[]; count: number }>(`/scripts${buildScriptQuery(projectId, folderId)}`),
    [],
  )

  const createScript = useCallback(
    (script: any, projectId?: string, folderId?: string | null) =>
      request<any>('/scripts', { method: 'POST', body: JSON.stringify({ script, project_id: projectId, folder_id: folderId }) }),
    [],
  )

  const getScript = useCallback((id: string) => request<any>(`/scripts/${id}`), [])

  const updateScript = useCallback(
    (id: string, script: any) =>
      request<any>(`/scripts/${id}`, { method: 'PUT', body: JSON.stringify({ script }) }),
    [],
  )

  const deleteScript = useCallback((id: string) => request<any>(`/scripts/${id}`, { method: 'DELETE' }), [])

  const moveScript = useCallback(
    (id: string, folderId: string | null) =>
      request<any>(`/scripts/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const validateScript = useCallback((id: string) => request<any>(`/scripts/${id}/validate`, { method: 'POST' }), [])

  const exportScript = useCallback(
    (id: string, outputDir?: string, filename?: string) =>
      request<any>(`/scripts/${id}/export`, {
        method: 'POST',
        body: JSON.stringify({ output_dir: outputDir, filename }),
      }),
    [],
  )

  const importLoadRunner = useCallback(
    async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean }) => {
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
    },
    [],
  )

  const importSample = useCallback(
    (data: {
      name?: string
      format: string
      content: string
      response_content?: string
      options?: {
        base_url?: string
        include_assertions?: boolean
        infer_extractors?: boolean
        sanitize_sensitive?: boolean
      }
    }) => request<any>('/scripts/import/sample', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  return useMemo(
    () => ({
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      moveScript,
      validateScript,
      exportScript,
      importLoadRunner,
      importSample,
    }),
    [
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      moveScript,
      validateScript,
      exportScript,
      importLoadRunner,
      importSample,
    ],
  )
}
