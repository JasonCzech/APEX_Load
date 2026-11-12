import { type ReactNode, useEffect, useId } from 'react'
import TabList from './ux/TabList'
import './ImportReviewTabs.css'
import './ImportReviewWorkspace.css'

const IMPORT_REVIEW_TABS = [
  { id: 'review', label: 'Review' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'data', label: 'Data & parameters' },
  { id: 'runtime', label: 'Runtime settings' },
  { id: 'dsl', label: 'DSL Preview' },
  { id: 'source', label: 'Source Mapping' },
] as const
export type ImportReviewTab = typeof IMPORT_REVIEW_TABS[number]['id']

export function ImportReviewTabList<T extends string = ImportReviewTab>({
  tabs,
  active,
  onChange,
  counts = {},
  checkingTabs,
  tabId,
  panelId,
  className = 'import-review-tabs',
  badgeClassName,
  activeClassName,
  ariaLabel = 'Import review sections',
  countSingularLabel = 'review item',
  countPluralLabel = 'review items',
}: {
  tabs?: readonly { readonly id: T; readonly label: string }[]
  active: T
  onChange: (tab: T) => void
  counts?: Partial<Record<T, number>>
  checkingTabs?: Partial<Record<T, boolean>>
  tabId: (tab: T) => string
  panelId: string
  className?: string
  badgeClassName?: string
  activeClassName?: string
  ariaLabel?: string
  countSingularLabel?: string
  countPluralLabel?: string
}) {
  const resolvedTabs = (tabs ?? IMPORT_REVIEW_TABS) as readonly { readonly id: T; readonly label: string }[]
  return <TabList className={className} aria-label={ariaLabel}>
    {resolvedTabs.map(tab => {
      const checking = Boolean(checkingTabs?.[tab.id])
      const count = checking ? 0 : (counts[tab.id] || 0)
      const isSelected = active === tab.id
      // The count is part of the name, announced once; the visible badge is decorative.
      const name = checking
        ? `${tab.label}, checking issues`
        : count > 0
          ? `${tab.label}, ${count} ${count === 1 ? countSingularLabel : countPluralLabel}`
          : tab.label
      return <button type="button" key={tab.id}
        id={tabId(tab.id)} role="tab" aria-label={name}
        aria-selected={isSelected} aria-controls={panelId}
        tabIndex={isSelected ? 0 : -1}
        className={activeClassName ? (isSelected ? activeClassName : '') : undefined}
        onClick={() => onChange(tab.id)}>
        {tab.label}{checking ? <span className={badgeClassName} aria-hidden="true" title="Checking…">…</span> : count > 0 && <span className={badgeClassName} aria-hidden="true">{count}</span>}
      </button>
    })}
  </TabList>
}

export default function ImportReviewTabs({ active, onChange, counts = {}, visibleTabs, idPrefix, children }: {
  active: ImportReviewTab
  onChange: (tab: ImportReviewTab) => void
  counts?: Partial<Record<ImportReviewTab, number>>
  visibleTabs?: readonly ImportReviewTab[]
  /** Stable ids (`<prefix>-tab-<tab>`, `<prefix>-panel`) for links that focus a tab. */
  idPrefix?: string
  children: ReactNode
}) {
  const generatedId = useId()
  const tabId = (tab: ImportReviewTab) => idPrefix ? `${idPrefix}-tab-${tab}` : `${generatedId}-${tab}`
  const panelId = idPrefix ? `${idPrefix}-panel` : `${generatedId}-panel`
  const tabs = visibleTabs ? IMPORT_REVIEW_TABS.filter(tab => visibleTabs.includes(tab.id)) : IMPORT_REVIEW_TABS
  const resolvedActive = tabs.some(tab => tab.id === active) ? active : tabs[0]?.id
  useEffect(() => {
    if (resolvedActive && resolvedActive !== active) onChange(resolvedActive)
  }, [active, onChange, resolvedActive])
  return <div className="import-review-layout">
    <ImportReviewTabList
      tabs={tabs}
      active={resolvedActive ?? active}
      onChange={onChange}
      counts={counts}
      tabId={tabId}
      panelId={panelId}
    />
    <div role="tabpanel" id={panelId} aria-labelledby={resolvedActive ? tabId(resolvedActive) : undefined} className="import-review-panel">{children}</div>
  </div>
}
