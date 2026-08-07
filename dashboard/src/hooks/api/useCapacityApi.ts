import { useCallback, useMemo } from 'react'

import {
  CapacityTimeslot,
  CapacityTimeslotAvailabilityResponse,
  CapacityTimeslotListResponse,
} from '../../types/capacity'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

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
    (params?: { project_id?: string; limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<CapacityTimeslotListResponse>(`/capacity/timeslots${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getTimeslot = useCallback(
    (id: string, options?: ApiCallOptions) => request<CapacityTimeslot>(`/capacity/timeslots/${id}`, withCallOptions(undefined, options)),
    [],
  )

  const createTimeslot = useCallback(
    (body: Partial<CapacityTimeslot>, options?: ApiCallOptions) =>
      request<CapacityTimeslot>('/capacity/timeslots', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const updateTimeslot = useCallback(
    (id: string, body: Partial<CapacityTimeslot>, options?: ApiCallOptions) =>
      request<CapacityTimeslot>(`/capacity/timeslots/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)),
    [],
  )

  const deleteTimeslot = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/capacity/timeslots/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const checkTimeslotAvailability = useCallback(
    (body: { project_id: string; start_at: string; end_at: string }, options?: ApiCallOptions) =>
      request<CapacityTimeslotAvailabilityResponse>('/capacity/timeslots/availability', withCallOptions({
        method: 'POST',
        body: JSON.stringify(body),
      }, options)),
    [],
  )

  const approveTimeslot = useCallback(
    (id: string, body?: { conflict_override?: boolean; reason?: string }, options?: ApiCallOptions) =>
      request<CapacityTimeslot>(`/capacity/timeslots/${id}/approve`, withCallOptions({
        method: 'POST',
        ...(body ? { body: JSON.stringify(body) } : {}),
      }, options)),
    [],
  )

  const rejectTimeslot = useCallback(
    (id: string, options?: ApiCallOptions) => request<CapacityTimeslot>(`/capacity/timeslots/${id}/reject`, withCallOptions({ method: 'POST' }, options)),
    [],
  )

  const bindTimeslot = useCallback(
    (id: string, testId: string, options?: ApiCallOptions) =>
      request<CapacityTimeslot>(`/capacity/timeslots/${id}/bind`, withCallOptions({
        method: 'POST',
        body: JSON.stringify({ test_id: testId }),
      }, options)),
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
