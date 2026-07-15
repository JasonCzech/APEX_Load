import { useMemo } from 'react'

import type { ApiGuideContentPageID, ApiGuideToggleID } from '../apiGuideNavigation'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { ApiGuideTopbarToggle } from './ApiGuideTopbarToggle'

export function useApiGuideTopbarContribution(
  active: ApiGuideToggleID,
  title: string,
  onContentPageChange?: (page: ApiGuideContentPageID) => void,
) {
  const controls = useMemo(() => (
    <ApiGuideTopbarToggle active={active} onContentPageChange={onContentPageChange} />
  ), [active, onContentPageChange])

  const topbarContribution = useMemo(() => ({
    id: 'api-guide-navigation',
    title,
    controls,
    priority: 10,
  }), [controls, title])

  useTopbarContribution(topbarContribution)
  return controls
}
