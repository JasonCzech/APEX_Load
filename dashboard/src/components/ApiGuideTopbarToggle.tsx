import { useCallback, useMemo } from 'react'

import {
  API_GUIDE_TOGGLE_OPTIONS,
  apiGuideHashForToggle,
  isApiGuideContentPageID,
  type ApiGuideContentPageID,
  type ApiGuideToggleID,
} from '../apiGuideNavigation'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import PageViewToggle from './PageViewToggle'

type ApiGuideTopbarToggleProps = {
  active: ApiGuideToggleID
  onContentPageChange?: (page: ApiGuideContentPageID) => void
}

export function ApiGuideTopbarToggle({ active, onContentPageChange }: ApiGuideTopbarToggleProps) {
  const handleChange = useCallback((value: ApiGuideToggleID) => {
    if (isApiGuideContentPageID(value)) {
      onContentPageChange?.(value)
    }
    if (typeof window !== 'undefined') {
      window.location.hash = apiGuideHashForToggle(value)
    }
  }, [onContentPageChange])

  return (
    <div className="api-guide-section-navigation">
      <PageViewToggle
        value={active}
        ariaLabel="API guide sections"
        className="api-guide-section-toggle new-test-view-toggle topbar-page-section-toggle"
        options={API_GUIDE_TOGGLE_OPTIONS}
        onChange={handleChange}
      />
      <label className="api-guide-section-select-label">
        <span>API guide section</span>
        <select
          className="api-guide-section-select"
          value={active}
          aria-label="API guide section"
          onChange={event => handleChange(event.target.value as ApiGuideToggleID)}
        >
          {API_GUIDE_TOGGLE_OPTIONS.map(option => (
            <option key={option.value} value={option.value}>{option.label}</option>
          ))}
        </select>
      </label>
    </div>
  )
}

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
