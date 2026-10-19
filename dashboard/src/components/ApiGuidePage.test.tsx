// @vitest-environment jsdom
import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { helpArticles, loadHelpSource } from '../helpContent'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import ApiGuidePage from './ApiGuidePage'

function Topbar() { const contribution = useActiveTopbarContribution(); return <div data-testid="topbar">{contribution?.title}{contribution?.controls}</div> }
const access = (ids: string[]) => [{ id: 'test', name: 'Test', description: '', operations: ids.map(id => ({ id, label: id, allowed: true })) }]
describe('Help documentation', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    window.history.replaceState(null, '', '/')
    container = document.createElement('div'); document.body.append(container); root = createRoot(container)

  })
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.restoreAllMocks() })
  async function render(props: ComponentProps<typeof ApiGuidePage> = {}) {
    await act(async () => { root.render(<TopbarContributionProvider><Topbar /><ApiGuidePage {...props} /></TopbarContributionProvider>); await Promise.all(helpArticles.map(loadHelpSource)) })
  }
  async function navigate(hash: string) { await act(async () => { window.location.hash = hash; window.dispatchEvent(new HashChangeEvent('hashchange')); await Promise.all(helpArticles.map(loadHelpSource)) }) }
  it('canonicalizes repository heading fragments before selecting and focusing their help section', async () => {
    await navigate('#/docs?guide=user-guide&article=messaging&section=webapi-execution-support-matrix')
    await render()
    expect(window.location.hash).toContain('article=web-api-support')
    expect(window.location.hash).toContain('section=web-api-execution-support-matrix')
    expect(container.querySelector('#web-api-execution-support-matrix')).not.toBeNull()
    expect(container.textContent).not.toContain('That section is not available')
  })
  it('starts with a self-contained first test and five semantic areas for administrators', async () => {
    await render({ effectiveAccessResources: access(['manage_system']) })
    expect(container.querySelector('[data-testid="topbar"]')?.textContent).toContain('Help & documentation')
    expect(container.querySelector('[data-testid="topbar"] nav[aria-label="Help areas"]')?.classList.contains('topbar-page-section-toggle')).toBe(true)
    expect(Array.from(container.querySelectorAll('nav[aria-label="Help areas"] a')).map(a => a.textContent)).toEqual(['Getting Started', 'User Guide', 'Administration', 'API & Automation', 'Troubleshooting'])
    expect(container.textContent).toContain('Your first test')
    expect(container.textContent).toContain('First_HTTP_Test')
    expect(container.querySelector('[data-table-pagination="off"]')).not.toBeNull()
    expect(container.querySelector('h2')?.textContent).toBe('In this area')
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Your first test')
  })
  it('makes operator code examples keyboard-focusable with an accessible name', async () => {
    await navigate('#/docs?guide=user-guide&article=dsl-operator-guide')
    await render()
    const examples = Array.from(container.querySelectorAll<HTMLElement>('pre[role="region"][aria-label="json code example"]'))
    expect(examples).toHaveLength(5)
    for (const example of examples) {
      expect(example.tabIndex).toBe(0)
      example.focus()
      expect(document.activeElement).toBe(example)
      expect(() => JSON.parse(example.textContent!)).not.toThrow()
    }
  })
  it('preserves MCP and authentication aliases inside API & Automation', async () => {
    await render({ activePage: 'mcp' })
    expect(container.textContent).toContain('MCP setup')
    expect(container.textContent).toContain('.venv/bin/python')
    expect(container.querySelector('nav[aria-label="Help areas"] a[aria-current="page"]')?.textContent).toBe('API & Automation')
    await navigate('#/docs?guide=authentication')
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('API authentication')
    expect(container.textContent).toContain('Service-account requests must satisfy both RBAC role checks and token scopes')
  })
  it('guards both area and article-level administrator deep links', async () => {
    await render({ activePage: 'admin-guide', effectiveAccessResources: access(['view_scripts']) })
    expect(container.querySelector('nav[aria-label="Help areas"]')?.textContent).not.toContain('Administration')
    expect(container.textContent).toContain('Your first test')
    await navigate('#/docs?guide=user-guide&article=identity')
    expect(container.textContent).toContain('Administration requires administrative access')
    // The address names the article actually shown, not the denied one.
    expect(window.location.hash).not.toContain('article=identity')
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Your first test')
  })
  it('does not expose administration while access is unresolved or failed', async () => {
    await render({ activePage: 'admin-guide', effectiveAccessResources: access(['manage_system']), effectiveAccessLoaded: false })
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Your first test')
    await render({ activePage: 'admin-guide', effectiveAccessResources: access(['manage_system']), effectiveAccessError: true })
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Your first test')
  })
  it('renders a bounded task article and safe in-app references', async () => {
    await navigate('#/docs?guide=user-guide&article=scenarios')
    await render()
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Model load')
    expect(container.querySelector('.markdown-body')?.textContent).toContain('250 VUsers')
    expect(container.querySelector('.markdown-body')?.textContent).not.toContain('The Access Console')
    await navigate('#/repository-guide?article=api-workflows')
    expect(container.querySelector('a[href="#/swagger"]')).not.toBeNull()
    expect(container.querySelector('.markdown-body')?.textContent).not.toContain('does not yet wait for run N')
  })
  it('removes indexed administrator results immediately when access changes', async () => {
    await render({ effectiveAccessResources: access(['manage_system']) })
    const input = container.querySelector<HTMLInputElement>('input[type="search"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Controller startup compatibility')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 200)) })
    expect(container.querySelector('.help-search-results')?.textContent).toContain('Controller startup compatibility')
    await render({ effectiveAccessResources: access(['view_scripts']) })
    expect(Array.from(container.querySelectorAll('.help-search-results li a')).some(a => a.textContent === 'Controller startup compatibility')).toBe(false)
  })
  it('updates copyable API examples only for a safe controller URL', async () => {
    await navigate('#/docs?guide=general&article=api-overview'); await render()
    const input = container.querySelector<HTMLInputElement>('input[type="url"]')!
    const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    const update = async (value: string) => act(async () => { setValue.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
    await update('https://load.example.test:8443/api/v1')
    expect(container.querySelector('.markdown-body')?.textContent).toContain('export APEXLOAD_URL=https://load.example.test:8443/api/v1')
    for (const invalid of ['https://load.example.test/;whoami', 'https://user:password@load.example.test/api/v1', 'javascript:alert(1)', 'https://load.example.test/api/v1?token=secret']) {
      await update(invalid)
      expect(input.getAttribute('aria-invalid')).toBe('true')
      expect(container.querySelector('.markdown-body')?.textContent).not.toContain(invalid)
    }
  })
  it('canonicalizes source section links to the correct task article', async () => {
    await navigate('#/docs?guide=user-guide&article=orientation&section=validating-and-replaying'); await render()
    expect(window.location.hash).toContain('article=replay')
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Validate & replay')
    expect(container.querySelector('nav[aria-label="Help articles"] a[aria-current="page"]')?.textContent).toBe('Validate & replay')
  })
  it('reports an unknown article and falls back to a usable guide', async () => {
    await navigate('#/docs?article=unknown'); await render()
    expect(container.textContent).toContain('That article is not in this build')
    expect(container.querySelector('.help-article-header h2')?.textContent).toBe('Your first test')
  })
  it('copies the displayed example and its shareable article URL', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await render()
    const buttons = Array.from(container.querySelectorAll('button'))
    await act(async () => buttons.find(b => b.textContent === 'Copy example')!.click())
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('HTTPServer'))
    expect(container.textContent).toContain('Copied')
    await act(async () => buttons.find(b => b.textContent === 'Copy article link')!.click())
    expect(writeText).toHaveBeenLastCalledWith(expect.stringContaining('article=first-test'))
  })
  it('keeps a useful recovery message if clipboard access fails', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } })
    await render()
    await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Copy example')!.click())
    expect(container.textContent).toContain('Copy unavailable. Select the example and copy manually.')
  })
})
