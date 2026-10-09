// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useSectionListKeys } from './useSectionListKeys'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function SectionList({ current }: { current: string }) {
  const ref = useSectionListKeys()
  return <nav ref={ref} aria-label="Sections">
    <input aria-label="Find" />
    {['General', 'Pools', 'Clusters'].map(name => <button key={name} type="button" aria-current={current === name ? 'page' : undefined}>{name}</button>)}
    <button type="button" disabled>Unavailable</button>
  </nav>
}

describe('useSectionListKeys', () => {
  let host: HTMLDivElement
  let root: Root
  beforeEach(() => { host = document.createElement('div'); document.body.append(host); root = createRoot(host) })
  afterEach(() => { act(() => root.unmount()); host.remove() })
  const button = (name: string) => Array.from(host.querySelectorAll('button')).find(item => item.textContent === name)!
  const press = (key: string) => act(() => { document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true })) })

  it('keeps one Tab stop on the current entry and moves with Arrow, Home and End', () => {
    act(() => root.render(<SectionList current="Pools" />))
    expect(['General', 'Pools', 'Clusters'].map(name => button(name).tabIndex)).toEqual([-1, 0, -1])
    act(() => button('Pools').focus())
    press('ArrowDown')
    expect(document.activeElement).toBe(button('Clusters'))
    expect(button('Clusters').tabIndex).toBe(0)
    press('ArrowDown')
    expect(document.activeElement).toBe(button('General'))
    press('End')
    expect(document.activeElement).toBe(button('Clusters'))
    press('Home')
    expect(document.activeElement).toBe(button('General'))
    press('ArrowLeft')
    expect(document.activeElement).toBe(button('Clusters'))
  })

  it('leaves typing in the list search box alone', () => {
    act(() => root.render(<SectionList current="General" />))
    const input = host.querySelector('input')!
    act(() => input.focus())
    press('ArrowDown')
    expect(document.activeElement).toBe(input)
  })
})
