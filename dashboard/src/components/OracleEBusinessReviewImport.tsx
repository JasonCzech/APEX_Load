import { useEffect, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type OracleEBusinessReviewResponse } from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'
import './SampleScriptImport.css'
import './ScriptImportReview.css'

interface Props {
  projectId?: string
  onClose?: () => void
}

const packageOptions = [
  { value: '', label: 'Auto-detect package' },
  { value: 'oracle_web', label: 'Oracle E-Business Web' },
  { value: 'siebel_web', label: 'Siebel Web' },
  { value: 'oracle_nca', label: 'Oracle NCA / Forms' },
  { value: 'web_http_html', label: 'Generic Web HTTP/HTML' },
]

export default function OracleEBusinessReviewImport({ projectId, onClose }: Props) {
  const api = useApi()
  const reviewTask = useAsyncTask<OracleEBusinessReviewResponse>()
  const [name, setName] = useState('')
  const [packageName, setPackageName] = useState('')
  const [sourceText, setSourceText] = useState('')
  const [files, setFiles] = useState<Array<{ name: string; content: string }>>([])
  const [supportNotes, setSupportNotes] = useState<string[]>([])
  const [messageError, setMessageError] = useState('')

  useEffect(() => {
    let cancelled = false
    api.getOracleEBusinessSupportMatrix()
      .then(response => { if (!cancelled) setSupportNotes(response.notes || []) })
      .catch(() => { if (!cancelled) setSupportNotes([]) })
    return () => { cancelled = true }
  }, [api])

  const handleFiles = async (nextFiles: FileList | null) => {
    if (!nextFiles?.length) return
    try {
      const loaded = await Promise.all(Array.from(nextFiles).map(async file => ({ name: file.name, content: await file.text() })))
      setFiles(loaded)
      setMessageError('')
      reviewTask.reset()
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Unable to read review source file.'))
    }
  }

  const handleReview = async () => {
    if (!sourceText.trim() && files.length === 0) {
      setMessageError('Provide source text or upload at least one review file.')
      return
    }
    setMessageError('')
    try {
      await reviewTask.run(async () => api.reviewOracleEBusiness({
        project_id: projectId,
        name: name || undefined,
        package: packageName || undefined,
        source_text: sourceText || undefined,
        files,
      }), { clearValue: true })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Oracle E-Business review failed.'))
    }
  }

  const review = reviewTask.value

  return (
    <div className="sample-import">
      <div className="sample-import-header">
        <div>
          <h2>Oracle E-Business / Siebel Review</h2>
          <p className="sample-import-subtitle">Review packaged enterprise app sources for package signals, correlation candidates, execution posture, and evidence requirements.</p>
        </div>
        {onClose && <button className="btn btn-secondary" type="button" onClick={onClose}>Back</button>}
      </div>

      <div className="sample-import-card">
        <div className="sample-import-grid two-col">
          <label className="sample-import-field">
            <span>Review Name</span>
            <input value={name} onChange={event => setName(event.target.value)} placeholder="Oracle checkout review" />
          </label>
          <label className="sample-import-field">
            <span>Package</span>
            <select value={packageName} onChange={event => setPackageName(event.target.value)}>
              {packageOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
          <label className="sample-import-field sample-import-span-2">
            <span>Source Text</span>
            <textarea value={sourceText} onChange={event => setSourceText(event.target.value)} rows={12} spellCheck={false} placeholder="Paste HAR excerpts, source snippets, exported steps, URLs, or notes for review." />
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
        </div>
      </div>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" type="button" onClick={handleReview} disabled={reviewTask.pending}>
          {reviewTask.pending ? <><span className="spinner" /> Reviewing...</> : 'Run Review'}
        </button>
      </div>

      {(messageError || reviewTask.error) && <div className="sample-import-error">{messageError || reviewTask.error}</div>}

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
          <div className="script-import-review">
            <div className="script-import-review-header">
              <div>
                <h3>{review.name || 'Review Pack'}</h3>
                <p>{review.package} · {review.status}</p>
              </div>
            </div>
            <div className="sample-import-stats-grid">
              <div className="sample-stat">
                <span className="sample-stat-value">{review.source_summary?.files ?? files.length}</span>
                <span className="sample-stat-label">files</span>
              </div>
              <div className="sample-stat">
                <span className="sample-stat-value">{review.source_summary?.bytes ?? sourceText.length}</span>
                <span className="sample-stat-label">bytes</span>
              </div>
              <div className="sample-stat">
                <span className="sample-stat-value">{review.execution_posture?.posture || 'review'}</span>
                <span className="sample-stat-label">posture</span>
              </div>
              <div className="sample-stat">
                <span className="sample-stat-value">{review.repair_suggestions?.length || 0}</span>
                <span className="sample-stat-label">suggestions</span>
              </div>
            </div>
            {review.review_sections?.length ? (
              <div className="script-import-review-sections">
                <h4>Review Sections ({review.review_sections.length})</h4>
                <div className="script-import-review-section-list">
                  {review.review_sections.map(section => (
                    <details key={section.key} className={`script-import-review-section severity-${section.severity || 'info'}`} open={section.severity !== 'info'}>
                      <summary>
                        <span>{section.title}</span>
                        <small>{section.severity}</small>
                      </summary>
                      {section.summary && <p>{section.summary}</p>}
                      {section.items?.length ? <ul>{section.items.map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul> : null}
                    </details>
                  ))}
                </div>
              </div>
            ) : null}
            {review.next_actions?.length ? (
              <div className="script-import-review-unsupported">
                <h4>Next Actions</h4>
                <ul>{review.next_actions.map(action => <li key={action}>{action}</li>)}</ul>
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  )
}
