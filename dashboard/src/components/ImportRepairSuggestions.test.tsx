// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

import ImportRepairSuggestions from './ImportRepairSuggestions'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('offers a button for actions the host opens and shows any other kind as guidance', () => {
  const run = vi.fn()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportRepairSuggestions
      suggestions={[
        { id: 'known', category: 'jmeter', severity: 'blocker', title: 'Normalize OAuth options', description: 'The option names are non-standard.', action: 'normalize_oauth_options' },
        { id: 'unknown', category: 'future', severity: 'warning', title: 'Split the payload', description: 'A later converter kind.', action: 'split_payload_into_messages' },
        { id: 'empty', category: 'x', severity: 'info', title: '', description: '', action: '' },
      ]}
      actionFor={suggestion => suggestion.action === 'normalize_oauth_options' ? { label: 'Open this repair', run } : undefined}
    />))
    expect(container.textContent).toContain('Suggested repairs (2)')
    expect(container.textContent).toContain('Suggested action: Split payload into messages')
    const buttons = container.querySelectorAll('button')
    expect(buttons).toHaveLength(1)
    act(() => buttons[0].click())
    expect(run).toHaveBeenCalled()
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})

it('shows the API resolution text and marks a gate nothing clears yet without a button', () => {
  const run = vi.fn()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    act(() => root.render(<ImportRepairSuggestions
      suggestions={[
        { id: 'jmeter:jmeter_eventhubs_credentials_required:g1', code: 'jmeter_eventhubs_credentials_required', category: 'review_gate', severity: 'blocker', title: 'Bind the Event Hubs connection string', description: 'The sampler authenticates with a shared access key.', action: 'Create a project secret, then apply the repair.', target_action: 'Publish', clearable: true, batch_operation: 'bind_eventhubs_credentials', source_group_id: 'g1' },
        { id: 'jmeter:jmeter_messaging_compatibility_unverified', code: 'jmeter_messaging_compatibility_unverified', category: 'review_gate', severity: 'blocker', title: 'JMeter messaging replay is not available yet', description: 'Not verified against a live broker.', action: 'Nothing in the plan or the import can clear this gate in this release.', clearable: false },
      ]}
      actionFor={() => ({ label: 'Fix it', run })}
      scopeFor={suggestion => suggestion.source_group_id ? 'Hub east' : undefined}
    />))
    const items = container.querySelectorAll('li')
    expect(items[0].textContent).toContain('To clear it: Create a project secret, then apply the repair.')
    expect(items[0].textContent).toContain('Hub east · first held step: Publish')
    expect(items[0].querySelector('button')?.textContent).toBe('Fix it')
    expect(items[1].textContent).toContain('Cannot be cleared yet. Nothing in the plan or the import can clear this gate in this release.')
    expect(items[1].querySelector('button')).toBeNull()
  } finally {
    act(() => root.unmount())
    container.remove()
  }
})
