// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ConfluenceConnectionsCard from './ConfluenceConnectionsCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const api = {listConfluenceConnections:vi.fn(),createConfluenceConnection:vi.fn(),updateConfluenceConnection:vi.fn(),deleteConfluenceConnection:vi.fn(),testConfluenceConnection:vi.fn()}
vi.mock('../hooks/api/useConfluenceApi', () => ({useConfluenceApi:() => api}))
let container:HTMLDivElement,root:Root
beforeEach(() => {container=document.createElement('div');document.body.appendChild(container);root=createRoot(container)})
afterEach(() => {act(() => root.unmount());container.remove();vi.clearAllMocks()})

describe('ConfluenceConnectionsCard', () => {
  it('shows redacted connection health without implying the site is missing', async () => {
    api.listConfluenceConnections.mockResolvedValue({connections:[{id:'c1',project_id:'p1',name:'Wiki',enabled:true,site_url:'',email:'',space_id:'',parent_page_id:'',has_token:false,auto_publish:false,status:'connected',last_check_status:'connected',updated_at:''}],public_url_configured:false})
    await act(async () => root.render(<ConfluenceConnectionsCard projectId="p1" canManage={false} />))
    expect(container.textContent).toContain('connected')
    expect(container.textContent).not.toContain('wiki-token')
    expect(container.textContent).not.toContain('Site not configured')
    expect(container.querySelector('input[type="password"]')).toBeNull()
  })
  it('offers setup fields and explains automatic publishing URL requirement', async () => {
    api.listConfluenceConnections.mockResolvedValue({connections:[],public_url_configured:false})
    await act(async () => root.render(<ConfluenceConnectionsCard projectId="p1" canManage />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent==='Add Confluence connection')!.click())
    expect(container.textContent).toContain('API token project-secret reference')
    expect(container.textContent).toContain('APEXLOAD_PUBLIC_URL')
    const auto=Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[1]
    expect(auto.disabled).toBe(true)
  })
  it('lets an admin turn off an existing automatic connection when the public URL is missing', async () => {
    api.listConfluenceConnections.mockResolvedValue({connections:[{id:'c1',project_id:'p1',name:'Wiki',enabled:true,site_url:'https://example.atlassian.net',email:'writer@example.com',space_id:'42',parent_page_id:'100',has_token:true,auto_publish:true,status:'connected',updated_at:''}],public_url_configured:false})
    await act(async () => root.render(<ConfluenceConnectionsCard projectId="p1" canManage />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent==='Edit')!.click())
    const auto=Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[1]
    expect(auto.disabled).toBe(false)
    await act(async () => auto.click())
    expect(auto.checked).toBe(false)
  })
  it('refreshes saved health after a connection test fails', async () => {
    const connection={id:'c1',project_id:'p1',name:'Wiki',enabled:true,site_url:'https://example.atlassian.net',email:'writer@example.com',space_id:'42',parent_page_id:'100',has_token:true,auto_publish:false,status:'connected',updated_at:''}
    api.listConfluenceConnections
      .mockResolvedValueOnce({connections:[connection],public_url_configured:false})
      .mockResolvedValueOnce({connections:[{...connection,status:'error',last_check_status:'error',last_check_message:'Parent page is unavailable'}],public_url_configured:false})
    api.testConfluenceConnection.mockRejectedValueOnce(new Error('Connection test failed'))
    await act(async () => root.render(<ConfluenceConnectionsCard projectId="p1" canManage />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent==='Test saved connection')!.click())
    expect(api.listConfluenceConnections).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Parent page is unavailable')
    expect(container.textContent).toContain('Connection test failed')
  })
})
