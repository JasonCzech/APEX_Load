import { useCallback, useMemo } from 'react'

import {
  CapacityTimeslot,
  CapacityTimeslotAvailabilityResponse,
  CapacityTimeslotListResponse,
} from '../../types'
import { request } from '../apiClient'

function buildQuery(params: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

export function useCapacityApi() {
  const listTimeslots = useCallback(
    (params?: { project_id?: string; limit?: number; offset?: number }) =>
      request<CapacityTimeslotListResponse>(`/capacity/timeslots${buildQuery({ ...params })}`),
    [],
  )

  const getTimeslot = useCallback(
    (id: string) => request<CapacityTimeslot>(`/capacity/timeslots/${id}`),
    [],
  )

  const createTimeslot = useCallback(
    (body: Partial<CapacityTimeslot>) =>
      request<CapacityTimeslot>('/capacity/timeslots', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateTimeslot = useCallback(
    (id: string, body: Partial<CapacityTimeslot>) =>
      request<CapacityTimeslot>(`/capacity/timeslots/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteTimeslot = useCallback(
    (id: string) => request<{ deleted: string }>(`/capacity/timeslots/${id}`, { method: 'DELETE' }),
    [],
  )

  const checkTimeslotAvailability = useCallback(
    (body: { project_id: string; start_at: string; end_at: string }) =>
      request<CapacityTimeslotAvailabilityResponse>('/capacity/timeslots/availability', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const approveTimeslot = useCallback(
    (id: string) => request<CapacityTimeslot>(`/capacity/timeslots/${id}/approve`, { method: 'POST' }),
    [],
  )

  const rejectTimeslot = useCallback(
    (id: string) => request<CapacityTimeslot>(`/capacity/timeslots/${id}/reject`, { method: 'POST' }),
    [],
  )

  const bindTimeslot = useCallback(
    (id: string, testId: string) =>
      request<CapacityTimeslot>(`/capacity/timeslots/${id}/bind`, {
        method: 'POST',
        body: JSON.stringify({ test_id: testId }),
      }),
    [],
  )

  return useMemo(
    () => ({
      listTimeslots,
      getTimeslot,
      createTimeslot,
      updateTimeslot,
      deleteTimeslot,
      checkTimeslotAvailability,
      approveTimeslot,
      rejectTimeslot,
      bindTimeslot,
    }),
    [listTimeslots, getTimeslot, createTimeslot, updateTimeslot, deleteTimeslot, checkTimeslotAvailability, approveTimeslot, rejectTimeslot, bindTimeslot],
  )
}
