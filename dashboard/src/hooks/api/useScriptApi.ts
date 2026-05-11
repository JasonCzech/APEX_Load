import { useCallback, useMemo } from 'react'

import { BrowserReplayStartRequest, BrowserReplayStartResponse, BrowserReplaySessionResponse } from '../../types'
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

  const replayScript = useCallback(
		(id: string, payload?: { group_name?: string; variables?: Record<string, string>; ignore_think_time?: boolean }) =>
			request<any>(`/scripts/${id}/replay`, { method: 'POST', body: JSON.stringify(payload || {}) }),
		[],
	)

  const replayDraftScript = useCallback(
    (payload: { script: any; group_name?: string; variables?: Record<string, string>; ignore_think_time?: boolean }) =>
      request<any>('/scripts/replay', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const startBrowserReplaySession = useCallback(
		(id: string, payload?: BrowserReplayStartRequest) =>
			request<BrowserReplayStartResponse>(`/scripts/${id}/browser-replay/sessions`, { method: 'POST', body: JSON.stringify(payload || {}) }),
		[],
	)

  const startDraftBrowserReplaySession = useCallback(
    (payload: BrowserReplayStartRequest & { script: any }) =>
      request<BrowserReplayStartResponse>('/scripts/browser-replay/sessions', { method: 'POST', body: JSON.stringify(payload) }),
    [],
  )

  const getBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}`),
    [],
  )

  const stopBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/stop`, { method: 'POST' }),
    [],
  )

  const pauseBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/pause`, { method: 'POST' }),
    [],
  )

  const resumeBrowserReplaySession = useCallback(
    (sessionId: string) => request<BrowserReplaySessionResponse>(`/browser-replay/sessions/${sessionId}/resume`, { method: 'POST' }),
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

  const listScenarios = useCallback(() => request<any>('/scenarios'), [])

  const createScenario = useCallback(
    (data: any) => request<any>('/scenarios', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const getScenario = useCallback((id: string) => request<any>(`/scenarios/${id}`), [])

  const updateScenario = useCallback(
    (id: string, data: any) => request<any>(`/scenarios/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    [],
  )

  const deleteScenario = useCallback((id: string) => request<any>(`/scenarios/${id}`, { method: 'DELETE' }), [])

  const listScenarioResults = useCallback(
    (id: string, params?: { application_id?: string; release_id?: string }) => {
      const query = new URLSearchParams()
      if (params?.application_id) query.set('application_id', params.application_id)
      if (params?.release_id) query.set('release_id', params.release_id)
      const suffix = query.toString()
      return request<any>(`/scenarios/${id}/results${suffix ? `?${suffix}` : ''}`)
    },
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
  		replayScript,
  		replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      exportScript,
      importLoadRunner,
      importSample,
      listScenarios,
      createScenario,
      getScenario,
      updateScenario,
      deleteScenario,
      listScenarioResults,
    }),
    [
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      moveScript,
      validateScript,
  		replayScript,
  		replayDraftScript,
      startBrowserReplaySession,
      startDraftBrowserReplaySession,
      getBrowserReplaySession,
      stopBrowserReplaySession,
      pauseBrowserReplaySession,
      resumeBrowserReplaySession,
      exportScript,
      importLoadRunner,
      importSample,
      listScenarios,
      createScenario,
      getScenario,
      updateScenario,
      deleteScenario,
      listScenarioResults,
    ],
  )
}
