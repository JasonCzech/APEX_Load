// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { API_ORIGIN, setApiOrigin } from '../hooks/apiClient'
import SwaggerOperationDetail, { CopyableCode } from './SwaggerOperationDetail'
import { parseSwaggerDocument } from './swaggerDocsParser'
import { referenceFixture } from './swaggerDetails.fixture'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('API request and response explorer', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.restoreAllMocks()
  })
  async function render(source = referenceFixture) {
    const document = parseSwaggerDocument(source)
    await act(async () => root.render(<SwaggerOperationDetail operation={document.operations[0]} document={document} />))
  }
  async function select(label: string, value: string) {
    const field = Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith(label))?.querySelector('select')
    expect(field).toBeTruthy()
    await act(async () => { field!.value = value; field!.dispatchEvent(new Event('change', { bubbles: true })) })
  }
  it('shows bodies, explores nested references, switches response status and reports clipboard success and failure', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await render()
    expect(container.textContent).toContain("O'Brien")
    expect(container.textContent).toContain('Exact revision')
    const nested = Array.from(container.querySelectorAll('.swagger-schema-node summary')).find(element => element.textContent?.includes('nested'))
    expect(nested).toBeTruthy()
    expect(container.textContent).not.toContain('Recursive reference')
    await act(async () => (nested as HTMLElement).click())
    const arrayItem = Array.from(container.querySelectorAll('.swagger-schema-node summary')).find(element => element.textContent?.includes('Array item'))
    expect(arrayItem).toBeTruthy()
    await act(async () => (arrayItem as HTMLElement).click())
    expect(container.textContent).toContain('Recursive reference')
    await select('Response status', '400')
    expect(container.textContent).toContain('Invalid request')
    expect(container.textContent).toContain('No example body is documented')
    expect(container.querySelector('.swagger-schema .swagger-required')).not.toBeNull()
    const copy = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Copy example body')!
    await act(async () => copy.click())
    expect(writeText).toHaveBeenCalledWith(JSON.stringify({ name: "O'Brien", active: false }, null, 2))
    expect(container.textContent).toContain('Copied to clipboard.')
    writeText.mockRejectedValueOnce(new Error('Permission denied'))
    await act(async () => copy.click())
    expect(container.textContent).toContain('Copy failed. Select and copy the text below.')
  })
  it('uses the configured controller origin and updates an open request after a desktop origin change', async () => {
    const original = API_ORIGIN
    try {
      setApiOrigin('http://127.0.0.1:56789')
      await render()
      expect(container.querySelector('pre[aria-label="cURL request"]')?.textContent).toContain('http://127.0.0.1:56789/api/v1/widgets/')
      await act(async () => setApiOrigin('https://controller.example'))
      expect(container.querySelector('pre[aria-label="cURL request"]')?.textContent).toContain('https://controller.example/api/v1/widgets/')
    } finally { await act(async () => setApiOrigin(original)) }
  })
  it('does not label changed code as copied after an older clipboard request completes', async () => {
    let finish!: () => void
    const writeText = vi.fn().mockImplementation(() => new Promise<void>(resolve => { finish = resolve }))
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    await act(async () => root.render(<CopyableCode label="Request" text="old request" />))
    await act(async () => container.querySelector('button')!.click())
    await act(async () => root.render(<CopyableCode label="Request" text="new request" />))
    await act(async () => finish())
    expect(container.querySelector('[role="status"]')?.textContent).toBe('')
    expect(container.querySelector('pre')?.textContent).toBe('new request')
  })
  it('opens on the success representation when wildcard success and numeric errors are mixed', async () => {
    await render(referenceFixture.replace("        '201':", "        '2XX':"))
    const status = Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith('Response status'))!.querySelector('select')!
    expect(status.value).toBe('2XX')
    expect(container.textContent).toContain('widget-1')
    expect(container.textContent).not.toContain('No example body is documented')
  })
  it('distinguishes public, no-body and unspecified contracts, and handles malformed details locally', async () => {
    await render(referenceFixture.replace('    post:\n', '    get:\n      security: []\n').replace("        '201':", "        '204':"))
    expect(container.textContent).toContain('No authentication required')
    expect(container.textContent).toContain('No response body.')
    expect(container.querySelector('pre[aria-label="Example body"]')?.textContent).toContain('active')
    await render(referenceFixture.replace('      operationId: widgets', '      operationId: widgets\n      broken: [unclosed'))
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('could not be parsed')
  })
})
