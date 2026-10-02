// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { JavaDependencyInventory } from './JavaDependencyInventory'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('shows bounded binary provenance without presenting runtime provisioning or binary contents', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const files = Array.from({ length: 51 }, (_, index) => ({ source_path: `lib-${index}.jar`, role: 'jar', size_bytes: 12, sha256: 'a'.repeat(64), binary: 'DO_NOT_RENDER_BYTES' }))
  act(() => root.render(<JavaDependencyInventory metadata={{ java_dependency_inventory: {
    contract: 'apexload.java-dependency-inventory.v1', runtime_provisioned: false,
    counts: { jars: 51, classes: 1, executables: 1 }, files,
  } }} />))
  expect(container.textContent).toContain('51 JARs')
  expect(container.textContent).toContain('not installed on runners')
  expect(container.querySelectorAll('li')).toHaveLength(50)
  expect(container.textContent).toContain('Showing 50 of 51')
  expect(container.textContent).toContain('SHA-256')
  expect(container.textContent).not.toContain('DO_NOT_RENDER_BYTES')
  act(() => root.unmount())
})
it('ignores unknown contracts', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => root.render(<JavaDependencyInventory metadata={{ java_dependency_inventory: { contract: 'unknown', files: [] } }} />))
  expect(container.textContent).toBe('')
  act(() => root.unmount())
})
