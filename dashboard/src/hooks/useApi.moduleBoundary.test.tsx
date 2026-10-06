// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

// Gate a real service module, so both an inline provider assembly and a
// runtime import of the separated provider would prevent consumer loading.
const service = vi.hoisted(() => {
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release, started: 0, completed: false }
})
vi.mock('./api/useTestApi', async () => {
  service.started++
  await service.ready
  const actual = await vi.importActual('./api/useTestApi')
  service.completed = true
  return actual
})

it('loads consumer hooks without loading the provider service assembly', async () => {
  let leaf: typeof import('./useApi') | undefined
  const imported = import('./useApi').then(module => { leaf = module; return module })
  const node = document.createElement('div')
  const root = createRoot(node)
  let optional: unknown = 'not rendered'
  try {
    await vi.waitFor(() => expect(leaf).toBeDefined())
    expect(service.started).toBe(0)
    function Consumer() {
      optional = leaf!.useOptionalApi()
      return null
    }
    await act(async () => root.render(<Consumer />))
    expect(optional).toBeNull()
    expect(service.started).toBe(0)
    expect(typeof leaf!.ApiError).toBe('function')
  } finally {
    service.release()
    await imported
    if (service.started) await vi.waitFor(() => expect(service.completed).toBe(true))
    await act(async () => root.unmount())
  }
})
