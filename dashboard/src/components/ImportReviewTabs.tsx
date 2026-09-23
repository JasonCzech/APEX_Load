import { type ReactNode, useId } from 'react'
import TabList from './ux/TabList'
import './ImportReviewTabs.css'

const IMPORT_REVIEW_TABS = [
  { id: 'review', label: 'Review' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'data', label: 'Data & parameters' },
  { id: 'runtime', label: 'Runtime settings' },
  { id: 'dsl', label: 'DSL Preview' },
  { id: 'source', label: 'Source Mapping' },
] as const
export type ImportReviewTab = typeof IMPORT_REVIEW_TABS[number]['id']

export default function ImportReviewTabs({ active, onChange, counts = {}, children }: {
  active: ImportReviewTab
  onChange: (tab: ImportReviewTab) => void
  counts?: Partial<Record<ImportReviewTab, number>>
  children: ReactNode
}) {
  const id = useId()
  return <div className="import-review-layout">
    <TabList className="import-review-tabs" aria-label="Import review sections">
      {IMPORT_REVIEW_TABS.map(tab => <button type="button" key={tab.id}
        id={`${id}-${tab.id}`} role="tab" aria-label={tab.label}
        aria-selected={active === tab.id} aria-controls={`${id}-panel`}
        tabIndex={active === tab.id ? 0 : -1} onClick={() => onChange(tab.id)}>
        {tab.label}{(counts[tab.id] || 0) > 0 && <span aria-hidden="true">{counts[tab.id]}</span>}
      </button>)}
    </TabList>
    <div role="tabpanel" id={`${id}-panel`} aria-labelledby={`${id}-${active}`} className="import-review-panel">{children}</div>
  </div>
}
