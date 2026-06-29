import { useCallback, useEffect, useMemo, useState } from 'react'

import Logo from './Logo'
import MarkdownRenderer from './MarkdownRenderer'
import ReportCharts from './ReportCharts'
import { useApi } from '../hooks/useApi'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { ReportAnnotations, ReportCommentarySection, ReportFinding, RunArchiveReport, TestNote } from '../types'
import type { ReportProfileId } from './GenerateReportModal'
import './ReportComposer.css'

const PROFILE_LABEL: Record<string, string> = {
  executive: 'Executive Summary',
  engineer: 'Detailed Technical',
  audit: 'Audit',
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
  const [testNotes, setTestNotes] = useState<TestNote[]>([])
  const [testNotesError, setTestNotesError] = useState('')
  const [selectedNoteId, setSelectedNoteId] = useState('')
  const [noteSectionId, setNoteSectionId] = useState('')

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

    Promise.all([
      api.getTestArchiveReport(testId),
      api.getTestReportAnnotations(testId, profile),
      api.listTestNotes(testId)
        .then((response) => ({ response, error: '' }))
        .catch((err: unknown) => ({
          response: { test_id: testId, notes: [] as TestNote[] },
          error: getErrorMessage(err, 'Unable to load test notes'),
        })),
    ])
      .then(([reportResult, annotationsResult, notesResult]) => {
        if (cancelled) return
        setReport(reportResult)
        setSections(annotationsResult.sections || [])
        setNoteSectionId(annotationsResult.sections?.[0]?.id || '')
        applyAnnotations(annotationsResult.annotations)
        setTestNotesError(notesResult.error)
        const loadedNotes = notesResult.response.notes || []
        setTestNotes(loadedNotes)
        setSelectedNoteId(loadedNotes[0]?.id || '')
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
  const profileLabel = PROFILE_LABEL[profile] || profile
  // Gate preview sections off the profile's declared sections (the backend's
  // single source of truth), not hardcoded profile ids. This keeps a 4th/custom
  // backend profile from rendering a hybrid-executive preview.
  const includedSections = useMemo(
    () => new Set(report?.report_profiles?.find((candidate) => candidate.id === profile)?.included_sections ?? []),
    [report?.report_profiles, profile],
  )
  const hasSection = useCallback((...ids: string[]) => ids.some((id) => includedSections.has(id)), [includedSections])
  const isExecutive = profile === 'executive'
  const isEngineer = profile === 'engineer'
  const isAudit = profile === 'audit'
  // Narrative/summary heading derived from declared sections so it stays correct
  // for profiles beyond the built-in three.
  const summaryLabel = hasSection('audit_identity', 'official_outcome')
    ? 'Audit Summary'
    : hasSection('technical_overview')
      ? 'Technical Summary'
      : 'Executive Summary'
  const executionPlanApproval = manifest?.provenance?.governance?.execution_plan_approval
  const executionPlanApprovalSummary = executionPlanApproval
    ? [
        executionPlanApproval.status || 'n/a',
        executionPlanApproval.resolved_by || executionPlanApproval.requested_by || 'n/a',
        executionPlanApproval.decision_reason || 'no reason captured',
      ].join(' · ')
    : 'not captured'
  const selectedNote = useMemo(
    () => testNotes.find((note) => note.id === selectedNoteId) || null,
    [selectedNoteId, testNotes],
  )

  const topActions = useMemo(() => {
    const entries = Object.entries(report?.by_action || {})
    entries.sort((a, b) => b[1].transactions - a[1].transactions)
    return entries.slice(0, profile === 'executive' ? 5 : 20)
  }, [report?.by_action, profile])

  const appendSelectedNoteToSummary = useCallback(() => {
    if (!selectedNote) return
    setNarrative((current) => appendReportText(current, selectedNote.body))
    setStatus('Inserted note')
  }, [selectedNote])

  const appendSelectedNoteToSection = useCallback(() => {
    if (!selectedNote) return
    const sectionId = noteSectionId || sections[0]?.id
    if (!sectionId) return
    setComments((current) => ({
      ...current,
      [sectionId]: appendReportText(current[sectionId] || '', selectedNote.body),
    }))
    setNoteSectionId(sectionId)
    setStatus('Inserted note')
  }, [noteSectionId, sections, selectedNote])

  const convertSelectedNoteToFinding = useCallback(() => {
    if (!selectedNote) return
    setFindings((current) => [
      ...current,
      {
        id: '',
        title: firstNoteLine(selectedNote.body),
        severity: 'info',
        observed: selectedNote.body,
      },
    ])
    setStatus('Inserted finding')
  }, [selectedNote])

  return (
    <div className="report-composer-overlay" role="dialog" aria-modal="true" aria-label="Report composer">
      <div className="report-toolbar">
        <div className="report-toolbar-left">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
          <span className="report-toolbar-title">{profileLabel} · {testName}</span>
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
              <p className="report-eyebrow">{profileLabel} Report</p>
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
              <h2>{summaryLabel}</h2>
              <textarea
                className="report-narrative"
                value={narrative}
                onChange={(event) => setNarrative(event.target.value)}
                placeholder={isAudit ? 'Summarize the audit scope, evidence posture, and exceptions…' : isEngineer ? 'Summarize the technical outcome, bottlenecks, and follow-up…' : 'Summarize the outcome of this test run for stakeholders…'}
                rows={4}
              />
            </section>

            {hasSection('graphs') && (
              <section className="report-section report-graphs">
                <h2>{isExecutive ? 'Headline Performance' : 'Performance Graphs'}</h2>
                <ReportCharts report={report} />
              </section>
            )}

            {hasSection('sla_detail', 'sla_rollup') && slaBreached && (manifest?.sla_details?.length ?? 0) > 0 && (
              <section className="report-section">
                <h2>SLA Findings</h2>
                <ul className="report-list">
                  {manifest?.sla_details?.map((detail, index) => (
                    <li key={index}>{detail}</li>
                  ))}
                </ul>
              </section>
            )}

            {hasSection('actions') && topActions.length > 0 && (
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

            {hasSection('network_phases') && report.network_phases?.overall && (
              <section className="report-section">
                <h2>Network Phase Breakdown</h2>
                <div className="report-metrics">
                  <Metric label="DNS Avg" value={`${formatNum(report.network_phases.overall.avg_dns_ms)} ms`} />
                  <Metric label="Connect Avg" value={`${formatNum(report.network_phases.overall.avg_connect_ms)} ms`} />
                  <Metric label="TLS Avg" value={`${formatNum(report.network_phases.overall.avg_tls_ms)} ms`} />
                  <Metric label="TTFB Avg" value={`${formatNum(report.network_phases.overall.avg_ttfb_ms)} ms`} />
                </div>
              </section>
            )}

            {hasSection('errors') && (report.error_patterns?.length ?? 0) > 0 && (
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

            {hasSection('resources') && report.resource_peaks && (
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

            {hasSection('audit_identity', 'artifact_integrity', 'governance') && (
              <section className="report-section">
                <h2>Audit Evidence</h2>
                <table className="report-table">
                  <tbody>
                    <tr><th>Manifest Schema</th><td>{report.manifest.schema_version}</td></tr>
                    <tr><th>Report Schema</th><td>{report.schema_version || 'n/a'}</td></tr>
                    <tr><th>Compatibility</th><td>{report.compatibility?.status || 'n/a'}</td></tr>
                    <tr><th>Completeness</th><td>{formatNum(report.completeness?.score_pct)}% · {report.completeness?.status || 'n/a'}</td></tr>
                    <tr><th>Created By</th><td>{formatActor(report.manifest.provenance?.created_by_display_name, report.manifest.provenance?.created_by_subject)}</td></tr>
                    <tr><th>Started By</th><td>{formatActor(report.manifest.provenance?.started_by_display_name, report.manifest.provenance?.started_by_subject)}</td></tr>
                    <tr><th>Stopped By</th><td>{formatActor(report.manifest.provenance?.stopped_by_display_name, report.manifest.provenance?.stopped_by_subject)}</td></tr>
                    <tr><th>Execution-plan Approval</th><td>{executionPlanApprovalSummary}</td></tr>
                    <tr><th>Runtime Overrides</th><td>{report.change_attribution?.runtime_overrides ? (report.change_attribution.runtime_override_fields || []).join(', ') || 'Yes' : 'No'}</td></tr>
                  </tbody>
                </table>
                <h3>Artifact Integrity</h3>
                <table className="report-table">
                  <thead>
                    <tr><th>Artifact</th><th>Records</th><th>Bytes</th><th>SHA-256</th></tr>
                  </thead>
                  <tbody>
                    {(report.artifact_inventory || report.manifest.artifacts || []).map((artifact) => (
                      <tr key={artifact.name}>
                        <td>{artifact.name}</td>
                        <td>{formatInt(artifact.count)}</td>
                        <td>{formatInt(artifact.size_bytes)}</td>
                        <td>{artifact.sha256 || 'not captured'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
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
              <div className="report-notes-source no-print">
                {testNotesError ? (
                  <p className="report-note-source-empty report-error" role="alert">{testNotesError}</p>
                ) : testNotes.length === 0 ? (
                  <p className="report-note-source-empty">No test notes are available for this run.</p>
                ) : (
                  <>
                    <div className="report-notes-source-list">
                      {testNotes.map((note) => (
                        <button
                          key={note.id}
                          type="button"
                          className={`report-note-source-item${note.id === selectedNoteId ? ' selected' : ''}`}
                          onClick={() => setSelectedNoteId(note.id)}
                        >
                          <strong>{firstNoteLine(note.body)}</strong>
                          <span>Updated {formatTime(note.updated_at)}</span>
                        </button>
                      ))}
                    </div>
                    <div className="report-note-source-preview">
                      {selectedNote && (
                        <>
                          <div className="report-note-source-markdown markdown-body">
                            <MarkdownRenderer source={selectedNote.body} blockKeyPrefix={`report-note-${selectedNote.id}`} />
                          </div>
                          <div className="report-note-source-actions">
                            <button type="button" className="btn btn-secondary" onClick={appendSelectedNoteToSummary}>
                              Append to {summaryLabel}
                            </button>
                            <select
                              className="report-select"
                              value={noteSectionId || sections[0]?.id || ''}
                              onChange={(event) => setNoteSectionId(event.target.value)}
                              aria-label="Commentary section"
                            >
                              {sections.map((section) => (
                                <option key={section.id} value={section.id}>{section.label}</option>
                              ))}
                            </select>
                            <button type="button" className="btn btn-secondary" onClick={appendSelectedNoteToSection} disabled={sections.length === 0}>
                              Append to Section
                            </button>
                            <button type="button" className="btn btn-secondary" onClick={convertSelectedNoteToFinding}>
                              Convert to Info Finding
                            </button>
                          </div>
                        </>
                      )}
                    </div>
                  </>
                )}
              </div>
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

function formatActor(displayName?: string, subject?: string): string {
  const display = displayName?.trim()
  const id = subject?.trim()
  if (display && id) return `${display} (${id})`
  return display || id || 'n/a'
}

function appendReportText(current: string, addition: string): string {
  const body = addition.trim()
  if (!body) return current
  return current.trim() ? `${current.trimEnd()}\n\n${body}` : body
}

function firstNoteLine(body: string): string {
  const line = body.split(/\r?\n/).find((entry) => entry.trim())?.trim() || 'Test note'
  const normalized = line.replace(/^#{1,6}\s+/, '')
  return normalized.length > 90 ? `${normalized.slice(0, 87)}...` : normalized
}
