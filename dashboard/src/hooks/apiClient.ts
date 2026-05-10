import { useCallback, useMemo } from 'react'

export const BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  code?: string
  payload?: any

  constructor(message: string, status: number, payload?: any) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = payload?.code
    this.payload = payload
  }
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options?.headers },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(body.error || res.statusText, res.status, body)
  }
  return res.json()
}
