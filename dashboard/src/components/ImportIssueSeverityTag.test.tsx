// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'

import ImportIssueSeverityTag from './ImportIssueSeverityTag'

it.each([
  ['blocker', 'Blocker'],
  ['error', 'Error'],
  ['warning', 'Warning'],
  ['info', 'Review note'],
  ['resolved', 'Resolved'],
] as const)('uses the shared %s label and visual tone', (severity, label) => {
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportIssueSeverityTag severity={severity} />))
    const tag = container.querySelector('.import-issue-severity-tag')
    expect(tag?.textContent).toBe(label)
    expect(tag?.classList.contains(severity)).toBe(true)
  } finally {
    act(() => root.unmount())
  }
})
