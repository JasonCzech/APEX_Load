import { useMemo, useState } from 'react'

import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import type { Application, Project } from '../types'
import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import DataPoolsManager from './DataPoolsManager'
import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import ProjectLiveTablesPanel from './ProjectLiveTablesPanel'
import ProjectSecretsPanel from './ProjectSecretsPanel'
import SavedBlocksPanel from './SavedBlocksPanel'
import { DEFAULT_SHARED_ASSETS_SECTION, SHARED_ASSET_SECTIONS, sharedAssetsHref, type SharedAssetsSection } from './sharedAssetsNavigation'
import { useSectionListKeys } from './ux/useSectionListKeys'
import './SharedAssetsPage.css'

/** What the viewer may change, by the API's own operations. */
export interface SharedAssetsPermissions {
  /** Pools and live tables: editors and up. */
  manageDataPools: boolean
  /** Secrets to add, rule sets, object repositories and saved blocks: editors and up. */
  manageScripts: boolean
  /** Rotating secrets: project admins and up. */
  manageProjects: boolean
}

/**
 * Shared assets: what a project's scripts and scenarios share, one section
 * per kind, each editable as the viewer's role allows.
 */
export default function SharedAssetsPage({ activeProject, applications = [], section, resourceId, permissions }: {
  activeProject?: Project | null
  /** The project's applications, whose secrets override the project's. */
  applications?: Application[]
  section?: SharedAssetsSection
  /** A row the address names: a pool, a live table or an application. */
  resourceId?: string
  permissions: SharedAssetsPermissions
}) {
  const current = section ?? DEFAULT_SHARED_ASSETS_SECTION
  const entry = SHARED_ASSET_SECTIONS.find(item => item.id === current) ?? SHARED_ASSET_SECTIONS[0]
  const navRef = useSectionListKeys()
  const contribution = useMemo(() => ({
    id: 'shared-assets',
    title: 'Shared assets',
    controls: <span className="ux-save-status">Applies to: Project: {activeProject?.name || 'Select a project'}</span>,
    priority: 10,
  }), [activeProject?.name])
  useTopbarContribution(contribution)

  return <div className="page shared-assets-page">
    <div className="settings-tabbed-layout">
      {/* The same always-visible section list as Settings, Access and Help. */}
      <nav ref={navRef} className="section-nav shared-assets-navigation" aria-label="Shared assets">
        <div>
          <h3>Shared by scripts and scenarios</h3>
          {SHARED_ASSET_SECTIONS.map(item => <a key={item.id} href={sharedAssetsHref(item.id)} aria-current={item.id === current ? 'page' : undefined}>{item.title}</a>)}
        </div>
      </nav>
      <section className="shared-assets-section" aria-labelledby="shared-assets-title" aria-describedby={entry.usage ? 'shared-assets-usage' : undefined}>
        {/* As in Settings, the section list names the section; the heading is for screen readers and focus. */}
        <header className="settings-task-heading">
          <h2 id="shared-assets-title" tabIndex={-1}>{entry.title}</h2>
        </header>
        {entry.usage && <p id="shared-assets-usage" className="shared-assets-usage">{entry.usage}</p>}
        {!activeProject
          ? <p>Select a project to see what its scripts and scenarios share.</p>
          : <SharedAssetSection key={`${activeProject.id}:${current}:${resourceId || ''}`} section={current} project={activeProject} applications={applications} resourceId={resourceId} permissions={permissions} />}
      </section>
    </div>
  </div>
}

/** One section's panel, on the page or in a dialog over the page that asked for it. */
export function SharedAssetSection({ section, project, applications, resourceId, permissions }: { section: SharedAssetsSection; project: Project; applications: Application[]; resourceId?: string; permissions: SharedAssetsPermissions }) {
  switch (section) {
    case 'test-data':
      return <DataPoolsManager activeProject={project} projects={[project]} preferredPoolId={resourceId} canManage={permissions.manageDataPools} />
    case 'live-tables':
      return <ProjectLiveTablesPanel projectId={project.id} projectName={project.name} canCreate={permissions.manageDataPools} selectedId={resourceId} />
    case 'secrets':
      return <SecretsSection project={project} applications={applications} applicationId={resourceId} canCreate={permissions.manageScripts} canRotate={permissions.manageProjects} />
    case 'correlation-rules':
      return <CorrelationRulesetPanel projectId={project.id} canManage={permissions.manageScripts} />
    case 'object-repositories':
      return <ObjectRepositoryPanel projectId={project.id} canManage={permissions.manageScripts} />
    case 'saved-blocks':
      return <SavedBlocksPanel projectId={project.id} canManage={permissions.manageScripts} />
  }
}

/**
 * The project's secrets, or an application's (which override the project's
 * of the same name). Editors add secrets; project admins also rotate them.
 */
function SecretsSection({ project, applications, applicationId, canCreate, canRotate }: { project: Project; applications: Application[]; applicationId?: string; canCreate: boolean; canRotate: boolean }) {
  const scopes = applications.filter(application => !application.project_id || application.project_id === project.id)
  // The application a link named, once the project's applications have
  // loaded (they may arrive after this mounts); the viewer's choice after that.
  const [chosenScope, setChosenScope] = useState<string>()
  const scope = chosenScope ?? (scopes.some(application => application.id === applicationId) ? applicationId! : '')
  const application = scopes.find(candidate => candidate.id === scope)
  return <>
    {scopes.length > 0 && <label className="shared-assets-scope">
      <span>Secrets of</span>
      <select value={scope} onChange={event => setChosenScope(event.target.value)}>
        <option value="">{`Project ${project.name}`}</option>
        {scopes.map(candidate => <option key={candidate.id} value={candidate.id}>{`Application ${candidate.name}`}</option>)}
      </select>
    </label>}
    {/* The panel says what this role can and cannot do. */}
    <ProjectSecretsPanel key={scope || project.id} projectId={project.id} projectName={project.name} applicationId={application?.id} applicationName={application?.name} canCreate={canCreate} canRotate={canRotate} />
  </>
}
