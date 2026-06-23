// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { TopbarContributionProvider, useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import SwaggerDocsPage from './SwaggerDocsPage'
import { parseSwaggerDocument } from './swaggerDocsParser'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="topbar-contribution">
      {contribution?.titleNode || contribution?.title}
      {contribution?.controls}
    </div>
  )
}

describe('SwaggerDocsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.history.replaceState(null, '', '/')
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  async function renderSwaggerDocsPage() {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <SwaggerDocsPage />
        </TopbarContributionProvider>,
      )
    })
  }

  function topbarText() {
    return container.querySelector('[data-testid="topbar-contribution"]')?.textContent || ''
  }

  function findTopbarButton(label: string) {
    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    return Array.from(topbar?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === label)
  }

  it('parses operation details from generated OpenAPI YAML', () => {
    const parsed = parseSwaggerDocument(`openapi: 3.0.3
info:
  title: Example API
  description: 'Example API reference.'
  version: 1.2.3
servers:
  - url: http://localhost:8080
paths:
  /api/v1/widgets/{id}:
    get:
      summary: Get Widget
      description: '**Handler:** \`handleGetWidget\`


        **Access Control:** Requires widget viewer'
      operationId: handleGetWidget
      responses:
        '200':
          description: Successful operation
      x-roles: Requires widget viewer
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      tags:
        - Widgets
`)

    expect(parsed.title).toBe('Example API')
    expect(parsed.version).toBe('1.2.3')
    expect(parsed.serverUrl).toBe('http://localhost:8080')
    expect(parsed.operations).toHaveLength(1)
    expect(parsed.operations[0]).toMatchObject({
      method: 'GET',
      path: '/api/v1/widgets/{id}',
      category: 'Widgets',
      summary: 'Get Widget',
      operationId: 'handleGetWidget',
      roles: 'Requires widget viewer',
      tags: ['Widgets'],
      parameters: [{ name: 'id', location: 'path', required: true, type: 'string' }],
      responses: [{ status: '200', description: 'Successful operation' }],
    })
  })

  it('adds broad categories even when an operation has no OpenAPI tags', () => {
    const parsed = parseSwaggerDocument(`openapi: 3.0.3
info:
  title: Example API
  description: |-
    Example API reference.
  version: 1.2.3
servers:
  - url: http://localhost:8080
paths:
  /api/v1/scripts/import/openapi:
    post:
      summary: Import OpenAPI
      operationId: handleImportOpenAPI
      responses:
        "200":
          description: Successful operation
  /api/v1/tests/{id}/start:
    post:
      summary: Start Test
      operationId: handleStartTest
      responses:
        "200":
          description: Successful operation
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
  /metrics:
    get:
      summary: Prometheus Metrics
      operationId: getMetrics
      responses:
        "200":
          description: Successful operation
`)

    expect(parsed.description).toBe('Example API reference.')
    expect(parsed.operations.map(operation => operation.category)).toEqual([
      'Script Authoring',
      'Test Execution',
      'Operations & Observability',
    ])
    expect(parsed.operations[1].parameters).toEqual([{ name: 'id', location: 'path', required: true, type: 'string' }])
  })

  it('renders a searchable API list with endpoint details', async () => {
    await renderSwaggerDocsPage()

    expect(container.textContent).toContain('Swagger Docs')
    expect(topbarText()).toContain('Swagger Docs')
    expect(topbarText()).toContain('General')
    expect(topbarText()).toContain('MCP Server')
    expect(topbarText()).toContain('Quick Start')
    expect(topbarText()).toContain('Authentication')
    expect(topbarText()).toContain('Endpoint Map')
    expect(topbarText()).toContain('Swagger')
    expect(topbarText()).toContain('Repo Guide')
    expect(findTopbarButton('Swagger')?.getAttribute('aria-pressed')).toBe('true')
    expect(container.querySelector('a[href="#/docs"]')).toBeNull()
    expect(container.textContent).toContain('APEX Load API')
    expect(container.textContent).toContain('API Reference')
    expect(container.textContent).toContain('Categories')
    expect(container.textContent).toContain('Script Authoring')
    expect(container.textContent).toContain('Test Execution')
    expect(container.textContent).toContain('System & Distribution')
    expect(container.textContent).toContain('Create Domain')
    expect(container.textContent).toContain('/api/v1/domains')
    expect(container.textContent).toContain('Operation ID')
    expect(container.textContent).toContain('Access Control')
    expect(container.textContent).toContain('OpenAPI Tags')

    const categorySelect = container.querySelector('select') as HTMLSelectElement | null
    expect(categorySelect).not.toBeNull()
    expect(Array.from(categorySelect?.options || []).map(option => option.textContent)).toContain('Script Authoring')
    expect(Array.from(container.querySelectorAll('.swagger-docs-tag-group h4')).map(heading => heading.textContent)).not.toContain('Other')

    const searchInput = container.querySelector('input[placeholder^="Search method"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    await act(async () => {
      if (!searchInput) return
      setInputValue(searchInput, 'health')
    })

    expect(container.textContent).toContain('/api/v1/system/health')
    expect(container.textContent).not.toContain('Create Domain')

    await act(async () => {
      if (!categorySelect) return
      setInputValue(searchInput as HTMLInputElement, '')
      categorySelect.value = 'Test Execution'
      categorySelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('/api/v1/tests')
    expect(container.textContent).not.toContain('/api/v1/scripts/import/openapi')

    const authButton = findTopbarButton('Authentication')
    expect(authButton).toBeDefined()
    await act(async () => {
      authButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(window.location.hash).toBe('#/docs?guide=authentication')
  }, 10_000)
})
