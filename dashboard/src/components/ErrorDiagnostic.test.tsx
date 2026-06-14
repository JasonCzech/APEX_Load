// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import ErrorDiagnostic from './ErrorDiagnostic'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ErrorDiagnostic', () => {
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

  it('renders an accessible diagnostic with likely cause and next steps', async () => {
    await act(async () => {
      root.render(<ErrorDiagnostic error={new TypeError('Failed to fetch')} fallback="Unable to load auth status" />)
    })

    expect(container.querySelector('[role="alert"]')).not.toBeNull()
    expect(container.textContent).toContain('API controller is unreachable')
    expect(container.textContent).toContain('Likely cause:')
    expect(container.textContent).toContain('Docker Compose stack or controller process')
  })
})
