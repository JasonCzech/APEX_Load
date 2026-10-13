import { useEffect, useMemo, useState } from 'react'

import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import type { Project } from '../types'
import ProjectSecretsPanel from './ProjectSecretsPanel'
import { readStoredString } from '../utils/browserStorage'
import { consumeProjectSecretsSettingsProjectId, PROJECT_SECRETS_SETTINGS_PROJECT_KEY } from './projectSecretsNavigation'

/**
 * Settings › Project secrets for roles without Settings: editors add secrets
 * (as the API allows them), project admins also rotate them, and viewers see
 * the inventory. Values are never shown.
 */
export default function ProjectSecretsPage({ activeProject, canCreate, canRotate }: { activeProject?: Project | null; canCreate: boolean; canRotate: boolean }) {
  const contribution = useMemo(() => ({ id: 'project-secrets', title: 'Project secrets', priority: 10 }), [])
  useTopbarContribution(contribution)
  // A Configure secrets link names its project; never configure another one in its place.
  // Read while rendering and cleared once mounted: StrictMode renders twice.
  const [requestedProjectId] = useState(() => readStoredString(PROJECT_SECRETS_SETTINGS_PROJECT_KEY).trim())
  useEffect(() => { consumeProjectSecretsSettingsProjectId() }, [])
  const elsewhere = Boolean(requestedProjectId && activeProject && requestedProjectId !== activeProject.id)

  return <div className="page settings-page project-secrets-page">
    {!activeProject ? <p>Select a project to see its secrets.</p>
      : elsewhere ? <p className="settings-notice" role="note">The secrets you were sent to configure belong to another project. Switch the workspace to that project to configure them.</p>
        // The panel says what this role can and cannot do.
        : <ProjectSecretsPanel projectId={activeProject.id} projectName={activeProject.name} canCreate={canCreate} canRotate={canRotate} />}
  </div>
}
