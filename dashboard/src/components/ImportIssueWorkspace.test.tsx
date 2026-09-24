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
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Edit runtime settings')!
    act(() => edit.click())
    expect(onNavigate).toHaveBeenCalledWith('runtime')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
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
    expect(container.textContent).toContain('Reviewed with limitation (1)')
    act(() => root.render(<ImportIssueWorkspace issues={[{ id: 'stable-id', severity: 'warning', title: 'Warning', description: 'New evidence' }]} />))
    expect(container.textContent).toContain('Reviewed with limitation (0)')
    expect(container.querySelector<HTMLInputElement>('input[type=search]')?.value).toBe('')
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).toContain('New evidence')
    expect(container.querySelector('.shared-import-issue-detail')?.textContent).not.toContain('Old evidence')
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
