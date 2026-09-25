import { useCallback, useMemo } from 'react'

import { request } from '../apiClient'

export interface ConfluenceConnection {
  id: string; project_id: string; name: string; enabled: boolean; site_url: string; email: string
  space_id: string; parent_page_id: string; has_token: boolean; auto_publish: boolean; status: string
  last_checked_at?: string; last_check_status?: string; last_check_message?: string
  created_at: string; updated_at: string
}

export interface ConfluenceConnectionInput {
  name: string; enabled: boolean; site_url: string; email: string; space_id: string
  parent_page_id: string; token_secret_name?: string; auto_publish: boolean; updated_at?: string
}

export interface ConfluencePublication {
  test_id: string; project_id: string; connection_id: string; page_id?: string; page_url?: string
  status: string; error?: string; attempts: number; requested_by?: string; created_at: string; updated_at: string
}

export function useConfluenceApi() {
  const listConfluenceConnections = useCallback((projectId: string) => request<{connections: ConfluenceConnection[]; count: number; public_url_configured: boolean}>(`/projects/${encodeURIComponent(projectId)}/integrations/confluence-connections`), [])
  const createConfluenceConnection = useCallback((projectId: string, input: ConfluenceConnectionInput) => request<ConfluenceConnection>(`/projects/${encodeURIComponent(projectId)}/integrations/confluence-connections`, {method:'POST',body:JSON.stringify(input)}), [])
  const updateConfluenceConnection = useCallback((projectId: string, id: string, input: ConfluenceConnectionInput) => request<ConfluenceConnection>(`/projects/${encodeURIComponent(projectId)}/integrations/confluence-connections/${encodeURIComponent(id)}`, {method:'PUT',body:JSON.stringify(input)}), [])
  const deleteConfluenceConnection = useCallback((projectId: string, id: string) => request<void>(`/projects/${encodeURIComponent(projectId)}/integrations/confluence-connections/${encodeURIComponent(id)}`, {method:'DELETE'}), [])
  const testConfluenceConnection = useCallback((projectId: string, id: string) => request<{status:string;message:string;checked_at:string}>(`/projects/${encodeURIComponent(projectId)}/integrations/confluence-connections/${encodeURIComponent(id)}/test`, {method:'POST'}), [])
  const getConfluencePublishStatus = useCallback((testId: string) => request<{connections:ConfluenceConnection[];publications:ConfluencePublication[]}>(`/tests/${encodeURIComponent(testId)}/publish/confluence`), [])
  const publishRunToConfluence = useCallback((testId: string, connectionId: string) => request<{publication:ConfluencePublication}>(`/tests/${encodeURIComponent(testId)}/publish/confluence`, {method:'POST',body:JSON.stringify({connection_id:connectionId})}), [])
  return useMemo(() => ({listConfluenceConnections,createConfluenceConnection,updateConfluenceConnection,deleteConfluenceConnection,testConfluenceConnection,getConfluencePublishStatus,publishRunToConfluence}), [listConfluenceConnections,createConfluenceConnection,updateConfluenceConnection,deleteConfluenceConnection,testConfluenceConnection,getConfluencePublishStatus,publishRunToConfluence])
}
