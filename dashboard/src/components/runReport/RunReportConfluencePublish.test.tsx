// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RunReportConfluencePublish from './RunReportConfluencePublish'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const api={getConfluencePublishStatus:vi.fn(),publishRunToConfluence:vi.fn()}
vi.mock('../../hooks/api/useConfluenceApi',() => ({useConfluenceApi:() => api}))
let container:HTMLDivElement,root:Root
beforeEach(() => {container=document.createElement('div');document.body.appendChild(container);root=createRoot(container)})
afterEach(() => {act(() => root.unmount());container.remove();vi.clearAllMocks()})

describe('RunReportConfluencePublish', () => {
  it('publishes the selected connection and shows the page link', async () => {
    const connection={id:'c1',name:'Wiki',enabled:true}
    const publication={test_id:'run-1',connection_id:'c1',status:'complete',page_url:'https://example.atlassian.net/wiki/spaces/42/pages/200'}
    api.getConfluencePublishStatus.mockResolvedValueOnce({connections:[connection],publications:[]}).mockResolvedValueOnce({connections:[connection],publications:[publication]})
    api.publishRunToConfluence.mockResolvedValue({publication})
    await act(async () => root.render(<RunReportConfluencePublish testId="run-1" canPublish />))
    await act(async () => (Array.from(container.querySelectorAll('button')).find(button => button.textContent==='Publish to Confluence') as HTMLButtonElement).click())
    expect(api.publishRunToConfluence).toHaveBeenCalledWith('run-1','c1')
    expect(container.querySelector('a')?.getAttribute('href')).toBe(publication.page_url)
    expect(container.textContent).toContain('Update Confluence page')
  })
  it('shows an empty state without a configured destination', async () => {
    api.getConfluencePublishStatus.mockResolvedValue({connections:[],publications:[]})
    await act(async () => root.render(<RunReportConfluencePublish testId="run-1" />))
    expect(container.textContent).toContain('No enabled Confluence connection')
  })
  it('shows publication links without a publish action for viewers', async () => {
    const publication={test_id:'run-1',connection_id:'c1',status:'complete',page_url:'https://example.atlassian.net/wiki/spaces/42/pages/200'}
    api.getConfluencePublishStatus.mockResolvedValue({connections:[{id:'c1',name:'Wiki',enabled:true}],publications:[publication]})
    await act(async () => root.render(<RunReportConfluencePublish testId="run-1" />))
    expect(container.textContent).toContain('Project editor access is required')
    expect(container.querySelector('a')?.getAttribute('href')).toBe(publication.page_url)
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent==='Publish to Confluence')).toBe(false)
  })
})
