// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

import ImportIssueWorkspace from './ImportIssueWorkspace'
import { groupImportWorkspaceIssues } from './importIssueWorkspaceModel'

it('groups repeated diagnostics and routes the operator to the setting that resolves them', () => {
  const issues = [0, 1].map(index => ({
    id: `group-${index}`,
    severity: 'blocker' as const,
    title: 'Runtime prerequisite',
    description: `jmeter_unbounded_thread_group affects Thread Group ${index + 1}`,
    source: `Thread Group ${index + 1}`,
  }))
  expect(groupImportWorkspaceIssues(issues)).toMatchObject([{
    title: 'Set a finite JMeter termination rule',
    targetTab: 'runtime',
    occurrences: [{ id: 'group-0' }, { id: 'group-1' }],
  }])

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const onNavigate = vi.fn()
  try {
    act(() => root.render(<ImportIssueWorkspace issues={issues} onNavigate={onNavigate} />))
    expect(container.textContent).toContain('2 occurrences')
    expect(Array.from(container.querySelectorAll('.shared-import-issue-row .import-issue-severity-tag')).map(tag => tag.textContent)).toEqual(['Blocker'])
    expect(container.querySelector('.shared-import-issue-detail .import-issue-severity-tag')).toBeNull()
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Edit runtime settings')!
    act(() => edit.click())
    expect(onNavigate).toHaveBeenCalledWith('runtime', expect.objectContaining({ title: 'Set a finite JMeter termination rule' }))
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('merges the same preserved credential reported by two converter channels', () => {
  const description = 'imported credential at line:6 [warning]: the imported credential was retained in the runnable destination script and should be reviewed by the operator'
  const grouped = groupImportWorkspaceIssues([
    { id: 'section', severity: 'warning', title: 'Import Diagnostics', description, source: '1 import diagnostic requires review' },
    { id: 'unsupported', severity: 'warning', title: 'Unsupported element', description, source: 'Import conversion' },
  ])
  expect(grouped).toHaveLength(1)
  expect(grouped[0]).toMatchObject({ title: 'Review preserved credential', targetTab: 'credentials', actionLabel: 'Review credential handling' })
  expect(grouped[0].occurrences).toHaveLength(1)
  expect(grouped[0].impact).not.toContain('line:6')
})

it('keeps diagnostics in distinct source files as separate occurrences', () => {
  const description = 'imported credential at line:6 [warning]: the imported credential was retained in the runnable destination script and should be reviewed by the operator'
  const grouped = groupImportWorkspaceIssues([
    { id: 'first', severity: 'warning', title: 'Import Diagnostics', description, source: 'a.jmx' },
    { id: 'second', severity: 'warning', title: 'Import Diagnostics', description, source: 'b.jmx' },
  ])
  expect(grouped).toHaveLength(1)
  expect(grouped[0].occurrences).toHaveLength(2)
})

it('clears local review state when a regenerated review reuses an issue id', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportIssueWorkspace issues={[{ id: 'stable-id', severity: 'warning', title: 'Warning', description: 'Old evidence' }]} />))
    const search = container.querySelector<HTMLInputElement>('input[type=search]')!
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'Old')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Mark reviewed with limitation')!.click())
    expect(container.textContent).toContain('Reviewed (1)')
    act(() => root.render(<ImportIssueWorkspace issues={[{ id: 'stable-id', severity: 'warning', title: 'Warning', description: 'New evidence' }]} />))
    expect(container.textContent).toContain('Reviewed (0)')
    expect(container.querySelector<HTMLInputElement>('input[type=search]')?.value).toBe('')
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).toContain('New evidence')
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).not.toContain('Old evidence')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('keeps blockers unacknowledgeable and explains their impact before source evidence', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportIssueWorkspace issues={[{
      id: 'source-blocker', severity: 'blocker', title: 'Unsupported source call',
      description: 'The source call has no supported replay equivalent.', source: 'plan.jmx:12',
    }]} />))
    const detail = container.querySelector('.shared-import-issue-detail')!
    expect(detail.querySelector('[aria-label="Problem and impact"]')?.textContent).toContain('no supported replay equivalent')
    expect(detail.querySelector('[aria-label="Available resolution"]')?.textContent).toContain('inspect it again')
    expect(Array.from(detail.querySelectorAll('button')).some(button => button.textContent?.includes('Mark reviewed'))).toBe(false)
    expect(detail.querySelector('details')?.textContent).toContain('plan.jmx:12')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('groups issue navigation by the review decision it supports', () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportIssueWorkspace issues={[
      { id: 'mapping', severity: 'warning', title: 'Source mapping unavailable', description: 'source_order_unavailable' },
      { id: 'termination', severity: 'blocker', title: 'Unbounded thread group', description: 'jmeter_unbounded_thread_group' },
    ]} />))
    expect(Array.from(container.querySelectorAll('.shared-import-issue-category')).map(item => item.textContent)).toEqual([
      'Runtime settings 1 issue', 'Source mapping 1 issue',
    ])
    expect(Array.from(container.querySelectorAll('.shared-import-issue-list > button')).map(button => button.textContent)).toEqual([
      expect.stringContaining('Set a finite JMeter termination rule'),
      expect.stringContaining('Confirm synthesized source order'),
    ])
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
