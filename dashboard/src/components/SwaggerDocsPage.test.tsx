// @vitest-environment jsdom

import { act, type ComponentProps } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import endpointCatalogRaw from '../../../docs/api-endpoint-catalog.json?raw'
import swaggerYamlFixture from '../../../docs/swagger.yaml?raw'

import { loadApiEndpointCatalog, loadSwaggerYaml } from '../apiDocsClient'
import { parseEndpointCatalog } from '../apiEndpointCatalog'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import SwaggerDocsPage from './SwaggerDocsPage'
import { parseSwaggerDocument } from './swaggerDocsParser'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../apiDocsClient', () => ({
  loadApiEndpointCatalog: vi.fn(),
  loadSwaggerYaml: vi.fn(),
}))

const endpointCatalogFixture = parseEndpointCatalog(endpointCatalogRaw)
const loadApiEndpointCatalogMock = vi.mocked(loadApiEndpointCatalog)
const loadSwaggerYamlMock = vi.mocked(loadSwaggerYaml)

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function accessResources(operationIDs: string[]) {
  return [{
    id: 'test',
    name: 'Test',
    description: 'Test operations',
    operations: operationIDs.map(id => ({ id, label: id, allowed: true })),
  }]
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
    loadApiEndpointCatalogMock.mockReset()
    loadSwaggerYamlMock.mockReset()
    loadApiEndpointCatalogMock.mockResolvedValue(endpointCatalogFixture)
    loadSwaggerYamlMock.mockResolvedValue(swaggerYamlFixture)
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

  async function renderSwaggerDocsPage(props: ComponentProps<typeof SwaggerDocsPage> = {}) {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <SwaggerDocsPage {...props} />
        </TopbarContributionProvider>,
      )
    })
    await act(async () => {})
  }

  function topbarText() {
    return container.querySelector('[data-testid="topbar-contribution"]')?.textContent || ''
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
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
                payload:
                  type: string
                  format: binary
                metadata:
                  type: object
                  properties:
                    nested:
                      type: string
      responses:
        '200':
          description: Successful operation
          content:
            application/json:
              schema:
                type: object
                properties:
                  result:
                    type: string
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
      requestBody: {
        required: true,
        media: [{
          mediaType: 'application/json',
          schemaType: 'object',
          fields: [
            { name: 'name', type: 'string', format: '' },
            { name: 'payload', type: 'string', format: 'binary' },
            { name: 'metadata', type: 'object', format: '' },
          ],
        }],
      },
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

    expect(container.textContent).toContain('API Reference (OpenAPI)')
    expect(topbarText()).toContain('Help & documentation')
    expect(topbarText()).toContain('API & Automation')
    expect(topbarText()).not.toContain('Repo Guide')
    expect(container.querySelector('nav[aria-label="Help areas"] a[aria-current="page"]')?.textContent).toBe('API & Automation')
    expect(container.textContent).toContain('APEX Load API')
    expect(container.textContent).toContain('API Reference')
    expect(container.textContent).toContain('Categories')
    expect(container.textContent).toContain('Script Authoring')
    expect(container.textContent).toContain('Test Execution')
    expect(container.textContent).toContain('System & Distribution')
    expect(container.textContent).toContain('Create Domain')
    expect(container.textContent).toContain('/api/v1/domains')
    expect(container.textContent).toContain('Not in my access')
    expect(container.textContent).toContain('Public')
    expect(container.textContent).toContain('Conditional auth')

    const firstDetails = container.querySelector('.swagger-operation-card') as HTMLDetailsElement | null
    expect(firstDetails).not.toBeNull()
    await act(async () => {
      if (!firstDetails) return
      firstDetails.open = true
      firstDetails.dispatchEvent(new Event('toggle', { bubbles: true }))
    })
    expect(container.textContent).toContain('Operation ID')
    expect(container.textContent).toContain('Access Control')
    expect(container.textContent).toContain('OpenAPI Tags')

    const ciRunSummary = Array.from(container.querySelectorAll('.swagger-operation-card summary'))
      .find(summary => summary.textContent?.includes('/api/v1/ci/runs') && summary.textContent?.includes('POST'))
    expect(ciRunSummary).toBeDefined()
    await act(async () => {
      const details = ciRunSummary?.closest('details') as HTMLDetailsElement | null
      if (!details) return
      details.open = true
      details.dispatchEvent(new Event('toggle', { bubbles: true }))
    })
    expect(container.textContent).toContain('Request Body (required)')
    expect(container.textContent).toContain('project_id')

    const categorySelect = container.querySelector('.swagger-docs-controls select') as HTMLSelectElement | null
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

    const authLink = Array.from(container.querySelectorAll('a')).find(link => link.getAttribute('href') === '#/docs?guide=general&article=authentication')
    expect(authLink?.textContent, Array.from(container.querySelectorAll('a')).map(a => a.getAttribute('href')).join(' | ')).toBe('API authentication')

    // Exercise the entire generated catalog and its filters without making
    // concurrent full-suite scheduling part of the rendering contract.
  }, 30_000)

  it('shows catalog-backed access badges and filters to current access on demand', async () => {
    await renderSwaggerDocsPage({ effectiveAccessResources: accessResources(['view_scripts']) })

    expect(container.textContent).toContain('My access')
    expect(container.textContent).toContain('Not in my access')

    const accessCheckbox = container.querySelector('.swagger-docs-access-filter input[type="checkbox"]') as HTMLInputElement | null
    expect(accessCheckbox).not.toBeNull()
    await act(async () => {
      if (!accessCheckbox) return
      accessCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const codeValues = Array.from(container.querySelectorAll('.swagger-operation-card summary code')).map(code => code.textContent || '')
    expect(codeValues).toContain('/api/v1/scripts')
    expect(codeValues).toContain('/api/v1/system/health')
    expect(codeValues).not.toContain('/api/v1/projects')
    const scriptRows = Array.from(container.querySelectorAll('.swagger-operation-card summary')).map(summary => summary.textContent || '')
    expect(scriptRows.some(row => row.includes('GET') && row.includes('/api/v1/scripts'))).toBe(true)
    expect(scriptRows.some(row => row.includes('POST') && row.includes('/api/v1/scripts') && row.includes('Create Script'))).toBe(false)
  }, 10_000)

  it('shows a controller docs error when the OpenAPI document cannot be loaded', async () => {
    loadSwaggerYamlMock.mockRejectedValueOnce(new Error('swagger route unavailable'))

    await renderSwaggerDocsPage()

    expect(container.textContent).toContain('API Docs Unavailable')
    expect(container.textContent).toContain('swagger route unavailable')
    expect(container.querySelector('.swagger-docs-results')).toBeNull()
    expect(container.querySelector('.swagger-docs-controls')).toBeNull()
  })
})
