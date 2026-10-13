import { useMemo } from 'react'

import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import type { Project } from '../types'
import DataPoolsManager from './DataPoolsManager'
import RepositorySectionTabs from './RepositorySectionTabs'

/**
 * Repository › Shared test data for roles without Settings: the project's
 * data pools, which editors create and change and viewers see and export.
 */
export default function SharedTestDataPage({ activeProject, canManage }: { activeProject?: Project | null; canManage: boolean }) {
  const contribution = useMemo(() => ({
    id: 'shared-test-data',
    title: 'Shared test data',
    controls: <RepositorySectionTabs current="test-data" />,
    priority: 10,
  }), [])
  useTopbarContribution(contribution)
  // A pool link opens that pool, as it does in Settings.
  const poolId = new URLSearchParams(window.location.hash.split('?')[1] || '').get('resourceId') || undefined

  return <div className="page settings-page shared-test-data-page">
    {activeProject
      ? <DataPoolsManager key={activeProject.id} activeProject={activeProject} projects={[activeProject]} preferredPoolId={poolId} canManage={canManage} />
      : <p>Select a project to see its shared test data.</p>}
  </div>
}
