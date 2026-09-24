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

export default function ImportReviewTabs({ active, onChange, counts = {}, visibleTabs, children }: {
  active: ImportReviewTab
  onChange: (tab: ImportReviewTab) => void
  counts?: Partial<Record<ImportReviewTab, number>>
  visibleTabs?: readonly ImportReviewTab[]
  children: ReactNode
}) {
  const id = useId()
  const tabs = visibleTabs ? IMPORT_REVIEW_TABS.filter(tab => visibleTabs.includes(tab.id)) : IMPORT_REVIEW_TABS
  const resolvedActive = tabs.some(tab => tab.id === active) ? active : tabs[0]?.id
  useEffect(() => {
    if (resolvedActive && resolvedActive !== active) onChange(resolvedActive)
  }, [active, onChange, resolvedActive])
  return <div className="import-review-layout">
    <TabList className="import-review-tabs" aria-label="Import review sections">
      {tabs.map(tab => <button type="button" key={tab.id}
        id={`${id}-${tab.id}`} role="tab" aria-label={tab.label}
        aria-describedby={(counts[tab.id] || 0) > 0 ? `${id}-${tab.id}-count` : undefined}
        aria-selected={resolvedActive === tab.id} aria-controls={`${id}-panel`}
        tabIndex={resolvedActive === tab.id ? 0 : -1} onClick={() => onChange(tab.id)}>
        {tab.label}{(counts[tab.id] || 0) > 0 && <><span aria-hidden="true">{counts[tab.id]}</span><small id={`${id}-${tab.id}-count`} className="import-review-count-description">{counts[tab.id]} review item{counts[tab.id] === 1 ? '' : 's'}</small></>}
      </button>)}
    </TabList>
    <div role="tabpanel" id={`${id}-panel`} aria-labelledby={resolvedActive ? `${id}-${resolvedActive}` : undefined} className="import-review-panel">{children}</div>
  </div>
}
