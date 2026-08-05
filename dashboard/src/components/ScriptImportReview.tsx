import { type MutableRefObject, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import {
  type ImportReviewSection,
  type Parameterization,
  type ParameterizationDiff,
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import ParameterizationReview from './ParameterizationReview'
import { importDraftExpiryStatus, protectedImportExpiryLabel } from './protectedImportSession'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import { requirementsPreserveDestinationValues } from './secretBindingUtils'
import IssueDetailsDialog, { type IssueDetail } from './IssueDetailsDialog'
import './ScriptImportReview.css'

const severityRank: Record<string, number> = {
  blocker: 0,
  warning: 1,
  info: 2,
}

const PREVIEW_MAX_CHARS = 120000

type CredentialAction = 'save' | 'edit'

interface CredentialActionRequest {
  id: number
  action: CredentialAction
  handler?: () => void
  sourceIdentity: string
  phase: 'committing' | 'applying' | 'awaiting-resolution'
}

function previewJSON(value: unknown): { text: string; truncated: boolean } {
  const text = JSON.stringify(value, null, 2)
  if (text.length <= PREVIEW_MAX_CHARS) {
    return { text, truncated: false }
  }
  return {
    text: `${text.slice(0, PREVIEW_MAX_CHARS)}\n…`,
    truncated: true,
  }
}

interface Props {
  title: string
  reviewGeneration?: string | number
  subtitle?: ReactNode
  script: ScriptDefinition
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  unsupportedElements?: string[]
  reviewSections?: ImportReviewSection[]
  parameterization?: Parameterization
  onParameterizationApplied?: (
    script: ScriptDefinition,
    diffs: ParameterizationDiff[],
    selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => void
  secretBindingRequirements?: SecretBindingRequirement[]
  sourceMigrationRequirements?: SecretBindingRequirement[]
  preserveSourceSessionAfterApply?: boolean
  onSecretBindingsApply?: (bindings: SecretBindingSelection[]) => void | Promise<void>
  projectId?: string
  sourceSecretSessionId?: string
  sourceSecretSessionExpiresAt?: string
  sourceSecretMigrationAllowed?: boolean
  protectedImportSessionExpiresAt?: string
  importDraftExpiresAt?: string
  onSourceSecretSessionSettled?: (
    sourceSecretSessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => void
  credentialReviewRef?: MutableRefObject<SecretBindingReviewHandle | null>
  onCredentialsPreparedChange?: (prepared: PreparedSecretBindings | null) => void
  saving?: boolean
  saveDisabled?: boolean
  reviewBusy?: boolean
  onReviewBusyChange?: (busy: boolean) => void
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  previewAside?: ReactNode
  showPreview?: boolean
  showPreviewAside?: boolean
  onSave?: () => void
  onEdit?: () => void
  children?: ReactNode
}

export default function ScriptImportReview({
  title,
  reviewGeneration = 0,
  subtitle,
  script,
  previewData,
  warnings = [],
  warningsTitle = 'Warnings',
  unsupportedElements = [],
  reviewSections = [],
  parameterization,
  onParameterizationApplied,
  secretBindingRequirements = [],
  sourceMigrationRequirements,
  preserveSourceSessionAfterApply = false,
  onSecretBindingsApply,
  projectId,
  sourceSecretSessionId,
  sourceSecretSessionExpiresAt,
  sourceSecretMigrationAllowed = false,
  protectedImportSessionExpiresAt,
  importDraftExpiresAt,
  onSourceSecretSessionSettled,
  credentialReviewRef,
  onCredentialsPreparedChange,
  saving = false,
  saveDisabled = false,
  reviewBusy = false,
  onReviewBusyChange,
  saveLabel = 'Save To Script Library',
  editLabel = 'Open In Visual Editor',
  previewTitle = 'Generated Script Preview',
  previewAside,
  showPreview = true,
  showPreviewAside = true,
  onSave,
  onEdit,
  children,
}: Props) {
  const hasUnresolvedSecretBindings = secretBindingRequirements.length > 0
  const allRemainingRequirementsPreserveValues = requirementsPreserveDestinationValues(secretBindingRequirements)
  const internalBindingReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const bindingReviewRef = credentialReviewRef || internalBindingReviewRef
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [credentialAction, setCredentialAction] = useState<CredentialAction | null>(null)
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const [importDraftExpiryGeneration, setImportDraftExpiryGeneration] = useState(0)
  const [issueDialog, setIssueDialog] = useState<{ title: string; issues: IssueDetail[] } | null>(null)
  const parameterizationApplyingRef = useRef(false)
  const credentialActionRequestRef = useRef<CredentialActionRequest | null>(null)
  const credentialActionRequestIdRef = useRef(0)
  const reviewIdentity = String(reviewGeneration)
  const currentReviewIdentityRef = useRef(reviewIdentity)
  currentReviewIdentityRef.current = reviewIdentity
  const currentHasUnresolvedSecretBindingsRef = useRef(hasUnresolvedSecretBindings)
  currentHasUnresolvedSecretBindingsRef.current = hasUnresolvedSecretBindings
  const currentRemainingRequirementsPreserveValuesRef = useRef(allRemainingRequirementsPreserveValues)
  currentRemainingRequirementsPreserveValuesRef.current = allRemainingRequirementsPreserveValues
  const defaultConfigureLaterCount = useMemo(
    () => new Set(secretBindingRequirements.filter(requirement => (
      requirement.destination_value_preserved !== true
    )).map(requirement => {
      const name = requirement.suggested_name?.trim()
      if (!name) return ''
      const scope = typeof requirement.script_index === 'number'
        ? `index:${requirement.script_index}`
        : requirement.script_id
          ? `id:${requirement.script_id}`
          : requirement.script_name
            ? `name:${requirement.script_name}`
            : 'unscoped'
      return `${scope}:${name}`
    }).filter(Boolean)).size,
    [secretBindingRequirements],
  )
  const credentialsValid = !hasUnresolvedSecretBindings || (preparedSecrets?.valid ?? true)
  const credentialHandlerUnavailable = hasUnresolvedSecretBindings && !onSecretBindingsApply
  const configureLaterCount = preparedSecrets?.configureLaterCount ?? defaultConfigureLaterCount
  const selectedBindingCount = preparedSecrets?.selectedBindingCount
    ?? secretBindingRequirements.filter(requirement => requirement.destination_value_preserved !== true).length
  const deferredSaveLabel = /^Save to Script Library$/i.test(saveLabel) ? 'Save' : saveLabel
  const resolvedSaveLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${deferredSaveLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : saveLabel
  const resolvedEditLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${editLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : editLabel

  const reportInternalReviewBusy = useCallback((options?: { parameterization?: boolean; credentials?: boolean }) => {
    const parameterizationBusy = options?.parameterization ?? parameterizationApplyingRef.current
    const credentialsBusy = options?.credentials ?? credentialActionRequestRef.current !== null
    onReviewBusyChange?.(parameterizationBusy || credentialsBusy)
  }, [onReviewBusyChange])

  const clearCredentialAction = useCallback(() => {
    credentialActionRequestRef.current = null
    setCredentialAction(null)
    reportInternalReviewBusy({ credentials: false })
  }, [reportInternalReviewBusy])

  const handleParameterizationApplyingChange = useCallback((applying: boolean) => {
    parameterizationApplyingRef.current = applying
    setParameterizationApplying(applying)
    reportInternalReviewBusy({ parameterization: applying })
  }, [reportInternalReviewBusy])

  useEffect(() => {
    const request = credentialActionRequestRef.current
    if (!request) return

    if (request.sourceIdentity !== reviewIdentity) {
      clearCredentialAction()
      return
    }

    if (
      request.phase !== 'awaiting-resolution'
      || (hasUnresolvedSecretBindings && !allRemainingRequirementsPreserveValues)
    ) return
    clearCredentialAction()
    request.handler?.()
  }, [allRemainingRequirementsPreserveValues, clearCredentialAction, hasUnresolvedSecretBindings, reviewIdentity])

  useEffect(() => {
    if (hasUnresolvedSecretBindings) return
    setPreparedSecrets(null)
    // A binding commit can render the resolved draft before its awaited
    // callback advances the queued action from `applying` to
    // `awaiting-resolution`. Keep that exact request alive; runAction will
    // dispatch it as soon as the commit promise resumes. Clearing it here
    // would resolve the references but silently drop the user's Save/Edit.
    if (!credentialActionRequestRef.current) clearCredentialAction()
  }, [clearCredentialAction, hasUnresolvedSecretBindings])

  useEffect(() => {
    handleParameterizationApplyingChange(false)
  }, [handleParameterizationApplyingChange, parameterization, reviewIdentity])

  useEffect(() => {
    const expiry = Date.parse(importDraftExpiresAt || '')
    if (!Number.isFinite(expiry) || expiry <= Date.now()) return
    const timeout = window.setTimeout(
      () => setImportDraftExpiryGeneration(current => current + 1),
      Math.min(expiry - Date.now() + 10, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [importDraftExpiresAt, importDraftExpiryGeneration])

  useEffect(() => () => {
    credentialActionRequestIdRef.current += 1
    credentialActionRequestRef.current = null
    parameterizationApplyingRef.current = false
    onReviewBusyChange?.(false)
  }, [onReviewBusyChange])

  useEffect(() => {
    onCredentialsPreparedChange?.(preparedSecrets)
  }, [onCredentialsPreparedChange, preparedSecrets])

  const applySecretBindings = useCallback(async (bindings: SecretBindingSelection[]) => {
    if (reviewIdentity !== currentReviewIdentityRef.current) {
      throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
    }
    const request = credentialActionRequestRef.current
    if (request) {
      if (request.phase !== 'committing' || request.sourceIdentity !== currentReviewIdentityRef.current) {
        throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      }
      request.phase = 'applying'
    }
    await onSecretBindingsApply?.(bindings)
    if (request && credentialActionRequestRef.current === request) {
      if (request.sourceIdentity !== currentReviewIdentityRef.current) {
        credentialActionRequestRef.current = null
        setCredentialAction(null)
        reportInternalReviewBusy({ credentials: false })
        throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      }
      request.phase = 'awaiting-resolution'
    }
  }, [onSecretBindingsApply, reportInternalReviewBusy, reviewIdentity])

  const runAction = async (action: CredentialAction) => {
    if (importDraftExpired || parameterizationApplyingRef.current || reviewBusy) return
    if (!hasUnresolvedSecretBindings) {
      if (action === 'save') onSave?.()
      if (action === 'edit') onEdit?.()
      return
    }
    if (!onSecretBindingsApply || !bindingReviewRef.current || !credentialsValid || credentialActionRequestRef.current) return
    const request: CredentialActionRequest = {
      id: ++credentialActionRequestIdRef.current,
      action,
      handler: action === 'save' ? onSave : onEdit,
      sourceIdentity: reviewIdentity,
      phase: 'committing',
    }
    credentialActionRequestRef.current = request
    setCredentialAction(action)
    reportInternalReviewBusy({ credentials: true })
    const committed = await bindingReviewRef.current.commit()
    if (credentialActionRequestRef.current !== request) return
    if (!committed) {
      clearCredentialAction()
      return
    }
    if (selectedBindingCount === 0) {
      clearCredentialAction()
      request.handler?.()
      return
    }
    // onApply normally moves the review to a requirements-free render. If it
    // completed synchronously without doing so, keep the action queued for the
    // exact same review generation instead of firing against stale props.
    if (request.phase === 'applying') request.phase = 'awaiting-resolution'
    if (
      request.phase === 'awaiting-resolution'
      && (
        !currentHasUnresolvedSecretBindingsRef.current
        || currentRemainingRequirementsPreserveValuesRef.current
      )
    ) {
      clearCredentialAction()
      request.handler?.()
    }
  }
  const preview = useMemo(
    () => showPreview ? previewJSON(previewData ?? { script }) : null,
    [previewData, script, showPreview],
  )
  const previewPane = preview ? (
    <details className="script-import-review-preview" open>
      <summary>
        <h4>{previewTitle}</h4>
      </summary>
      {preview.truncated && (
        <div className="script-import-review-preview-note" role="status">
          Preview truncated for browser performance. Save or open the script to review the full generated payload.
        </div>
      )}
      <pre>{preview.text}</pre>
    </details>
  ) : null
  const sortedReviewSections = [...reviewSections].sort((a, b) => {
    const left = severityRank[String(a.severity || '').toLowerCase()] ?? 3
    const right = severityRank[String(b.severity || '').toLowerCase()] ?? 3
    if (left !== right) return left - right
    return a.title.localeCompare(b.title)
  })
  const blockerDetails: IssueDetail[] = sortedReviewSections
    .filter(section => section.severity === 'blocker')
    .flatMap(section => section.items.length > 0
      ? section.items.map((item, index) => ({
          id: `${section.key}-blocker-${index}`,
          severity: 'blocker' as const,
          title: section.title,
          description: item,
          source: section.summary || 'Import review',
        }))
      : [{
          id: `${section.key}-blocker`,
          severity: 'blocker' as const,
          title: section.title,
          description: section.summary,
          source: 'Import review',
        }])
  const warningDetails: IssueDetail[] = [
    ...sortedReviewSections
      .filter(section => section.severity === 'warning')
      .flatMap(section => section.items.length > 0
        ? section.items.map((item, index) => ({
            id: `${section.key}-warning-${index}`,
            severity: 'warning' as const,
            title: section.title,
            description: item,
            source: section.summary || 'Import review',
          }))
        : [{
            id: `${section.key}-warning`,
            severity: 'warning' as const,
            title: section.title,
            description: section.summary,
            source: 'Import review',
          }]),
    ...warnings.map((warning, index) => ({
      id: `import-warning-${index}`,
      severity: 'warning' as const,
      title: warningsTitle.replace(/\s*\(\d+\)\s*$/, ''),
      description: warning,
      source: 'Import conversion',
    })),
  ]
  const protectedSessionLabel = protectedImportExpiryLabel(protectedImportSessionExpiresAt)
  const importDraftExpiry = importDraftExpiryStatus(importDraftExpiresAt)
  const importDraftExpired = importDraftExpiry?.expired === true

  return (
    <div className="script-import-review">
      <div className="script-import-review-header">
        <div>
          <h3>{title}</h3>
          {subtitle && <p>{subtitle}</p>}
        </div>
        <div className="script-import-review-actions">
          {onEdit && (
            <button
              className="btn btn-secondary"
              onClick={() => { void runAction('edit') }}
              disabled={importDraftExpired || parameterizationApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              title={importDraftExpired
                ? 'Re-run the import to obtain a fresh credential-preserving draft before editing.'
                : !credentialsValid
                  ? 'Enter valid secret reference names before editing this draft.'
                  : undefined}
            >
              {credentialAction === 'edit' ? 'Preparing editor...' : resolvedEditLabel}
            </button>
          )}
          {onSave && (
            <button
              className="btn btn-primary"
              onClick={() => { void runAction('save') }}
              disabled={importDraftExpired || saving || saveDisabled || parameterizationApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              title={importDraftExpired ? 'Re-run the import to obtain a fresh credential-preserving draft before saving.' : undefined}
            >
              {saving ? 'Saving...' : credentialAction === 'save' ? 'Preparing credentials...' : resolvedSaveLabel}
            </button>
          )}
        </div>
      </div>

      {protectedSessionLabel && (
        <div className="script-import-review-session" role="status">
          {protectedSessionLabel} Credential bindings and parameterization selections remain protected; other draft edits switch to the compatibility commit path.
        </div>
      )}

      {importDraftExpiry && (
        <div className="script-import-review-session" role={importDraftExpiry.expired ? 'alert' : 'status'}>
          {importDraftExpiry.label}
        </div>
      )}

      {hasUnresolvedSecretBindings && onSecretBindingsApply && (
        <SecretBindingReview
          ref={bindingReviewRef}
          requirements={secretBindingRequirements}
          sourceMigrationRequirements={sourceMigrationRequirements}
          preserveSourceSessionAfterApply={preserveSourceSessionAfterApply}
          reviewGeneration={reviewGeneration}
          projectId={projectId}
          sourceSecretSessionId={sourceSecretSessionId}
          sourceSecretSessionExpiresAt={sourceSecretSessionExpiresAt}
          sourceMigrationAllowed={sourceSecretMigrationAllowed}
          onSourceSessionSettled={onSourceSecretSessionSettled}
          onApply={applySecretBindings}
          onPreparedChange={setPreparedSecrets}
          disabled={importDraftExpired || parameterizationApplying || reviewBusy}
        />
      )}

      {hasUnresolvedSecretBindings && !onSecretBindingsApply && (
        <div className="secret-binding-review-unavailable" role="alert">
          This import contains redacted credentials, but this import source cannot prepare safe secret references.
        </div>
      )}

      {children}

      {(blockerDetails.length > 0 || warningDetails.length > 0) && (
        <div className="script-import-review-issue-summary" aria-label="Import issue summary">
          {blockerDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token danger issue-pill-button"
              aria-label={`View ${blockerDetails.length} import blocker ${blockerDetails.length === 1 ? 'detail' : 'details'}`}
              onClick={() => setIssueDialog({ title: 'Import blockers', issues: blockerDetails })}
            >
              {blockerDetails.length} blocker{blockerDetails.length === 1 ? '' : 's'}
            </button>
          )}
          {warningDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token warning issue-pill-button"
              aria-label={`View ${warningDetails.length} import warning ${warningDetails.length === 1 ? 'detail' : 'details'}`}
              onClick={() => setIssueDialog({ title: 'Import warnings', issues: warningDetails })}
            >
              {warningDetails.length} warning{warningDetails.length === 1 ? '' : 's'}
            </button>
          )}
        </div>
      )}

      {parameterization && onParameterizationApplied && (parameterization.suggestions?.length ?? 0) > 0 && (
        <ParameterizationReview
          parameterization={parameterization}
          script={script}
          scriptName={script.name}
          reviewGeneration={reviewGeneration}
          disabled={importDraftExpired || saving || reviewBusy || credentialAction !== null}
          onApplyingChange={handleParameterizationApplyingChange}
          onApplied={(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt) => {
            bindingReviewRef.current?.preserveEditsForNextReviewGeneration()
            onParameterizationApplied(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt)
          }}
        />
      )}

      {unsupportedElements.length > 0 && (
        <div className="script-import-review-unsupported">
          <h4>Unsupported Elements ({unsupportedElements.length})</h4>
          <ul>
            {unsupportedElements.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}
          </ul>
        </div>
      )}

      {sortedReviewSections.length > 0 && (
        <div className="script-import-review-sections">
          <h4>Import Review ({sortedReviewSections.length})</h4>
          <div className="script-import-review-section-list">
            {sortedReviewSections.map(section => (
              <details key={section.key} className={`script-import-review-section severity-${section.severity || 'info'}`} open={section.severity === 'blocker' || section.severity === 'warning'}>
                <summary>
                  <span>{section.title}</span>
                  <small>{section.severity}</small>
                </summary>
                {section.summary && <p>{section.summary}</p>}
                {section.items.length > 0 && (
                  <ul>
                    {section.items.map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}
                  </ul>
                )}
              </details>
            ))}
          </div>
        </div>
      )}

      {warnings.length > 0 && (
        <div className="script-import-review-warnings">
          <h4>{warningsTitle}</h4>
          <ul>
            {warnings.map((warning, index) => <li key={index}>{warning}</li>)}
          </ul>
        </div>
      )}

      {showPreview && previewPane}
      {showPreviewAside && previewAside && (
        <div className="script-import-review-preview-aside">
          {previewAside}
        </div>
      )}
      {issueDialog && (
        <IssueDetailsDialog
          title={issueDialog.title}
          issues={issueDialog.issues}
          onClose={() => setIssueDialog(null)}
        />
      )}
    </div>
  )
}
