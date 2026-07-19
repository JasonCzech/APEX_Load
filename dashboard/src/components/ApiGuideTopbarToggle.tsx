import { useCallback, useMemo } from 'react'

import {
  apiGuideHashForToggle,
  apiGuideToggleOptions,
  isApiGuideContentPageID,
  type ApiGuideContentPageID,
  type ApiGuideToggleID,
} from '../apiGuideNavigation'
import PageViewToggle from './PageViewToggle'

type ApiGuideTopbarToggleProps = {
  active: ApiGuideToggleID
  onContentPageChange?: (page: ApiGuideContentPageID) => void
  includeAdminGuide?: boolean
}

export function ApiGuideTopbarToggle({ active, onContentPageChange, includeAdminGuide = false }: ApiGuideTopbarToggleProps) {
  const options = useMemo(() => apiGuideToggleOptions({ includeAdminGuide }), [includeAdminGuide])
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
        options={options}
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
          {options.map(option => (
            <option key={option.value} value={option.value}>{option.label}</option>
          ))}
        </select>
      </label>
    </div>
  )
}
