// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

import type { Project } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const request = vi.fn()
vi.mock('../hooks/apiClient', () => ({ request }))

const { default: ProjectTelemetryEvidenceCard } = await import('./ProjectTelemetryEvidenceCard')

const project = {
  id: 'proj a',
  name: 'Project A',
  settings: { max_vusers: 50, features: { media_stream: true } },
} as unknown as Project

describe('ProjectTelemetryEvidenceCard', () => {
  it('saves only the opt-in through the telemetry-policy route', async () => {
    const updated = { ...project, settings: { ...project.settings, telemetry: { client_render_evidence: true } } } as Project
    request.mockResolvedValue(updated)
    const onUpdated = vi.fn()
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<ProjectTelemetryEvidenceCard project={project} canEdit onUpdated={onUpdated} />))
    const checkbox = container.querySelector<HTMLInputElement>('input[type="checkbox"]')!
    expect(checkbox.checked).toBe(false)
    await act(async () => {
      checkbox.click()
    })
    expect(request).toHaveBeenCalledWith('/projects/proj%20a/telemetry-policy', { method: 'PUT', body: JSON.stringify({ client_render_evidence: true }) })
    expect(onUpdated).toHaveBeenCalledWith(updated)
    act(() => root.unmount())
  })

  it('shows a failed save and is read-only without edit access', async () => {
    request.mockRejectedValue(new Error('forbidden'))
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<ProjectTelemetryEvidenceCard project={project} canEdit />))
    await act(async () => {
      container.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click()
    })
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('forbidden')
    act(() => root.render(<ProjectTelemetryEvidenceCard project={project} />))
    expect(container.querySelector<HTMLInputElement>('input[type="checkbox"]')!.disabled).toBe(true)
    act(() => root.unmount())
  })
})
