// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import SwaggerDocsPage, { parseSwaggerDocument } from './SwaggerDocsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('SwaggerDocsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
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
      summary: 'Get Widget',
      operationId: 'handleGetWidget',
      roles: 'Requires widget viewer',
      tags: ['Widgets'],
      parameters: [{ name: 'id', location: 'path', required: true, type: 'string' }],
      responses: [{ status: '200', description: 'Successful operation' }],
    })
  })

  it('renders a searchable API list with endpoint details', async () => {
    await act(async () => {
      root.render(<SwaggerDocsPage />)
    })

    expect(container.textContent).toContain('Swagger Docs')
    expect(container.textContent).toContain('APEX Load API')
    expect(container.textContent).toContain('API Reference')
    expect(container.textContent).toContain('Create Domain')
    expect(container.textContent).toContain('/api/v1/domains')
    expect(container.textContent).toContain('Operation ID')
    expect(container.textContent).toContain('Access Control')

    const searchInput = container.querySelector('input[placeholder^="Search method"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    await act(async () => {
      if (!searchInput) return
      setInputValue(searchInput, 'health')
    })

    expect(container.textContent).toContain('/api/v1/system/health')
    expect(container.textContent).not.toContain('Create Domain')
  })
})
