import { useCallback, useMemo } from 'react'

import {
  GitHubBranch,
  GitHubConfigResponse,
  GitHubConnectionResponse,
  GitHubContentsResponse,
  GitHubPushResponse,
  GitHubRepo,
  GitHubScriptStatus,
} from '../../types'
import { ScriptImportResult } from '../../types/scriptImports'
import { BASE, request } from '../apiClient'

const GITHUB_BASE = '/integrations/github'

export type GitHubPushRequest = {
  owner: string
  repo: string
  branch?: string
  path?: string
  message?: string
  strategy?: 'commit' | 'pr'
  pr_branch?: string
  pr_title?: string
  pr_body?: string
}

export type GitHubImportRequest = {
  project_id: string
  owner: string
  repo: string
  path: string
  ref?: string
  name?: string
}

export function useGitHubApi() {
  const getConfig = useCallback(
    () => request<GitHubConfigResponse>(`${GITHUB_BASE}/config`),
    [],
  )

  const getConnection = useCallback(
    () => request<GitHubConnectionResponse>(`${GITHUB_BASE}/connection`),
    [],
  )

  const disconnect = useCallback(
    () => request<{ disconnected: boolean }>(`${GITHUB_BASE}/connection`, { method: 'DELETE' }),
    [],
  )

  // loginUrl builds the full OAuth connect URL; the browser is navigated here so
  // the GitHub (GHES) redirect can complete and set the connection cookie.
  const loginUrl = useCallback((returnTo?: string) => {
    const params = new URLSearchParams()
    if (returnTo?.trim()) {
      params.set('return_to', returnTo.trim())
    }
    const query = params.toString()
    return `${BASE}${GITHUB_BASE}/login${query ? `?${query}` : ''}`
  }, [])

  const listRepos = useCallback(
    () => request<{ repos: GitHubRepo[] }>(`${GITHUB_BASE}/repos`),
    [],
  )

  const listBranches = useCallback(
    (owner: string, repo: string) => {
      const params = new URLSearchParams({ owner, repo })
      return request<{ branches: GitHubBranch[] }>(`${GITHUB_BASE}/branches?${params.toString()}`)
    },
    [],
  )

  const getContents = useCallback(
    (owner: string, repo: string, path = '', ref = '') => {
      const params = new URLSearchParams({ owner, repo })
      if (path) params.set('path', path)
      if (ref) params.set('ref', ref)
      return request<GitHubContentsResponse>(`${GITHUB_BASE}/contents?${params.toString()}`)
    },
    [],
  )

  const importScript = useCallback(
    (body: GitHubImportRequest) =>
      request<ScriptImportResult>('/scripts/import/github', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const pushScript = useCallback(
    (scriptId: string, body: GitHubPushRequest) =>
      request<GitHubPushResponse>(`/scripts/${encodeURIComponent(scriptId)}/github/push`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const scriptStatus = useCallback(
    (scriptId: string) => request<GitHubScriptStatus>(`/scripts/${encodeURIComponent(scriptId)}/github/status`),
    [],
  )

  return useMemo(
    () => ({
      getConfig,
      getConnection,
      disconnect,
      loginUrl,
      listRepos,
      listBranches,
      getContents,
      importScript,
      pushScript,
      scriptStatus,
    }),
    [getConfig, getConnection, disconnect, loginUrl, listRepos, listBranches, getContents, importScript, pushScript, scriptStatus],
  )
}
