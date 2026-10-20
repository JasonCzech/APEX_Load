// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import JavaHTTPDiagnostics from './JavaHTTPDiagnostics'
import type { JavaHTTPActions, JavaHTTPSummary } from '../types/javaHttp'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('keeps older absence hidden and renders explicit refused or partial evidence', async () => {
  const container = document.createElement('div'), root = createRoot(container)
  try {
    await act(async () => root.render(<JavaHTTPDiagnostics />)); expect(container.textContent).toBe('')
    await act(async () => root.render(<JavaHTTPDiagnostics evidence={{ version: 9 }} />))
    expect(container.textContent).toContain('Diagnostics unavailable')
    expect(container.textContent).toContain('JVM object footprint is unavailable')
    const evidence: JavaHTTPActions = { version: 1, incomplete: true, actions: { '<script>request</script>': { population: 2, diagnostics: { version: 9 } as JavaHTTPSummary } } }
    await act(async () => root.render(<JavaHTTPDiagnostics evidence={evidence} />))
    expect(container.textContent).toContain('Source coverage incomplete')
    expect(container.querySelector('summary')?.textContent).toBe('<script>request</script>')
    expect(container.querySelector('script')).toBeNull()
    expect(container.textContent).toContain('unsupported or inconsistent evidence')
  } finally { act(() => root.unmount()) }
})
