// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ThresholdAlertStrip from './ThresholdAlertStrip'
import type { ThresholdAlertEntry } from '../thresholdAlerts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function alertEntry(overrides: Partial<ThresholdAlertEntry> = {}): ThresholdAlertEntry {
  return {
    key: 'test-1:p99_ms:',
    testId: 'test-1',
    testName: 'Checkout',
    metric: 'p99_ms',
    value: 1830,
    threshold: 1500,
    firstSeenAt: '2026-07-30T12:00:00Z',
    lastSeenAt: '2026-07-30T12:00:00Z',
    acknowledged: false,
    active: true,
    ...overrides,
  }
}

describe('ThresholdAlertStrip', () => {
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
  })

  it('renders nothing when no alerts are provided', async () => {
    await act(async () => {
      root.render(<ThresholdAlertStrip alerts={[]} />)
    })
    expect(container.querySelector('.threshold-alert-strip')).toBeNull()
  })

  it('does not keep resolved alerts in the live run strip', async () => {
    await act(async () => {
      root.render(<ThresholdAlertStrip alerts={[alertEntry({ active: false })]} />)
    })
    expect(container.querySelector('.threshold-alert-strip')).toBeNull()
  })

  it('renders alert rows with metric labels, stop severity, and overflow count', async () => {
    const alerts = [
      alertEntry({ key: 'test-1:error_pct:', metric: 'error_pct', value: 7.5, threshold: 5, action: 'stop_requested' }),
      alertEntry(),
      alertEntry({ key: 'test-1:avg_transfer_ms:cart', metric: 'avg_transfer_ms', transaction: 'cart' }),
      alertEntry({ key: 'test-1:avg_server_processing_ms:checkout', metric: 'avg_server_processing_ms', transaction: 'checkout' }),
    ]
    await act(async () => {
      root.render(<ThresholdAlertStrip alerts={alerts} />)
    })
    const rows = container.querySelectorAll('.threshold-alert-strip-row')
    expect(rows).toHaveLength(3)
    expect(rows[0].className).toContain('danger')
    expect(rows[0].textContent).toContain('auto-stop requested')
    expect(container.querySelector('.threshold-alert-strip-more')?.textContent).toBe('+1 more threshold alert')
  })

  it('acknowledges an alert and renders acknowledged rows subdued without an Ack button', async () => {
    const onAcknowledge = vi.fn()
    await act(async () => {
      root.render(<ThresholdAlertStrip alerts={[alertEntry()]} onAcknowledge={onAcknowledge} />)
    })
    const ackButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Ack')
    expect(ackButton).toBeTruthy()
    await act(async () => {
      ackButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onAcknowledge).toHaveBeenCalledWith('test-1:p99_ms:')

    await act(async () => {
      root.render(<ThresholdAlertStrip alerts={[alertEntry({ acknowledged: true })]} onAcknowledge={onAcknowledge} />)
    })
    expect(container.querySelector('.threshold-alert-strip-row')?.className).toContain('acknowledged')
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Ack')).toBeUndefined()
  })
})
