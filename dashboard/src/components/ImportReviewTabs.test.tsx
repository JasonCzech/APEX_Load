// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import ImportReviewTabs, { type ImportReviewTab } from './ImportReviewTabs'

it('keeps edits while keyboard navigation selects the labelled review panel', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  function Review() {
    const [active, setActive] = useState<ImportReviewTab>('review')
    return <ImportReviewTabs active={active} onChange={setActive} counts={{ credentials: 2 }}>
      <div hidden={active !== 'review'}><input aria-label="Draft name" defaultValue="Original" /></div>
      <div hidden={active !== 'credentials'}>Required credentials</div>
    </ImportReviewTabs>
  }
  try {
    act(() => root.render(<Review />))
    const tabs = [...container.querySelectorAll<HTMLButtonElement>('[role=tab]')]
    expect(tabs.map(tab => tab.getAttribute('aria-label'))).toEqual(['Review', 'Credentials', 'Data & parameters', 'Runtime settings', 'DSL Preview', 'Source Mapping'])
    const input = container.querySelector('input')!
    input.value = 'Edited draft'
    act(() => {
      tabs[0].focus()
      tabs[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    })
    expect(container.querySelector(`[id="${tabs[1].getAttribute('aria-describedby')}"]`)?.textContent).toBe('2 review items')
    expect(document.activeElement).toBe(tabs[1])
    expect(tabs[1].getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('[role=tabpanel]')?.getAttribute('aria-labelledby')).toBe(tabs[1].id)
    expect(input.closest('[hidden]')).not.toBeNull()
    act(() => tabs[0].click())
    expect(container.querySelector('input')).toBe(input)
    expect(input.value).toBe('Edited draft')
    expect(input.closest('[hidden]')).toBeNull()
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('shows only applicable tabs and omits zero-count badges', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportReviewTabs
      active="review"
      onChange={() => {}}
      visibleTabs={['review', 'runtime', 'source']}
      counts={{ review: 2, runtime: 0 }}
    ><p>Review content</p></ImportReviewTabs>))
    const tabs = [...container.querySelectorAll<HTMLButtonElement>('[role=tab]')]
    expect(tabs.map(tab => tab.getAttribute('aria-label'))).toEqual(['Review', 'Runtime settings', 'Source Mapping'])
    expect(tabs[0].textContent).toContain('2')
    expect(tabs[1].querySelector('span')).toBeNull()
    expect(tabs[1].getAttribute('aria-describedby')).toBeNull()
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('falls back to the first applicable tab when the requested tab is hidden', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  function RestrictedReview() {
    const [active, setActive] = useState<ImportReviewTab>('credentials')
    return <ImportReviewTabs active={active} onChange={setActive} visibleTabs={['review', 'runtime']}>
      <p>{active === 'review' ? 'Review content' : 'Hidden content'}</p>
    </ImportReviewTabs>
  }
  try {
    act(() => root.render(<RestrictedReview />))
    const review = container.querySelector<HTMLButtonElement>('[role=tab][aria-label="Review"]')!
    expect(review.getAttribute('aria-selected')).toBe('true')
    expect(review.tabIndex).toBe(0)
    expect(container.querySelector('[role=tabpanel]')?.getAttribute('aria-labelledby')).toBe(review.id)
    expect(container.querySelector('[role=tabpanel]')?.textContent).toBe('Review content')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
