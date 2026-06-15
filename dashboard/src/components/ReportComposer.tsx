import { useCallback, useEffect, useMemo, useState } from 'react'

import Logo from './Logo'
import ReportCharts from './ReportCharts'
import { useApi } from '../hooks/useApi'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { ReportAnnotations, ReportCommentarySection, ReportFinding, RunArchiveReport } from '../types'
import type { ReportProfileId } from './GenerateReportModal'
import './ReportComposer.css'

const PROFILE_LABEL: Record<ReportProfileId, string> = {
  executive: 'Executive Summary',
  engineer: 'Detailed Technical',
}

interface Props {
  testId: string
  testName: string
  profile: ReportProfileId
  onClose: () => void
}

export default function ReportComposer({ testId, testName, profile, onClose }: Props) {
  const api = useApi()

  const [report, setReport] = useState<RunArchiveReport | null>(null)
  const [sections, setSections] = useState<ReportCommentarySection[]>([])
  const [title, setTitle] = useState('')
  const [preparedBy, setPreparedBy] = useState('')
  const [narrative, setNarrative] = useState('')
  const [comments, setComments] = useState<Record<string, string>>({})
  const [verdictOverride, setVerdictOverride] = useState('')
  const [verdictExplanation, setVerdictExplanation] = useState('')
  const [findings, setFindings] = useState<ReportFinding[]>([])
  const [updatedBy, setUpdatedBy] = useState('')

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [status, setStatus] = useState('')

  const applyAnnotations = useCallback((annotations: ReportAnnotations) => {
    setTitle(annotations.title || '')
    setPreparedBy(annotations.prepared_by || '')
    setNarrative(annotations.executive_narrative || '')
    setComments(annotations.section_comments || {})
    setVerdictOverride(annotations.verdict_override || '')
    setVerdictExplanation(annotations.verdict_explanation || '')
    setFindings(annotations.findings || [])
    setUpdatedBy(annotations.updated_by || '')
  }, [])

  const addFinding = useCallback(() => {
    setFindings((current) => [...current, { id: '', title: '', severity: 'minor' }])
  }, [])

  const updateFinding = useCallback((index: number, patch: Partial<ReportFinding>) => {
    setFindings((current) => current.map((finding, i) => (i === index ? { ...finding, ...patch } : finding)))
  }, [])

  const removeFinding = useCallback((index: number) => {
    setFindings((current) => current.filter((_, i) => i !== index))
  }, [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')

    Promise.all([api.getTestArchiveReport(testId), api.getTestReportAnnotations(testId, profile)])
      .then(([reportResult, annotationsResult]) => {
        if (cancelled) return
        setReport(reportResult)
        setSections(annotationsResult.sections || [])
        applyAnnotations(annotationsResult.annotations)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to load report data'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [api, testId, profile, applyAnnotations])

  const persist = useCallback(async (): Promise<boolean> => {
    setSaving(true)
    setStatus('')
    try {
      // Drop findings with no title so a stray empty row never replaces the
      // auto-derived list; the backend backfills any missing F-0n ids.
      const cleanedFindings = findings
        .map((finding) => ({ ...finding, title: finding.title.trim() }))
        .filter((finding) => finding.title !== '')
      const response = await api.saveTestReportAnnotations(testId, profile, {
        title: title.trim(),
        prepared_by: preparedBy.trim(),
        executive_narrative: narrative.trim(),
        section_comments: comments,
        verdict_override: verdictOverride,
        verdict_explanation: verdictExplanation.trim(),
        findings: cleanedFindings,
      })
      applyAnnotations(response.annotations)
      setStatus('Saved')
      return true
    } catch (err: unknown) {
      setStatus(getErrorMessage(err, 'Save failed'))
      return false
    } finally {
      setSaving(false)
    }
  }, [api, testId, profile, title, preparedBy, narrative, comments, verdictOverride, verdictExplanation, findings, applyAnnotations])

  const handleDownloadHtml = useCallback(async () => {
    if (!(await persist())) return
    try {
      const blob = await api.downloadTestReport(testId, profile)
      const slug = (testName || testId).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
      downloadBlob(blob, `${slug || 'apexload'}-${profile}-report.html`)
    } catch (err: unknown) {
      setStatus(getErrorMessage(err, 'Download failed'))
    }
  }, [api, persist, testId, testName, profile])

  const handlePrint = useCallback(async () => {
    await persist()
    window.print()
  }, [persist])

  const overview = report?.overview
  const manifest = report?.manifest
  const slaBreached = manifest?.sla_breached ?? false

  const topActions = useMemo(() => {
    const entries = Object.entries(report?.by_action || {})
    entries.sort((a, b) => b[1].transactions - a[1].transactions)
    return entries.slice(0, profile === 'executive' ? 5 : 20)
  }, [report?.by_action, profile])

  return (
    <div className="report-composer-overlay" role="dialog" aria-modal="true" aria-label="Report composer">
      <div className="report-toolbar">
        <div className="report-toolbar-left">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
          <span className="report-toolbar-title">{PROFILE_LABEL[profile]} · {testName}</span>
        </div>
        <div className="report-toolbar-right">
          {status && <span className="report-save-status">{status}</span>}
          <button type="button" className="btn btn-secondary" onClick={persist} disabled={saving || loading}>
            {saving ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="btn btn-secondary" onClick={handleDownloadHtml} disabled={loading || !report}>
            Download HTML
          </button>
          <button type="button" className="btn btn-primary" onClick={handlePrint} disabled={loading || !report}>
            Print / Save PDF
          </button>
        </div>
      </div>

      <div className="report-scroll">
        {loading && <p className="report-status">Loading report…</p>}
        {error && <p className="report-status report-error">{error}</p>}

        {!loading && !error && report && (
          <article className="report-paper">
            <header className="report-cover">
              <div className="report-brand">
                <Logo size={40} />
                <span className="report-brand-name">APEX Load</span>
              </div>
              <p className="report-eyebrow">{PROFILE_LABEL[profile]} Report</p>
              <input
                className="report-title-input"
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                placeholder="Report title"
                aria-label="Report title"
              />
              <div className="report-cover-meta">
                <span><strong>Test:</strong> {manifest?.test_name || testName}</span>
                <span><strong>Test ID:</strong> {manifest?.test_id || testId}</span>
                <span><strong>Status:</strong> {manifest?.status || 'COMPLETE'}</span>
                <span><strong>Completed:</strong> {formatTime(manifest?.completed_at)}</span>
              </div>
              <label className="report-prepared-by">
                <span>Prepared by</span>
                <input
                  value={preparedBy}
                  onChange={(event) => setPreparedBy(event.target.value)}
                  placeholder="Analyst name"
                />
              </label>
            </header>

            <section className="report-metrics">
              <Metric label="SLA" value={slaBreached ? 'Breached' : 'Passed'} tone={slaBreached ? 'danger' : 'ok'} />
              <Metric label="Transactions" value={formatInt(overview?.transactions)} />
              <Metric label="Avg Response" value={`${formatNum(overview?.avg_response_ms)} ms`} />
              <Metric label="Error Rate" value={`${formatNum(overview?.error_pct)}%`} tone={(overview?.error_pct ?? 0) > 0 ? 'warn' : 'ok'} />
              <Metric label="Peak TPS" value={formatNum(overview?.peak_tps)} />
              <Metric label="Peak VUsers" value={formatInt(overview?.peak_active_vusers)} />
            </section>

            <section className="report-section">
              <h2>Verdict</h2>
              <p className="report-hint no-print">
                Leave on “Auto” to use the verdict derived from SLA, telemetry, and completeness signals.
                Choosing an override (e.g. for a transient, self-recovered breach) replaces it in the report.
              </p>
              <label className="report-comment">
                <span>Verdict override</span>
                <select
                  className="report-select"
                  value={verdictOverride}
                  onChange={(event) => setVerdictOverride(event.target.value)}
                >
                  <option value="">Auto (derived)</option>
                  <option value="Pass">Pass</option>
                  <option value="Conditional Pass">Conditional Pass</option>
                  <option value="Fail">Fail</option>
                </select>
              </label>
              <label className="report-comment">
                <span>Verdict explanation</span>
                <textarea
                  value={verdictExplanation}
                  onChange={(event) => setVerdictExplanation(event.target.value)}
                  placeholder="Optional — shown in the verdict bar when an override is set."
                  rows={2}
                />
              </label>
            </section>

            <section className="report-section">
              <h2>Executive Summary</h2>
              <textarea
                className="report-narrative"
                value={narrative}
                onChange={(event) => setNarrative(event.target.value)}
                placeholder="Summarize the outcome of this test run for stakeholders…"
                rows={4}
              />
            </section>

            <section className="report-section report-graphs">
              <h2>Performance Graphs</h2>
              <ReportCharts report={report} />
            </section>

            {slaBreached && (manifest?.sla_details?.length ?? 0) > 0 && (
              <section className="report-section">
                <h2>SLA Findings</h2>
                <ul className="report-list">
                  {manifest?.sla_details?.map((detail, index) => (
                    <li key={index}>{detail}</li>
                  ))}
                </ul>
              </section>
            )}

            {topActions.length > 0 && (
              <section className="report-section">
                <h2>Action Summary</h2>
                <table className="report-table">
                  <thead>
                    <tr>
                      <th>Action</th>
                      <th>Transactions</th>
                      <th>Errors</th>
                      <th>Error %</th>
                      <th>Avg (ms)</th>
                      <th>Max (ms)</th>
                    </tr>
                  </thead>
                  <tbody>
                    {topActions.map(([name, stats]) => (
                      <tr key={name}>
                        <td>{name}</td>
                        <td>{formatInt(stats.transactions)}</td>
                        <td>{formatInt(stats.errors)}</td>
                        <td>{formatNum(stats.error_pct)}%</td>
                        <td>{formatNum(stats.avg_response_ms)}</td>
                        <td>{formatNum(stats.max_response_ms)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            )}

            {profile === 'engineer' && (report.error_patterns?.length ?? 0) > 0 && (
              <section className="report-section">
                <h2>Error Patterns</h2>
                <table className="report-table">
                  <thead>
                    <tr>
                      <th>Pattern</th>
                      <th>Occurrences</th>
                      <th>First Seen</th>
                      <th>Latest Seen</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.error_patterns?.map((pattern) => (
                      <tr key={pattern.signature}>
                        <td>{pattern.sample}</td>
                        <td>{formatInt(pattern.count)}</td>
                        <td>{formatTime(pattern.first_seen)}</td>
                        <td>{formatTime(pattern.latest_seen)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            )}

            {profile === 'engineer' && report.resource_peaks && (
              <section className="report-section">
                <h2>Resource Peaks</h2>
                <div className="report-metrics">
                  <Metric label="CPU Peak" value={`${formatNum(report.resource_peaks.cpu_process_pct)}%`} />
                  <Metric label="Memory Peak" value={`${formatNum(report.resource_peaks.mem_process_mb)} MB`} />
                  <Metric label="Net Sent" value={`${formatNum(report.resource_peaks.net_sent_kbps)} KB/s`} />
                  <Metric label="Net Recv" value={`${formatNum(report.resource_peaks.net_recv_kbps)} KB/s`} />
                </div>
              </section>
            )}

            <section className="report-section">
              <h2>Findings</h2>
              <p className="report-hint no-print">
                Leave empty to use the findings auto-derived from the run. Add entries to curate the
                F-0n list shown in the report; ids are filled in automatically.
              </p>
              {findings.map((finding, index) => (
                <div key={index} className="report-finding-edit">
                  <div className="report-finding-edit-head">
                    <span className="report-finding-id">
                      {finding.id || `F-${String(index + 1).padStart(2, '0')}`}
                    </span>
                    <input
                      className="report-finding-title"
                      value={finding.title}
                      onChange={(event) => updateFinding(index, { title: event.target.value })}
                      placeholder="Finding title"
                      aria-label={`Finding ${index + 1} title`}
                    />
                    <select
                      className="report-select"
                      value={finding.severity}
                      onChange={(event) =>
                        updateFinding(index, { severity: event.target.value as ReportFinding['severity'] })
                      }
                      aria-label={`Finding ${index + 1} severity`}
                    >
                      <option value="major">Major</option>
                      <option value="minor">Minor</option>
                      <option value="info">Info</option>
                    </select>
                    <button
                      type="button"
                      className="btn btn-secondary no-print"
                      onClick={() => removeFinding(index)}
                    >
                      Remove
                    </button>
                  </div>
                  <div className="report-finding-edit-grid">
                    <label className="report-comment">
                      <span>Observed</span>
                      <textarea
                        rows={2}
                        value={finding.observed || ''}
                        onChange={(event) => updateFinding(index, { observed: event.target.value })}
                      />
                    </label>
                    <label className="report-comment">
                      <span>Evidence</span>
                      <textarea
                        rows={2}
                        value={finding.evidence || ''}
                        onChange={(event) => updateFinding(index, { evidence: event.target.value })}
                      />
                    </label>
                    <label className="report-comment">
                      <span>Root cause</span>
                      <textarea
                        rows={2}
                        value={finding.root_cause || ''}
                        onChange={(event) => updateFinding(index, { root_cause: event.target.value })}
                      />
                    </label>
                    <label className="report-comment">
                      <span>Impact</span>
                      <textarea
                        rows={2}
                        value={finding.impact || ''}
                        onChange={(event) => updateFinding(index, { impact: event.target.value })}
                      />
                    </label>
                  </div>
                </div>
              ))}
              <button type="button" className="btn btn-secondary no-print report-add-finding" onClick={addFinding}>
                Add finding
              </button>
            </section>

            <section className="report-section">
              <h2>Analyst Commentary</h2>
              <p className="report-hint no-print">Edit the commentary below — it is saved with the report and appears in the downloaded HTML.</p>
              {sections.map((section) => (
                <label key={section.id} className="report-comment">
                  <span>{section.label}</span>
                  <textarea
                    value={comments[section.id] || ''}
                    onChange={(event) =>
                      setComments((current) => ({ ...current, [section.id]: event.target.value }))
                    }
                    rows={3}
                  />
                </label>
              ))}
            </section>

            <footer className="report-footer">
              Generated by APEX Load.{updatedBy ? ` Commentary last edited by ${updatedBy}.` : ''}
            </footer>
          </article>
        )}
      </div>
    </div>
  )
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: 'ok' | 'warn' | 'danger' }) {
  return (
    <div className={`report-metric${tone ? ` tone-${tone}` : ''}`}>
      <span className="report-metric-label">{label}</span>
      <span className="report-metric-value">{value}</span>
    </div>
  )
}

function formatNum(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) return '0.0'
  return value.toFixed(1)
}

function formatInt(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) return '0'
  return Math.round(value).toLocaleString()
}

function formatTime(value: string | undefined): string {
  if (!value) return 'n/a'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'n/a'
  return date.toLocaleString()
}
