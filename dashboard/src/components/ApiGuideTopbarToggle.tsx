import { apiGuideHashForToggle, apiGuideToggleOptions, helpAreaForPage, type ApiGuideContentPageID, type ApiGuideToggleID } from '../apiGuideNavigation'

type Props = { active: ApiGuideToggleID; onContentPageChange?: (page: ApiGuideContentPageID) => void; includeAdminGuide?: boolean }
export function ApiGuideTopbarToggle({ active, onContentPageChange, includeAdminGuide = false }: Props) {
  return <nav className="help-primary-nav" aria-label="Help areas">
    {apiGuideToggleOptions({ includeAdminGuide }).map(option => <a
      key={option.value} href={apiGuideHashForToggle(option.value)}
      aria-current={helpAreaForPage(active) === option.value ? 'page' : undefined}
      onClick={() => onContentPageChange?.(option.value)}
    >{option.label}</a>)}
  </nav>
}
