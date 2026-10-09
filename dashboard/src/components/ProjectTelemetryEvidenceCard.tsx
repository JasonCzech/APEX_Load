import { useContext, useState } from 'react'

import { request } from '../hooks/apiClient'
import { ProjectContext } from '../hooks/projectContext'
import type { Project } from '../types'

interface Props {
  project: Project
  // Editors may change the project's telemetry opt-ins.
  canEdit?: boolean
  onUpdated?: (project: Project) => void
}

function updateProjectTelemetryPolicy(projectID: string, clientRenderEvidence: boolean) {
  return request<Project>(`/projects/${encodeURIComponent(projectID)}/telemetry-policy`, {
    method: 'PUT',
    body: JSON.stringify({ client_render_evidence: clientRenderEvidence }),
  })
}

// Opt-in client render evidence (C5). When on, dashboard tabs viewing this
// project's live runs report how long each update took to render; the run
// shows it as an advisory browser render stage that never affects run health.
// The toggle saves at once through the telemetry-policy route, which changes
// nothing else in the project.
export default function ProjectTelemetryEvidenceCard({ project, canEdit = false, onUpdated }: Props) {
  const projectContext = useContext(ProjectContext)
  const enabled = Boolean(project.settings?.telemetry?.client_render_evidence)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const toggle = async (next: boolean) => {
    setBusy(true)
    setError('')
    try {
      const updated = await updateProjectTelemetryPolicy(project.id, next)
      onUpdated?.(updated)
      // An open run view reads the opt-in from the shared project list.
      void projectContext?.refreshProjects().catch(() => undefined)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Failed to update the project')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="setting-row">
      <div className="setting-info">
        <span className="setting-label">Client render evidence</span>
        <span className="setting-desc">
          Dashboard tabs viewing this project's live runs report how long each update took to render. Only visible tabs count, and the browser render stage is advisory: it never affects run health.
        </span>
        {error && <span className="setting-desc" role="alert">{error}</span>}
      </div>
      <input
        type="checkbox"
        aria-label="Client render evidence"
        checked={enabled}
        disabled={!canEdit || busy}
        onChange={event => { void toggle(event.target.checked) }}
      />
    </div>
  )
}
