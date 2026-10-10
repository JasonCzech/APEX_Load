import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useClustersApi } from '../hooks/api/useClustersApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { ClusterConnection, ClusterKind, ClusterTestResult } from '../types/cluster'
import { getErrorMessage } from '../utils/errorUtils'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import {
  AUTH_MODE_FOR_KIND,
  ClusterDraft,
  clusterDraftFrom,
  connectionBaseline,
  CLUSTER_FIELD_LABELS,
  type ClusterDraftField,
  connectionDraftErrors,
  connectionIsDirty,
  connectionPayload,
  describeCredentialSource,
  describeProbe,
  emptyClusterDraft,
  isRemoteKind,
} from './clusterConnectionModel'
import './parityPanels.css'
import LoadingState from './ux/LoadingState'
import EmptyState from './ux/EmptyState'
import { formatDateTime } from '../utils/dateTime'

interface Props {
  resourceId?: string
  canManage?: boolean
}

const KIND_LABELS: Record<ClusterKind, string> = {
  in_cluster: 'In-cluster (this controller)',
  generic: 'Kubernetes',
  aks: 'Azure Kubernetes Service',
  eks: 'Amazon EKS',
}

function healthPill(status: string | undefined): string {
  switch (status) {
    case 'ok':
      return 'pp-pill pp-pill-success'
    case 'degraded':
      return 'pp-pill pp-pill-warning'
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

/**
 * Registry of the Kubernetes and AKS control planes this controller can drive.
 *
 * Credentials are write-only end to end: the list/detail transport type carries
 * no secret field at all, so an edit form always starts with a blank credential
 * input and an omitted key means "keep the stored one".
 */
export default function ClustersCard({ canManage = false, resourceId = '' }: Props) {
  const api = useClustersApi()
  const [clusters, setClusters] = useState<ClusterConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [loadError, setLoadError] = useState(false)

  const [draft, setDraft] = useState<ClusterDraft | null>(null)
  const [draftBaseline, setDraftBaseline] = useState('')
  const [draftMode, setDraftMode] = useState<'create' | 'edit'>('create')
  // Fields the engineer has edited; a new connection's empty fields are not flagged until touched.
  const [touched, setTouched] = useState<Set<ClusterDraftField>>(new Set())
  const [deleteTarget, setDeleteTarget] = useState<ClusterConnection | null>(null)

  const [testingId, setTestingId] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<{ id: string; result: ClusterTestResult } | null>(null)
  const [testError, setTestError] = useState<string | null>(null)

  const dialogRef = useRef<HTMLDivElement | null>(null)
  const initialFocusRef = useRef<HTMLInputElement | null>(null)
  const dialogTitleId = useId()

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const response = await api.listClusters()
      setClusters(response.clusters ?? [])
      setError(null)
      setLoadError(false)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load cluster connections.')
      setLoadError(true)
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const draftErrors = draft ? connectionDraftErrors(draft, draftMode) : {}
  const invalidFields = Object.keys(draftErrors) as ClusterDraftField[]
  const draftError = invalidFields.length > 0
  // Each problem sits under its own field and describes it. The note stays
  // inside the field's label for layout, hidden from the label's name.
  const shownError = (field: ClusterDraftField) => (draftMode === 'edit' || touched.has(field)) ? draftErrors[field] : undefined
  const errorProps = (field: ClusterDraftField) => shownError(field) ? { 'aria-invalid': true as const, 'aria-describedby': `${dialogTitleId}-${field}-error` } : {}
  const errorNote = (field: ClusterDraftField) => shownError(field) ? <small id={`${dialogTitleId}-${field}-error`} className="pp-field-error" aria-hidden="true">{shownError(field)}</small> : null
  const draftDirty = draft ? connectionIsDirty(draft, draftBaseline) : false

  const closeDialog = useCallback(() => {
    setDraft(null)
    setDraftBaseline('')
  }, [])

  useModalDialog({ dialogRef, enabled: draft !== null, initialFocusRef, onClose: closeDialog })

  /**
   * Single mutation point for the draft. Clearing the stale test result here —
   * rather than at each call site — is what stops a newly-added field from
   * silently keeping an outdated "connection OK" on screen.
   */
  const updateDraft = useCallback((patch: Partial<ClusterDraft>) => {
    setDraft(current => (current ? { ...current, ...patch } : current))
    setTouched(current => new Set([...current, ...(Object.keys(patch) as ClusterDraftField[])]))
    setTestResult(null)
    setTestError(null)
  }, [])

  const openCreate = useCallback(() => {
    const next = emptyClusterDraft()
    setDraft(next)
    setDraftBaseline(connectionBaseline(next))
    setDraftMode('create')
    setTouched(new Set())
    setTestResult(null)
    setTestError(null)
  }, [])

  const openEdit = useCallback((connection: ClusterConnection) => {
    const next = clusterDraftFrom(connection)
    setDraft(next)
    setDraftBaseline(connectionBaseline(next))
    setDraftMode('edit')
    setTouched(new Set())
    setTestResult(null)
    setTestError(null)
  }, [])

  const saveRef = useRef<() => Promise<boolean>>(async () => false)
  const draftDirtyRef = useRef(false)
  draftDirtyRef.current = draftDirty

  // Registering the dirty draft globally is what makes navigating away from an
  // unsaved credential form prompt rather than silently discard it.
  const modalDraftGuard = useMemo<NavigationGuard>(
    () => ({
      id: 'cluster-connection-draft',
      label: 'Cluster connection editor',
      isDirty: () => draftDirtyRef.current,
      save: () => saveRef.current(),
      discard: closeDialog,
    }),
    [closeDialog],
  )
  useNavigationGuard(draft ? modalDraftGuard : null, draftDirty)

  const save = useCallback(async (): Promise<boolean> => {
    if (!draft || draftError) return false
    setBusy(true)
    try {
      const payload = connectionPayload(draft, draftMode)
      if (draftMode === 'create') {
        await api.createCluster(payload)
      } else {
        await api.updateCluster(draft.id, payload)
      }
      closeDialog()
      await load()
      setError(null)
      return true
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save the cluster connection.'))
      return false
    } finally {
      setBusy(false)
    }
  }, [api, closeDialog, draft, draftError, draftMode, load])
  saveRef.current = save

  const remove = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    try {
      await api.deleteCluster(deleteTarget.id)
      setDeleteTarget(null)
      await load()
      setError(null)
    } catch (err) {
      // A referenced connection returns 409 naming the pools that block it.
      setError(getErrorMessage(err, 'Failed to delete the cluster connection.'))
      setDeleteTarget(null)
    } finally {
      setBusy(false)
    }
  }, [api, deleteTarget, load])

  const testConnection = useCallback(
    async (connection: ClusterConnection) => {
      setTestingId(connection.id)
      setTestError(null)
      setTestResult(null)
      try {
        const result = await api.testCluster(connection.id)
        setTestResult({ id: connection.id, result })
        await load()
      } catch (err) {
        setTestError(getErrorMessage(err, 'The cluster connection test failed.'))
      } finally {
        setTestingId(null)
      }
    },
    [api, load],
  )

  const remote = draft ? isRemoteKind(draft.kind) : false

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {clusters.length} cluster connection{clusters.length === 1 ? '' : 's'}
        </span>
        {canManage && (
          <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={openCreate}>
            Connect Cluster
          </button>
        )}
      </div>

      {/* The cluster routes are system-admin only on READ as well as write, so a
          non-admin gets a 403 from listClusters rather than a read-only view.
          Say so plainly instead of leaving an unexplained empty list. */}
      {!canManage && (
        <div className="pp-muted">Cluster connections require the manage_system permission.</div>
      )}

      {error && (
        <div>
          <div className="pp-error" role="alert">{error}</div>
          {loadError && (
            <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
              Retry cluster data
            </button>
          )}
        </div>
      )}
      {testError && <div className="pp-error" role="alert">{testError}</div>}

      {loading ? (
        <LoadingState what="cluster connections" className="pp-loading" />
      ) : clusters.length === 0 ? (
        <EmptyState title="No clusters connected yet." hint="Connect a Kubernetes, AKS or EKS cluster to run load generators there and allocate their resources." />
      ) : (
        <div className="pp-list">
          {resourceId && <p role="status">Selected resource: {resourceId}. {!clusters.some(item => item.id === resourceId) && 'This resource was not found or is no longer accessible.'} <a href={window.location.hash.replace(/([?&])resourceId=[^&]*&?/, '$1').replace(/[?&]$/, '')}>Show all resources</a></p>}
          {clusters.filter(item => !resourceId || item.id === resourceId).map(connection => (
            <div className="pp-row" key={connection.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {connection.name}
                  <span className={healthPill(connection.last_check_status)}>
                    {connection.last_check_status || 'untested'}
                  </span>
                </div>
                <div className="pp-row-meta">
                  <span>{KIND_LABELS[connection.kind] ?? connection.kind}</span>
                  <span>namespace {connection.namespace}</span>
                  {/* Directory identifiers are administrator-only, so they are
                      shown in the edit form rather than the row summary. */}
                  {connection.api_server_url ? <span>{connection.api_server_url}</span> : null}
                  <span>{describeCredentialSource(connection)}</span>
                  {connection.last_checked_at ? (
                    <span>checked {formatDateTime(connection.last_checked_at, { seconds: true })}</span>
                  ) : null}
                </div>
                {testResult?.id === connection.id && (
                  <div
                    data-testid="cluster-test-result"
                    className={describeProbe(testResult.result).tone === 'danger' ? 'pp-error' : 'pp-muted'}
                  >
                    {describeProbe(testResult.result).text}
                  </div>
                )}
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    disabled={busy || testingId === connection.id}
                    onClick={() => { void testConnection(connection) }}
                  >
                    {testingId === connection.id ? 'Testing…' : 'Test connection'}
                  </button>
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => openEdit(connection)}>
                    Edit
                  </button>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(connection)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {draft && (
        <DialogLayer><div className="pp-modal-overlay">
          <div className="pp-modal" role="dialog" aria-modal="true" aria-labelledby={dialogTitleId} ref={dialogRef}>
            <div className="pp-modal-title" id={dialogTitleId}>
              {draftMode === 'create' ? 'Connect Cluster' : `Edit ${draft.name}`}
            </div>

            <label className="pp-field">
              <span>Name</span>
              <input ref={initialFocusRef} value={draft.name} {...errorProps('name')} onChange={e => updateDraft({ name: e.target.value })} />{errorNote('name')}
            </label>

            <div className="pp-field-row">
              <label className="pp-field">
                <span>Provider</span>
                <select
                  {...errorProps('kind')} value={draft.kind}
                  onChange={e => {
                    const kind = e.target.value as ClusterKind
                    updateDraft({
                      kind,
                      authMode: AUTH_MODE_FOR_KIND[kind],
                      // A cluster reached over the public EKS endpoint presents
                      // a publicly-trusted certificate, so system roots is the
                      // working default rather than a CA paste.
                      ...(kind === 'eks' ? { tlsMode: 'system_roots' as const } : {}),
                    })
                  }}
                >
                  <option value="aks">Azure Kubernetes Service</option>
                  <option value="eks">Amazon EKS</option>
                  <option value="generic">Kubernetes</option>
                  <option value="in_cluster">In-cluster (this controller)</option>
                </select>{errorNote('kind')}
              </label>
              <label className="pp-field">
                <span>Namespace</span>
                <input value={draft.namespace} {...errorProps('namespace')} onChange={e => updateDraft({ namespace: e.target.value })} />{errorNote('namespace')}
              </label>
            </div>

            {remote && (
              <>
                <label className="pp-field">
                  <span>API server URL</span>
                  <input
                    value={draft.apiServerUrl}
                    placeholder="https://my-cluster.hcp.eastus.azmk8s.io"
                    {...errorProps('apiServerUrl')} onChange={e => updateDraft({ apiServerUrl: e.target.value })}
                  />{errorNote('apiServerUrl')}
                </label>

                <label className="pp-field">
                  <span>TLS trust</span>
                  <select value={draft.tlsMode} onChange={e => updateDraft({ tlsMode: e.target.value as ClusterDraft['tlsMode'] })}>
                    <option value="custom_ca">Cluster CA certificate</option>
                    <option value="system_roots">Host system roots</option>
                  </select>
                </label>

                {draft.tlsMode === 'custom_ca' && (
                  <label className="pp-field">
                    <span>CA certificate (PEM)</span>
                    <textarea
                      rows={4}
                      value={draft.caCertificatePem}
                      placeholder="-----BEGIN CERTIFICATE-----"
                      {...errorProps('caCertificatePem')} onChange={e => updateDraft({ caCertificatePem: e.target.value })}
                    />{errorNote('caCertificatePem')}
                  </label>
                )}

                {draft.kind === 'aks' && (
                  <div className="pp-field-row">
                    <label className="pp-field">
                      <span>Azure tenant ID</span>
                      <input value={draft.azureTenantId} {...errorProps('azureTenantId')} onChange={e => updateDraft({ azureTenantId: e.target.value })} />{errorNote('azureTenantId')}
                    </label>
                    <label className="pp-field">
                      <span>Azure client ID</span>
                      <input value={draft.azureClientId} {...errorProps('azureClientId')} onChange={e => updateDraft({ azureClientId: e.target.value })} />{errorNote('azureClientId')}
                    </label>
                  </div>
                )}

                {draft.kind === 'eks' && (
                  <>
                    <div className="pp-field-row">
                      <label className="pp-field">
                        <span>AWS region</span>
                        <input
                          value={draft.awsRegion}
                          placeholder="us-east-1"
                          {...errorProps('awsRegion')} onChange={e => updateDraft({ awsRegion: e.target.value })}
                        />{errorNote('awsRegion')}
                      </label>
                      <label className="pp-field">
                        <span>EKS cluster name</span>
                        <input
                          value={draft.awsClusterName}
                          placeholder="prod-perf"
                          {...errorProps('awsClusterName')} onChange={e => updateDraft({ awsClusterName: e.target.value })}
                        />{errorNote('awsClusterName')}
                      </label>
                    </div>
                    <label className="pp-field">
                      <span>AWS credentials</span>
                      <select
                        {...errorProps('awsCredentialMode')} value={draft.awsCredentialMode}
                        onChange={e =>
                          updateDraft({ awsCredentialMode: e.target.value as ClusterDraft['awsCredentialMode'] })
                        }
                      >
                        <option value="ambient">Workload identity (IRSA / EKS Pod Identity)</option>
                        <option value="assume_role">Assume an IAM role</option>
                        <option value="static_keys">Static access key</option>
                      </select>{errorNote('awsCredentialMode')}
                    </label>
                    {draft.awsCredentialMode === 'ambient' && (
                      <div className="pp-muted">
                        The controller authenticates with its own IAM role. Nothing is stored here.
                      </div>
                    )}
                    {draft.awsCredentialMode === 'assume_role' && (
                      <>
                        <label className="pp-field">
                          <span>Role ARN</span>
                          <input
                            value={draft.awsRoleArn}
                            placeholder="arn:aws:iam::123456789012:role/apexload-eks"
                            {...errorProps('awsRoleArn')} onChange={e => updateDraft({ awsRoleArn: e.target.value })}
                          />{errorNote('awsRoleArn')}
                        </label>
                        <label className="pp-field">
                          <span>
                            {draft.hasAwsExternalId ? 'External ID (stored)' : 'External ID (optional)'}
                          </span>
                          <input
                            type="password"
                            autoComplete="new-password"
                            value={draft.awsExternalId}
                            onChange={e => updateDraft({ awsExternalId: e.target.value })}
                          />
                        </label>
                      </>
                    )}
                    {draft.awsCredentialMode === 'static_keys' && (
                      <div className="pp-field-row">
                        <label className="pp-field">
                          <span>Access key ID</span>
                          <input
                            value={draft.awsAccessKeyId}
                            {...errorProps('awsAccessKeyId')} onChange={e => updateDraft({ awsAccessKeyId: e.target.value })}
                          />{errorNote('awsAccessKeyId')}
                        </label>
                        <label className="pp-field">
                          <span>
                            {draft.hasStoredCredential ? 'Secret access key (stored)' : 'Secret access key'}
                          </span>
                          <input
                            type="password"
                            autoComplete="new-password"
                            value={draft.awsSecretAccessKey}
                            {...errorProps('awsSecretAccessKey')} onChange={e => updateDraft({ awsSecretAccessKey: e.target.value })}
                          />{errorNote('awsSecretAccessKey')}
                        </label>
                      </div>
                    )}
                    <label className="pp-field">
                      <span>STS endpoint (optional)</span>
                      <input
                        value={draft.awsStsEndpoint}
                        placeholder="https://sts.us-east-1.amazonaws.com"
                        {...errorProps('awsStsEndpoint')} onChange={e => updateDraft({ awsStsEndpoint: e.target.value })}
                      />{errorNote('awsStsEndpoint')}
                    </label>
                  </>
                )}

                {draft.kind === 'eks' ? null : draft.kind === 'aks' ? (
                  <label className="pp-field">
                    <span>{draft.hasStoredCredential ? 'Azure client secret (stored)' : 'Azure client secret'}</span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={draft.clientSecret}
                      {...errorProps('clientSecret')} onChange={e => updateDraft({ clientSecret: e.target.value })}
                    />{errorNote('clientSecret')}
                  </label>
                ) : (
                  <label className="pp-field">
                    <span>{draft.hasStoredCredential ? 'Service-account token (stored)' : 'Service-account token'}</span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={draft.staticToken}
                      {...errorProps('staticToken')} onChange={e => updateDraft({ staticToken: e.target.value })}
                    />{errorNote('staticToken')}
                  </label>
                )}
                {draftMode === 'edit' && draft.hasStoredCredential && (
                  <div className="pp-muted">Leave blank to keep the stored credential.</div>
                )}

                <label className="pp-field">
                  <span>Or read the credential from an environment variable</span>
                  <input
                    value={draft.clientSecretEnvVar}
                    placeholder="APEXLOAD_AKS_CLIENT_SECRET"
                    onChange={e => updateDraft({ clientSecretEnvVar: e.target.value })}
                  />
                </label>
              </>
            )}

            <div className="pp-field-row">
              <label className="pp-field">
                <span>Generator StatefulSet</span>
                <input
                  value={draft.generatorStatefulSet}
                  placeholder="apexload-generator"
                  onChange={e => updateDraft({ generatorStatefulSet: e.target.value })}
                />
              </label>
              <label className="pp-field">
                <span>Generator container</span>
                <input
                  value={draft.generatorContainer}
                  placeholder="generator"
                  onChange={e => updateDraft({ generatorContainer: e.target.value })}
                />
              </label>
            </div>
            <label className="pp-field">
              <span>Generator selector</span>
              <input
                value={draft.generatorSelector}
                placeholder="app=apexload-generator"
                {...errorProps('generatorSelector')} onChange={e => updateDraft({ generatorSelector: e.target.value })}
              />{errorNote('generatorSelector')}
            </label>

            {draftError && <div className="pp-muted" aria-live="polite">To {draftMode === 'create' ? 'connect' : 'save'}, complete or correct: {invalidFields.map(field => CLUSTER_FIELD_LABELS[field] || field).join(', ')}.</div>}
            <div className="pp-muted">Save the connection first — the connection test uses the stored configuration.</div>

            <div className="pp-modal-actions">
              <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={closeDialog}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary btn-sm" disabled={!!draftError || busy} onClick={() => { void save() }}>
                {draftMode === 'create' ? 'Connect' : 'Save'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete cluster connection"
          description={`This removes the connection “${deleteTarget.name}”. Fleet pools still bound to it must be repointed first.`}
          target={deleteTarget.name}
          targetLabel="Cluster"
          confirmLabel="Delete cluster"
          error={error}
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => { void remove() }}
        />
      )}
    </div>
  )
}
