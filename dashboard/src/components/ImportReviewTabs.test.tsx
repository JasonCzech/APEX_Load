// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import ImportReviewTabs, { ImportReviewTabList, type ImportReviewTab } from './ImportReviewTabs'

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
    expect(tabs.map(tab => tab.getAttribute('aria-label'))).toEqual(['Review', 'Credentials, 2 review items', 'Data & parameters', 'Runtime settings', 'DSL Preview', 'Source Mapping'])
    const input = container.querySelector('input')!
    input.value = 'Edited draft'
    act(() => {
      tabs[0].focus()
      tabs[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    })
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
    // The count is in the name once; the visible badge is hidden from assistive tech.
    expect(tabs.map(tab => tab.getAttribute('aria-label'))).toEqual(['Review, 2 review items', 'Runtime settings', 'Source Mapping'])
    expect(tabs[0].querySelector('span')?.getAttribute('aria-hidden')).toBe('true')
    expect(tabs[0].textContent).toContain('2')
    expect(tabs[1].querySelector('span')).toBeNull()
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

it('renders custom tab lists with checking indicators, custom labels, badge/active classes, and full keyboard navigation', () => {
  const customTabs = [
    { id: 'overview', label: 'Review' },
    { id: 'credentials', label: 'Credentials' },
    { id: 'data', label: 'Data & parameters' },
    { id: 'preserved', label: 'Runtime settings' },
    { id: 'dsl', label: 'DSL Preview' },
    { id: 'source', label: 'Source Mapping' },
  ] as const
  type CustomTab = typeof customTabs[number]['id']

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  function CustomReview({ checking }: { checking: boolean }) {
    const [active, setActive] = useState<CustomTab>('overview')
    return <ImportReviewTabList
      tabs={customTabs}
      active={active}
      onChange={setActive}
      counts={{ overview: 3, credentials: 1, data: 0 }}
      checkingTabs={checking ? { overview: true, credentials: true } : undefined}
      tabId={tab => `lr-validation-review-tab-${tab}`}
      panelId="lr-validation-review-panel"
      className="lr-validation-review-tabs"
      badgeClassName="lr-tab-count"
      activeClassName="active"
      ariaLabel="Script archive review sections"
      countSingularLabel="unresolved grouped issue"
      countPluralLabel="unresolved grouped issues"
    />
  }
  try {
    act(() => root.render(<CustomReview checking={true} />))
    const tablist = container.querySelector('[role="tablist"]')!
    expect(tablist.className).toBe('lr-validation-review-tabs')
    expect(tablist.getAttribute('aria-label')).toBe('Script archive review sections')
    const checkingTabs = [...container.querySelectorAll<HTMLButtonElement>('[role=tab]')]
    expect(checkingTabs[0].getAttribute('aria-label')).toBe('Review, checking issues')
    expect(checkingTabs[0].querySelector('.lr-tab-count')?.textContent).toBe('…')
    expect(checkingTabs[0].querySelector('.lr-tab-count')?.getAttribute('title')).toBe('Checking…')
    expect(checkingTabs[0].className).toBe('active')
    expect(checkingTabs[1].className).toBe('')

    act(() => root.render(<CustomReview checking={false} />))
    const tabs = [...container.querySelectorAll<HTMLButtonElement>('[role=tab]')]
    expect(tabs.map(tab => tab.getAttribute('aria-label'))).toEqual([
      'Review, 3 unresolved grouped issues',
      'Credentials, 1 unresolved grouped issue',
      'Data & parameters',
      'Runtime settings',
      'DSL Preview',
      'Source Mapping',
    ])
    expect(tabs[0].querySelector('.lr-tab-count')?.textContent).toBe('3')
    expect(tabs[1].querySelector('.lr-tab-count')?.textContent).toBe('1')
    expect(tabs[2].querySelector('.lr-tab-count')).toBeNull()

    act(() => {
      tabs[0].focus()
      tabs[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }))
    })
    expect(document.activeElement).toBe(tabs[5])
    expect(tabs[5].getAttribute('aria-selected')).toBe('true')
    expect(tabs[5].className).toBe('active')

    act(() => {
      tabs[5].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    })
    expect(document.activeElement).toBe(tabs[0])
    expect(tabs[0].getAttribute('aria-selected')).toBe('true')

    act(() => {
      tabs[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
    })
    expect(document.activeElement).toBe(tabs[5])
    expect(tabs[5].getAttribute('aria-selected')).toBe('true')

    act(() => {
      tabs[5].dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }))
    })
    expect(document.activeElement).toBe(tabs[0])
    expect(tabs[0].getAttribute('aria-selected')).toBe('true')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
