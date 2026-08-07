// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TimeslotsPanel from './TimeslotsPanel'
import { zonedLocalToISO } from './timeslotTimezoneModel'
import { CapacityTimeslot } from '../types'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const capacityApiMock = {
  listTimeslots: vi.fn(),
  getTimeslot: vi.fn(),
  createTimeslot: vi.fn(),
  updateTimeslot: vi.fn(),
  deleteTimeslot: vi.fn(),
  checkTimeslotAvailability: vi.fn(),
  approveTimeslot: vi.fn(),
  rejectTimeslot: vi.fn(),
  bindTimeslot: vi.fn(),
}

vi.mock('../hooks/api/useCapacityApi', () => ({
  useCapacityApi: () => capacityApiMock,
}))

const fleetApiMock = {
  listFleetPools: vi.fn().mockResolvedValue({ fleet_pools: [{ id: 'pool-1', name: 'East Pool', status: 'active' }], count: 1 }),
}

vi.mock('../hooks/api/useFleetApi', () => ({
  useFleetApi: () => fleetApiMock,
}))

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setNativeInput(element: HTMLInputElement | null | undefined, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(element, value)
  element?.dispatchEvent(new Event('input', { bubbles: true }))
}

function slot(overrides: Partial<CapacityTimeslot>): CapacityTimeslot {
  return {
    id: 'slot-1',
    project_id: 'proj-a',
    start_at: '2026-07-01T10:00:00Z',
    end_at: '2026-07-01T12:00:00Z',
    timezone: 'UTC',
    approval_state: 'pending',
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('TimeslotsPanel', () => {
  it('interprets reservation wall-clock input in the selected timezone', () => {
    expect(zonedLocalToISO('2026-08-04T09:30', 'UTC')).toBe('2026-08-04T09:30:00.000Z')
    expect(zonedLocalToISO('2026-08-04T09:30', 'America/New_York')).toBe('2026-08-04T13:30:00.000Z')
    expect(zonedLocalToISO('2026-03-08T02:30', 'America/New_York')).toBe('')
    expect(zonedLocalToISO('2026-11-01T01:30', 'America/New_York')).toBe('2026-11-01T05:30:00.000Z')
  })
  // Pick a zone whose wall-clock differs from the runner's local zone so the
  // assertions below stay meaningful wherever the suite runs.
  function foreignZone(instant: string): string {
    const local = new Date(instant).toLocaleString()
    return ['Asia/Tokyo', 'America/Los_Angeles']
      .find(candidate => new Date(instant).toLocaleString(undefined, { timeZone: candidate }) !== local) as string
  }

  it('renders the reservation window in the slot timezone, not the browser zone', async () => {
    const startAt = '2026-07-01T13:00:00Z'
    const endAt = '2026-07-01T17:00:00Z'
    const zone = foreignZone(startAt)
    const inZone = (value: string) => new Date(value).toLocaleString(undefined, { timeZone: zone })
    capacityApiMock.listTimeslots.mockResolvedValue({
      timeslots: [slot({ timezone: zone, start_at: startAt, end_at: endAt })],
      count: 1,
    })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    const title = container.querySelector('.pp-row-title')?.textContent || ''
    expect(title).toContain(inZone(startAt))
    expect(title).toContain(inZone(endAt))
    expect(title).not.toContain(new Date(startAt).toLocaleString())
    expect(container.querySelector('.pp-row-meta')?.textContent).toContain(zone)
  })

  it('confirms deletion against the reservation window in its own timezone', async () => {
    const startAt = '2026-07-01T13:00:00Z'
    const endAt = '2026-07-01T17:00:00Z'
    const zone = foreignZone(startAt)
    const inZone = (value: string) => new Date(value).toLocaleString(undefined, { timeZone: zone })
    capacityApiMock.listTimeslots.mockResolvedValue({
      timeslots: [slot({ approval_state: 'approved', timezone: zone, start_at: startAt, end_at: endAt })],
      count: 1,
    })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Delete')?.click()
    })
    const description = container.querySelector('[role="dialog"]')?.textContent || ''
    expect(description).toContain(inZone(startAt))
    expect(description).toContain(inZone(endAt))
    expect(description).toContain(zone)
    expect(description).not.toContain(new Date(startAt).toLocaleString())
  })

  it('prompts to select a project when none is active and does not fetch', async () => {
    await act(async () => {
      root.render(<TimeslotsPanel canManage />)
    })
    await flush()
    expect(capacityApiMock.listTimeslots).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Select a project')
  })

  it('shows the empty state and New Reservation action for an active project', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledWith({ project_id: 'proj-a', limit: 100, offset: 0 })
    expect(container.textContent).toContain('No capacity reservations')
    expect(container.textContent).toContain('New Reservation')
  })

  it('renders a pending timeslot with approve/reject actions', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'pending' })], count: 1 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.querySelector('.pp-pill-warning')).toBeTruthy()
    expect(container.textContent).toContain('Approve')
    expect(container.textContent).toContain('Reject')
  })

  it('offers Bind test for an approved, unbound timeslot', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'approved' })], count: 1 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.querySelector('.pp-pill-success')).toBeTruthy()
    expect(container.textContent).toContain('Bind test')
    expect(container.textContent).not.toContain('Approve')
  })

  it('approves a timeslot and reloads', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'pending' })], count: 1 })
    capacityApiMock.approveTimeslot.mockResolvedValue(slot({ approval_state: 'approved' }))
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    const approveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Approve')
    await act(async () => {
      approveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    expect(capacityApiMock.approveTimeslot).toHaveBeenCalledWith('slot-1')
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledTimes(2)
  })

  it('retries approval with conflict override reason when requested by the server', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('approved maintenance window')
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'pending' })], count: 1 })
    capacityApiMock.approveTimeslot
      .mockRejectedValueOnce(new ApiError('overbooked; pass conflict_override with a reason to approve anyway', 409))
      .mockResolvedValueOnce(slot({ approval_state: 'approved' }))
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    const approveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Approve')
    await act(async () => {
      approveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    expect(capacityApiMock.approveTimeslot).toHaveBeenNthCalledWith(1, 'slot-1')
    expect(capacityApiMock.approveTimeslot).toHaveBeenNthCalledWith(2, 'slot-1', {
      conflict_override: true,
      reason: 'approved maintenance window',
    })
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledTimes(2)
  })

  it('scopes a created reservation to the selected fleet pool', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    capacityApiMock.createTimeslot.mockResolvedValue(slot({}))
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    const newButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'New Reservation')
    await act(async () => {
      newButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    const setInput = (el: Element | null, value: string) => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(el, value)
      el?.dispatchEvent(new Event('input', { bubbles: true }))
    }
    const dt = container.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
    await act(async () => {
      setInput(dt[0], '2026-07-01T10:00')
      setInput(dt[1], '2026-07-01T12:00')
    })
    await flush()
    const poolSelect = container.querySelector<HTMLSelectElement>('select')
    expect(poolSelect?.textContent).toContain('East Pool')
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(poolSelect, 'pool-1')
      poolSelect?.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    const reserveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Reserve')
    await act(async () => {
      reserveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flush()
    expect(capacityApiMock.createTimeslot).toHaveBeenCalledTimes(1)
    const payload = capacityApiMock.createTimeslot.mock.calls[0][0] as { requested_capacity?: { pool_id?: string } }
    expect(payload.requested_capacity?.pool_id).toBe('pool-1')
  })

  it('opens a labelled reservation dialog, focuses the start field, and restores focus on Escape', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()

    const trigger = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'New Reservation')
    trigger?.focus()
    await act(async () => {
      trigger?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushFocus()

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    const start = container.querySelector<HTMLInputElement>('input[type="datetime-local"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('New Capacity Reservation')
    expect(document.activeElement).toBe(start)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('announces asynchronous loading errors', async () => {
    capacityApiMock.listTimeslots.mockRejectedValue(new ApiError('reservation service unavailable', 503))
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('reservation service unavailable')
  })

  it('announces availability-check failures from inside the reservation dialog', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    capacityApiMock.checkTimeslotAvailability.mockRejectedValueOnce(new ApiError('capacity check unavailable', 503))
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage />)
    })
    await flush()
    const trigger = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'New Reservation')
    await act(async () => {
      trigger?.click()
    })
    const setInput = (element: HTMLInputElement | undefined, value: string) => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(element, value)
      element?.dispatchEvent(new Event('input', { bubbles: true }))
    }
    const fields = container.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
    await act(async () => {
      setInput(fields[0], '2026-07-01T10:00')
      setInput(fields[1], '2026-07-01T12:00')
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'Check availability')?.click()
    })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('capacity check unavailable')
  })

  it('disables reservation creation and hides row management actions without permission', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'pending' })], count: 1 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage={false} />)
    })
    await flush()
    const newButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(b => b.textContent === 'New Reservation')
    expect(newButton).toBeTruthy()
    expect(newButton?.disabled).toBe(true)
    expect(container.textContent).not.toContain('Approve')
    expect(container.textContent).not.toContain('Delete')
  })

  it('ignores a late timeslot response from the previous project', async () => {
    let resolveProjectA: ((value: { timeslots: CapacityTimeslot[]; count: number }) => void) | undefined
    capacityApiMock.listTimeslots.mockImplementation(({ project_id }: { project_id: string }) => {
      if (project_id === 'proj-a') return new Promise(resolve => { resolveProjectA = resolve })
      return Promise.resolve({ timeslots: [slot({ id: 'slot-b', project_id: 'proj-b', scenario_id: 'project-b-scenario' })], count: 1 })
    })
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-a" canManage />) })
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.textContent).toContain('project-b-scenario')

    await act(async () => {
      resolveProjectA?.({ timeslots: [slot({ scenario_id: 'stale-project-a-scenario' })], count: 1 })
    })
    await flush()
    expect(container.textContent).toContain('project-b-scenario')
    expect(container.textContent).not.toContain('stale-project-a-scenario')
  })

  it('closes and clears a reservation draft when the project changes', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Reservation')?.click()
    })
    const dateFields = container.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
    await act(async () => setNativeInput(dateFields[0], '2026-07-01T10:00'))

    await act(async () => { root.render(<TimeslotsPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Reservation')?.click()
    })
    expect(container.querySelector<HTMLInputElement>('input[type="datetime-local"]')?.value).toBe('')
  })

  it('keeps the last good timeslot list when a same-project refresh fails', async () => {
    capacityApiMock.listTimeslots.mockResolvedValueOnce({ timeslots: [slot({ approval_state: 'pending', scenario_id: 'retained-scenario' })], count: 1 })
    capacityApiMock.approveTimeslot.mockResolvedValue(slot({ approval_state: 'approved' }))
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-a" canManage />) })
    await flush()
    capacityApiMock.listTimeslots.mockRejectedValueOnce(new ApiError('timeslot refresh unavailable', 503))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Approve')?.click()
    })
    await flush()
    expect(container.textContent).toContain('retained-scenario')
    expect(container.textContent).toContain('timeslot refresh unavailable')
  })

  it('keeps the bind draft open when binding fails so it can be retried', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'approved' })], count: 1 })
    capacityApiMock.bindTimeslot.mockRejectedValue(new ApiError('bind failed', 409))
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Bind test')?.click()
    })
    const testInput = container.querySelector<HTMLInputElement>('input[placeholder="test id to bind"]')
    await act(async () => setNativeInput(testInput, 'test-1'))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Bind')?.click()
    })
    await flush()
    expect(container.querySelector<HTMLInputElement>('input[placeholder="test id to bind"]')?.value).toBe('test-1')
    expect(container.textContent).toContain('bind failed')
  })

  it('keeps the delete confirmation open when deletion fails', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'approved' })], count: 1 })
    capacityApiMock.deleteTimeslot.mockRejectedValue(new ApiError('delete failed', 503))
    await act(async () => { root.render(<TimeslotsPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Delete')?.click()
    })
    const modalInputs = container.querySelectorAll<HTMLInputElement>('[role="dialog"] input')
    await act(async () => {
      setNativeInput(modalInputs[0], 'cleanup')
      setNativeInput(modalInputs[1], 'slot-1')
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Delete reservation')?.click()
    })
    await flush()
    expect(container.textContent).toContain('Delete capacity reservation')
    expect(container.textContent).toContain('delete failed')
  })

	it('guards a dirty reservation draft before closing the editor', async () => {
		capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
		await act(async () => {
			root.render(
				<NavigationGuardProvider>
					<TimeslotsPanel projectId="proj-a" canManage />
				</NavigationGuardProvider>,
			)
		})
		await flush()
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'New Reservation')?.click()
		})
		const start = container.querySelector<HTMLInputElement>('input[type="datetime-local"]')
		await act(async () => setNativeInput(start, '2026-07-01T10:00'))
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('.pp-modal-actions button')).find(button => button.textContent === 'Cancel')?.click()
		})

		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(container.querySelector<HTMLInputElement>('input[type="datetime-local"]')?.value).toBe('2026-07-01T10:00')
		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(button => button.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(container.querySelector('input[type="datetime-local"]')).toBeNull()
	})
})

async function flushFocus() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}
