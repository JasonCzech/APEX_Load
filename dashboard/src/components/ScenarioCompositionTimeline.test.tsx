// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { ScenarioCompositionTimeline } from './ScenarioCompositionTimeline'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ScenarioCompositionTimeline', () => {
  it('round-trips a dragged offset through the canonical duration field', async () => {
    const onOffsetChange = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    await act(async () => {
      root.render(<ScenarioCompositionTimeline
        duration="3m"
        groups={[
          { name: 'Immediate', script_id: 'checkout', start_offset: '', vusers: 10 },
          { name: 'Later', script_id: 'search', start_offset: '1m', vusers: 5 },
        ]}
        onOffsetChange={onOffsetChange}
      />)
    })
    const slider = container.querySelector('[aria-label="Start offset for Later"]') as HTMLInputElement
	expect(slider.value).toBe('60')
	await act(async () => {
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(slider, '120')
		slider.dispatchEvent(new Event('input', { bubbles: true }))
	})
    expect(onOffsetChange).toHaveBeenCalledWith(1, '2m')
    await act(async () => root.unmount())
    container.remove()
  })
})
