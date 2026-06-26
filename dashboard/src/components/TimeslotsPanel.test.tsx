// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TimeslotsPanel from './TimeslotsPanel'
import { CapacityTimeslot } from '../types'

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
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledWith({ project_id: 'proj-a' })
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
})
