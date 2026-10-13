import { useContext, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { ApplicationContext } from '../hooks/applicationContext'
import { useModalDialog } from '../hooks/useModalDialog'
import { NavigationGuardRegisterContext, useNavigationGuardRunner, type NavigationGuardRegistrar } from '../hooks/NavigationGuardProvider'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { ProjectContext } from '../hooks/projectContext'
import type { Project } from '../types'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { dialogBackdropProps } from './dialogBackdropProps'
import { SharedAssetSection, type SharedAssetsPermissions } from './SharedAssetsPage'
import { SHARED_ASSET_SECTIONS, sharedAssetsHref, type SharedAssetsSection } from './sharedAssetsNavigation'
import DialogLayer from './ux/DialogLayer'
import './SharedAssetsDialog.css'

export interface SharedAssetsDialogProps {
  projectId: string
  section: SharedAssetsSection
  /** The row to open: a pool, a live table or an application. */
  resourceId?: string
  /** Called once the dialog closes; the page that opened it refreshes what it shows. */
  onClose: () => void
}

/**
 * One Shared assets section over the page that asked for it, so an author
 * changes what a script or scenario uses and is back where they were.
 * Closing asks about the section's unsaved work only, not the page's; leaving
 * the page (Back, a link) asks about both, as the app's navigation sees the
 * section's work too.
 */
export default function SharedAssetsDialog(props: SharedAssetsDialogProps) {
  const pageRegister = useContext(NavigationGuardRegisterContext)
  return <NavigationGuardProvider><SharedAssetsDialogBody {...props} pageRegister={pageRegister} /></NavigationGuardProvider>
}

function SharedAssetsDialogBody({ projectId, section, resourceId, onClose, pageRegister }: SharedAssetsDialogProps & { pageRegister: NavigationGuardRegistrar }) {
  const runner = useNavigationGuardRunner()
  const dialogRegister = useContext(NavigationGuardRegisterContext)
  // Each panel's unsaved work is the dialog's, for closing, and the page's, for leaving.
  const register = useMemo<NavigationGuardRegistrar>(() => (guard, dirty) => {
    const releaseDialog = dialogRegister(guard, dirty)
    const releasePage = pageRegister(guard, dirty)
    return () => { releaseDialog(); releasePage() }
  }, [dialogRegister, pageRegister])
  const close = () => runner.runGuarded(onClose)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose: close })
  const titleId = useId()
  const entry = SHARED_ASSET_SECTIONS.find(item => item.id === section) ?? SHARED_ASSET_SECTIONS[0]
  const projectContext = useContext(ProjectContext)
  const applications = useContext(ApplicationContext)?.applications
  const project = useMemo<Project>(() => (
    projectContext?.projects.find(candidate => candidate.id === projectId)
    ?? (projectContext?.activeProject?.id === projectId ? projectContext.activeProject : null)
    ?? { id: projectId, name: projectId } as Project
  ), [projectContext?.activeProject, projectContext?.projects, projectId])
  const permissions = useSharedAssetsPermissions(project)
  const href = sharedAssetsHref(section, resourceId, `?${new URLSearchParams({ projectId })}`)

  return <DialogLayer><div className="shared-assets-dialog-backdrop" {...dialogBackdropProps(close)}>
    <div ref={dialogRef} className="shared-assets-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
      <header className="shared-assets-dialog-header">
        <div>
          <h2 id={titleId}>{`Shared assets · ${entry.title}`}</h2>
          <p>{`Project ${project.name}. Changes apply to every script and scenario that uses them.`}</p>
        </div>
        <button type="button" className="btn btn-ghost" aria-label="Close" onClick={close}>×</button>
      </header>
      <div className="shared-assets-dialog-body">
        {entry.usage && <p className="shared-assets-usage">{entry.usage}</p>}
        {permissions
          ? <NavigationGuardRegisterContext.Provider value={register}><SharedAssetSection section={section} project={project} applications={applications || []} resourceId={resourceId} permissions={permissions} /></NavigationGuardRegisterContext.Provider>
          // Until access is known, do not tell an editor they cannot change anything.
          : <p role="status">Checking access…</p>}
      </div>
      <footer className="shared-assets-dialog-footer">
        {/* An ordinary link: leaving asks about the dialog's unsaved work and the page's. */}
        <a href={href}>Open in Shared assets</a>
        <button type="button" className="btn btn-primary" onClick={close}>Done</button>
      </footer>
    </div>
  </div></DialogLayer>
}

/**
 * What the viewer may change in the project, by the API's own operations, as
 * the Shared assets page decides it; undefined until known. A failed check
 * leaves everything read-only.
 */
function useSharedAssetsPermissions(project: Project): SharedAssetsPermissions | undefined {
  const api = useApi()
  const [loaded, setLoaded] = useState<{ projectId: string; permissions: SharedAssetsPermissions }>()
  useEffect(() => {
    let cancelled = false
    const none: SharedAssetsPermissions = { manageDataPools: false, manageScripts: false, manageProjects: false }
    api.getAuthEffectiveAccess(project.id, { domain_id: project.domain_id })
      .then(response => {
        const resources = response.resources || []
        const allowed = (operation: string) => isEffectiveOperationAllowed(resources, operation, false)
        if (!cancelled) setLoaded({ projectId: project.id, permissions: { manageDataPools: allowed('manage_data_pools'), manageScripts: allowed('manage_scripts'), manageProjects: allowed('manage_projects') } })
      })
      .catch(() => { if (!cancelled) setLoaded({ projectId: project.id, permissions: none }) })
    return () => { cancelled = true }
  }, [api, project.domain_id, project.id])
  return loaded?.projectId === project.id ? loaded.permissions : undefined
}
