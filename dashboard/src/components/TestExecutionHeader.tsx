import TestExecutionQueuedRunActions from './TestExecutionQueuedRunActions'
import StatusBadge from './StatusBadge'
import ReferenceKeyBadge from './ReferenceKeyBadge'

import { TestSummary } from '../types'

interface Props {
  test: TestSummary
  isMultiGroup: boolean
  onBack: () => void
  onStart: () => void
  onSelectTest: (testId: string) => void
  isStopping: boolean
  showArchiveDownload: boolean
  onDownloadArchive: () => void
}

export default function TestExecutionHeader({
  test,
  isMultiGroup,
  onBack,
  onStart,
  onSelectTest,
  isStopping,
  showArchiveDownload,
  onDownloadArchive,
}: Props) {
  return (
    <div className="exec-header">
      <button className="btn btn-secondary" onClick={onBack}>Back</button>
      <div className="exec-title-area">
        <h2>{test.name}</h2>
        <div className="exec-meta">
          <StatusBadge status={test.status} />
          <ReferenceKeyBadge value={test.ref_key} />
          <span className="meta-item">{test.total_vusers} VUsers</span>
          <span className="meta-item">{test.duration}</span>
          {isMultiGroup && (
            <span className="meta-item group-count">{test.groups.length} groups</span>
          )}
        </div>
      </div>
      <div className="exec-actions">
        <TestExecutionQueuedRunActions
          test={test}
          onStart={onStart}
          onSelectTest={onSelectTest}
        />
        {showArchiveDownload && (
          <button className="btn btn-secondary" onClick={onDownloadArchive}>
            Download Archive
          </button>
        )}
        {isStopping && (
          <span className="meta-item">Stopping…</span>
        )}
      </div>
    </div>
  )
}
