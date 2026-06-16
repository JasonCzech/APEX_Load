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

  it('hides management actions without permission', async () => {
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [slot({ approval_state: 'pending' })], count: 1 })
    await act(async () => {
      root.render(<TimeslotsPanel projectId="proj-a" canManage={false} />)
    })
    await flush()
    expect(container.textContent).not.toContain('New Reservation')
    expect(container.textContent).not.toContain('Approve')
    expect(container.textContent).not.toContain('Delete')
  })
})
