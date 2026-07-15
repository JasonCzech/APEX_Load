// @vitest-environment jsdom

import { act, useEffect } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  selectActiveTopbarContribution,
  useActiveTopbarContribution,
  useTopbarContribution,
  type TopbarContribution,
} from './TopbarContributionProvider'
import { TopbarContributionProvider } from './TopbarContributionRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TopbarContributionProvider', () => {
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
    vi.clearAllMocks()
  })

  it('selects the highest-priority contribution and falls back to the newest peer', () => {
    expect(selectActiveTopbarContribution([
      { id: 'older-peer', title: 'Older', priority: 10, sequence: 1 },
      { id: 'newer-peer', title: 'Newer', priority: 10, sequence: 2 },
      { id: 'lower', title: 'Lower', priority: 1, sequence: 3 },
    ])?.id).toBe('newer-peer')

    expect(selectActiveTopbarContribution([
      { id: 'newer-peer', title: 'Newer', priority: 10, sequence: 2 },
      { id: 'higher', title: 'Higher', priority: 20, sequence: 1 },
    ])?.id).toBe('higher')
  })

  it('cleans up a page contribution on unmount', async () => {
    const onActive = vi.fn()

    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <Reader onActive={onActive} />
          <Publisher contribution={{ id: 'script-editor', title: 'Edit Script', priority: 80 }} />
        </TopbarContributionProvider>,
      )
    })

    expect(onActive).toHaveBeenLastCalledWith('script-editor')

    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <Reader onActive={onActive} />
        </TopbarContributionProvider>,
      )
    })

    expect(onActive).toHaveBeenLastCalledWith(null)
  })
})

function Publisher({ contribution }: { contribution: TopbarContribution }) {
  useTopbarContribution(contribution)
  return null
}

function Reader({ onActive }: { onActive: (id: string | null) => void }) {
  const active = useActiveTopbarContribution()
  useEffect(() => {
    onActive(active?.id || null)
  }, [active?.id, onActive])
  return null
}
