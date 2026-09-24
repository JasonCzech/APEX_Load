import { ImportConversionDetails } from './ImportConversionDetails'
import ImportReviewTabs, { type ImportReviewTab } from './ImportReviewTabs'
import ImportWorkflowGuide from './ImportWorkflowGuide'
import ImportIssueWorkspace from './ImportIssueWorkspace'
import { groupImportWorkspaceIssues, type ImportWorkspaceIssue } from './importIssueWorkspaceModel'
import { useEffect, useRef, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type OracleEBusinessReviewResponse } from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'
import { type IssueDetailSeverity } from './IssueDetailsDialog'
import './SampleScriptImport.css'
import './ScriptImportReview.css'

interface Props {
  projectId?: string
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
}

const packageOptions = [
  { value: '', label: 'Auto-detect package' },
  { value: 'oracle_web', label: 'Oracle E-Business Web' },
  { value: 'siebel_web', label: 'Siebel Web' },
  { value: 'oracle_nca', label: 'Oracle NCA / Forms' },
  { value: 'web_http_html', label: 'Generic Web HTTP/HTML' },
]

export default function OracleEBusinessReviewImport({ projectId, onClose, onOperationBusyChange }: Props) {
  const api = useApi()
  const reviewTask = useAsyncTask<OracleEBusinessReviewResponse>()
  const [name, setName] = useState('')
  const [packageName, setPackageName] = useState('')
  const [sourceText, setSourceText] = useState('')
  const [files, setFiles] = useState<Array<{ name: string; content: string }>>([])
  const [supportNotes, setSupportNotes] = useState<string[]>([])
  const [messageError, setMessageError] = useState('')
  const [readingFiles, setReadingFiles] = useState(false)
  const fileReadGeneration = useRef(0)
  const operationBusy = reviewTask.pending || readingFiles
  const resetReview = reviewTask.reset

  const invalidateReview = () => {
    resetReview()
    setMessageError('')
  }

  useEffect(() => {
    resetReview()
    setFiles([])
    setSourceText('')
    setName('')
    setPackageName('')
    setMessageError('')
    setReadingFiles(false)
    return () => { fileReadGeneration.current += 1 }
  }, [projectId, resetReview])

  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])

  useEffect(() => {
    let cancelled = false
    api.getOracleEBusinessSupportMatrix()
      .then(response => { if (!cancelled) setSupportNotes(response.notes || []) })
      .catch(() => { if (!cancelled) setSupportNotes([]) })
    return () => { cancelled = true }
  }, [api])

  const handleFiles = async (nextFiles: FileList | null) => {
    if (!nextFiles?.length || operationBusy) return
    const generation = ++fileReadGeneration.current
    invalidateReview()
    setReadingFiles(true)
    try {
      const loaded = await Promise.all(Array.from(nextFiles).map(async file => ({ name: file.name, content: await file.text() })))
      if (generation !== fileReadGeneration.current) return
      setFiles(loaded)
    } catch (error: unknown) {
      if (generation !== fileReadGeneration.current) return
      setMessageError(getErrorMessage(error, 'Unable to read review source file.'))
    } finally {
      if (generation === fileReadGeneration.current) setReadingFiles(false)
    }
  }

  const handleReview = async () => {
    if (operationBusy) return
    if (!sourceText.trim() && files.length === 0) {
      setMessageError('Provide source text or upload at least one review file.')
      return
    }
    setMessageError('')
    setActiveTab('review')
    try {
      await reviewTask.run(async () => api.reviewOracleEBusiness({
        project_id: projectId,
        name: name || undefined,
        package: packageName || undefined,
        source_text: sourceText || undefined,
        files,
      }), { clearValue: true })
    } catch {
      // useAsyncTask owns the error and discards stale project responses.
    }
  }

  const [activeTab, setActiveTab] = useState<ImportReviewTab>('review')
  const review = reviewTask.value
  const reviewIssues: ImportWorkspaceIssue[] = (review?.review_sections || []).flatMap(section => {
    const severity = ['blocker', 'error', 'warning', 'info'].includes(section.severity || '')
      ? section.severity as IssueDetailSeverity
      : 'info'
    const items = section.items?.length ? section.items : [section.summary || 'Review this source finding.']
    return items.map((item, index) => ({
      id: `${section.key}-${index}`,
      severity,
      title: section.title,
      description: item,
      source: section.summary && section.summary !== item ? section.summary : undefined,
      targetTab: /correlation|parameter|data/i.test(`${section.title} ${item}`) ? 'data' : /runtime|execution|runner/i.test(`${section.title} ${item}`) ? 'runtime' : 'source',
      actionLabel: /correlation|parameter|data/i.test(`${section.title} ${item}`) ? 'Review correlation and data guidance' : /runtime|execution|runner/i.test(`${section.title} ${item}`) ? 'Review execution posture' : 'Inspect source guidance',
    }))
  })
  const groupedReviewIssueCount = groupImportWorkspaceIssues(reviewIssues).length

  return (
    <div className="sample-import">
      <div className="sample-import-header">
        <div>
          <h2>Oracle E-Business / Siebel Review</h2>
          <p className="sample-import-subtitle">Review packaged enterprise app sources for package signals, correlation candidates, execution posture, and evidence requirements.</p>
        </div>
        {onClose && <button className="btn btn-secondary" type="button" disabled={operationBusy} onClick={onClose}>Back</button>}
      </div>

      <ImportWorkflowGuide stage={review ? 'review' : 'source'} reviewOnly />

      <details className="import-source-setup" open={!review}>
      <summary>Source & review options — changing these requires a new review</summary>
      <fieldset className="sample-import-card import-operation-gate" disabled={operationBusy} aria-label="Review source" aria-busy={operationBusy}>
        <div className="sample-import-grid two-col">
          <label className="sample-import-field">
            <span>Review Name</span>
            <input value={name} onChange={event => { invalidateReview(); setName(event.target.value) }} placeholder="Oracle checkout review" />
          </label>
          <label className="sample-import-field">
            <span>Package</span>
            <select value={packageName} onChange={event => { invalidateReview(); setPackageName(event.target.value) }}>
              {packageOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
          <label className="sample-import-field sample-import-span-2">
            <span>Source Text</span>
            <textarea value={sourceText} onChange={event => { invalidateReview(); setSourceText(event.target.value) }} rows={12} spellCheck={false} placeholder="Paste HAR excerpts, source snippets, exported steps, URLs, or notes for review." />
          </label>
          <label className="sample-import-field sample-import-span-2">
            <span>Source Files</span>
            <input
              type="file"
              multiple
              accept=".txt,.log,.json,.har,.js,.xml,.html,.htm,text/plain,application/json,text/xml,application/xml"
              onChange={event => {
                void handleFiles(event.currentTarget.files)
                event.currentTarget.value = ''
              }}
            />
            <small>{files.length ? files.map(file => file.name).join(', ') : 'Optional supporting files for review'}</small>
          </label>
          {files.length > 0 && <button className="btn btn-secondary" type="button" onClick={() => { invalidateReview(); setFiles([]) }}>Remove source files</button>}
        </div>
      </fieldset>
      </details>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" type="button" onClick={handleReview} disabled={operationBusy || (!sourceText.trim() && files.length === 0)}>
          {reviewTask.pending ? <><span className="spinner" /> Reviewing...</> : 'Run Review'}
        </button>
      </div>

      {readingFiles && <p role="status">Reading source files…</p>}

      {(messageError || reviewTask.error) && <div className="sample-import-error" role="alert">{messageError || reviewTask.error}</div>}

      {supportNotes.length > 0 && !review && (
        <div className="sample-import-card">
          <div className="sample-import-section-header">
            <div>
              <h3>Review Notes</h3>
              <p>{supportNotes.join(' ')}</p>
            </div>
          </div>
        </div>
      )}

      {review && (
        <div className="sample-import-result">
          <ImportReviewTabs active={activeTab} onChange={setActiveTab} counts={{ review: groupedReviewIssueCount }} visibleTabs={['review', 'data', 'runtime', 'source']}>
            <p role="status">Review only — this report does not create a script or verify replay.</p>
            <div hidden={activeTab !== 'review'} className="script-import-review">
            <div className="script-import-review-header">
              <div>
                <h3>{review.name || 'Review Pack'}</h3>
                <p>{(review.package || 'Unknown package').replace(/_/g, ' ')} · {(review.status || 'Review required').replace(/_/g, ' ')}</p>
              </div>
            </div>
            <ImportConversionDetails title="Source details" description="Source inventory from this review. Execution posture and required follow-up are shown separately." facts={[
              { label: 'files', value: review.source_summary?.files ?? files.length },
              { label: 'bytes', value: review.source_summary?.bytes ?? sourceText.length },
              { label: 'posture', value: (review.execution_posture?.posture || 'review').replace(/_/g, ' ') },
              { label: 'suggestions', value: review.repair_suggestions?.length || 0 }
            ]} />
            <ImportIssueWorkspace issues={reviewIssues} onNavigate={setActiveTab} />
            {review.next_actions?.length ? (
              <div className="script-import-review-unsupported">
                <h4>Next Actions</h4>
                <ul>{review.next_actions.map(action => <li key={action}>{action}</li>)}</ul>
              </div>
            ) : null}
          </div>
          {activeTab === 'data' && <p>Confirm correlation extractors and parameter/data-source fit in the executable script. This review pack cannot apply those changes; use the issue handoffs on Review to open the appropriate importer.</p>}
          {activeTab === 'runtime' && <p>Execution posture: {(review.execution_posture?.posture || 'review only').replace(/_/g, ' ')}. A supported runner and controlled replay evidence are required before promotion. This page does not configure or start a runner.</p>}
          {activeTab === 'source' && <p>No source-to-DSL mapping is available because this flow generates findings rather than a script. Source evidence and package signals remain visible in the Review issue workspace.</p>}
          {onClose && <p><button type="button" className="btn btn-secondary" onClick={onClose}>Choose an importer</button> Use the appropriate script importer to create a draft, resolve these findings, and attach controlled replay evidence.</p>}
          </ImportReviewTabs>
        </div>
      )}
    </div>
  )
}
