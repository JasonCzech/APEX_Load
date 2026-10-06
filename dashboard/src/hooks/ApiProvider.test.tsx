// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { ApiProvider } from './ApiProvider'
import { useApi, useOptionalApi } from './useApi'

it('shares one context identity across required and optional consumers and scopes nested providers', async () => {
  const node = document.createElement('div')
  const root = createRoot(node)
  const observed = new Map<string, { required: ReturnType<typeof useApi>; optional: ReturnType<typeof useOptionalApi> }>()
  function Consumer({ name }: { name: string }) {
    observed.set(name, { required: useApi(), optional: useOptionalApi() })
    return null
  }
  const tree = (nested: boolean) => <ApiProvider>
    <Consumer name="outer" />
    {nested ? <ApiProvider><Consumer name="inner" /></ApiProvider> : <Consumer name="inner" />}
    <Consumer name="sibling" />
  </ApiProvider>
  try {
    await act(async () => root.render(tree(true)))
    const outer = observed.get('outer')!.required
    const inner = observed.get('inner')!.required
    expect(outer).not.toBe(inner)
    expect(observed.get('sibling')!.required).toBe(outer)
    for (const value of observed.values()) expect(value.optional).toBe(value.required)
    expect(typeof inner.listTests).toBe('function')
    expect(typeof inner.listAppDynamicsConnections).toBe('function')

    await act(async () => root.render(tree(false)))
    expect(observed.get('outer')!.required).toBe(outer)
    expect(observed.get('inner')!.required).toBe(outer)
    expect(observed.get('inner')!.optional).toBe(outer)
    expect(observed.get('sibling')!.required).toBe(outer)
  } finally {
    await act(async () => root.unmount())
  }
})
