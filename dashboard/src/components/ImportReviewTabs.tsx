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
    <TabList className="import-review-tabs" aria-label="Import review sections">
      {tabs.map(tab => {
        const count = counts[tab.id] || 0
        // The count is part of the name, announced once; the visible badge is decorative.
        const name = count > 0 ? `${tab.label}, ${count} review item${count === 1 ? '' : 's'}` : tab.label
        return <button type="button" key={tab.id}
          id={tabId(tab.id)} role="tab" aria-label={name}
          aria-selected={resolvedActive === tab.id} aria-controls={panelId}
          tabIndex={resolvedActive === tab.id ? 0 : -1} onClick={() => onChange(tab.id)}>
          {tab.label}{count > 0 && <span aria-hidden="true">{count}</span>}
        </button>
      })}
    </TabList>
    <div role="tabpanel" id={panelId} aria-labelledby={resolvedActive ? tabId(resolvedActive) : undefined} className="import-review-panel">{children}</div>
  </div>
}
