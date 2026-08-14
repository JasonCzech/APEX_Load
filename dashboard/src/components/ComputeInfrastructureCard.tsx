import { useCallback, useEffect, useMemo, useState } from 'react'

import { useComputeApi } from '../hooks/api/useComputeApi'
import type { CloudConnection, CloudConnectionInput, ExecutionTarget, ExecutionTargetInput, ExecutionTargetTestResult } from '../types/compute'
import { getErrorMessage } from '../utils/errorUtils'
import { cloudConnectionError, executionTargetError, parseComputeRegistrationManifest } from './computeInfrastructureModel'
import './parityPanels.css'

interface Props { canManage?: boolean }

const emptyConnection = (): CloudConnectionInput => ({
  name: '', provider: 'azure', auth_mode: 'azure_identity', azure_cloud: 'public',
  azure_subscription_id: '', azure_resource_group: '', status: 'active',
})

const emptyTarget = (): ExecutionTargetInput => ({
  name: '', kind: 'azure_container_apps', cloud_connection_id: '', region: '', connectivity_mode: 'reverse',
  standing_owner: 'apex', burst_owner: 'disabled', archive_mode: 'controller_pull', status: 'active',
  azure_container_apps: { resource_group: '', environment_name: '', warm_container_app: '', burst_job: '', expected_principal_id: '' },
  runtime_profiles: [],
})

function statusPill(status?: string) {
  if (status === 'ok' || status === 'active') return 'pp-pill pp-pill-success'
  if (status === 'degraded') return 'pp-pill pp-pill-warning'
  if (status === 'failed' || status === 'error') return 'pp-pill pp-pill-danger'
  return 'pp-pill pp-pill-muted'
}

function splitList(value: string): string[] {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

export default function ComputeInfrastructureCard({ canManage = false }: Props) {
  const api = useComputeApi()
  const [connections, setConnections] = useState<CloudConnection[]>([])
  const [targets, setTargets] = useState<ExecutionTarget[]>([])
  const [connectionDraft, setConnectionDraft] = useState<CloudConnectionInput | null>(null)
  const [targetDraft, setTargetDraft] = useState<ExecutionTargetInput | null>(null)
  const [manifest, setManifest] = useState('')
  const [showManifest, setShowManifest] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [oneTimeTokens, setOneTimeTokens] = useState<Array<{ target: string; token: string }>>([])
  const [testResult, setTestResult] = useState<{ target: string; result: ExecutionTargetTestResult } | null>(null)

  const load = useCallback(async () => {
    try {
      const [connectionResponse, targetResponse] = await Promise.all([api.listCloudConnections(), api.listExecutionTargets()])
      setConnections(connectionResponse.cloud_connections || [])
      setTargets(targetResponse.execution_targets || [])
      setError(null)
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load compute infrastructure.'))
    }
  }, [api])

  useEffect(() => { void load() }, [load])

  const availableConnections = useMemo(() => {
    if (!targetDraft) return connections
    const provider = targetDraft.kind === 'azure_container_apps' ? 'azure' : 'aws'
    return connections.filter(connection => connection.provider === provider)
  }, [connections, targetDraft])

  const saveConnection = useCallback(async () => {
    if (!connectionDraft) return
    const validation = cloudConnectionError(connectionDraft)
    if (validation) { setError(validation); return }
    setBusy(true)
    try {
      await api.createCloudConnection(connectionDraft)
      setConnectionDraft(null)
      await load()
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to create cloud connection.'))
    } finally { setBusy(false) }
  }, [api, connectionDraft, load])

  const saveTarget = useCallback(async () => {
    if (!targetDraft) return
    const validation = executionTargetError(targetDraft)
    if (validation) { setError(validation); return }
    setBusy(true)
    try {
      const created = await api.createExecutionTarget(targetDraft)
      if (created.enrollment_token) setOneTimeTokens([{ target: created.name, token: created.enrollment_token }])
      setTargetDraft(null)
      await load()
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to create execution target.'))
    } finally { setBusy(false) }
  }, [api, load, targetDraft])

  const importManifest = useCallback(async () => {
    setBusy(true)
    try {
      const parsed = parseComputeRegistrationManifest(manifest)
	  const existingConnection = parsed.cloud_connection.id
	    ? connections.find(item => item.id === parsed.cloud_connection.id)
	    : undefined
	  const connection = existingConnection
	    ? await api.updateCloudConnection(existingConnection.id, { ...parsed.cloud_connection, updated_at: existingConnection.updated_at })
	    : await api.createCloudConnection(parsed.cloud_connection)
      const returnedTokens: Array<{ target: string; token: string }> = []
      for (const item of parsed.execution_targets) {
        const target = { ...item, cloud_connection_id: connection.id } as ExecutionTargetInput
        const validation = executionTargetError(target)
        if (validation) throw new Error(`${target.name || 'Execution target'}: ${validation}`)
	    const existingTarget = target.id ? targets.find(candidate => candidate.id === target.id) : undefined
	    const saved = existingTarget
	      ? await api.updateExecutionTarget(existingTarget.id, { ...target, updated_at: existingTarget.updated_at })
	      : await api.createExecutionTarget(target)
	    if (saved.enrollment_token) returnedTokens.push({ target: saved.name, token: saved.enrollment_token })
      }
      setOneTimeTokens(returnedTokens)
      setManifest('')
      setShowManifest(false)
      await load()
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to import the Terraform registration manifest.'))
    } finally { setBusy(false) }
  }, [api, connections, load, manifest, targets])

  const testTarget = useCallback(async (target: ExecutionTarget) => {
    setBusy(true)
    try {
      const result = await api.testExecutionTarget(target.id)
      setTestResult({ target: target.id, result })
      await load()
    } catch (err) { setError(getErrorMessage(err, 'Execution-target test failed.')) } finally { setBusy(false) }
  }, [api, load])

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <div>
          <div className="pp-section-header">Infrastructure &amp; Fleet</div>
          <div className="pp-muted">Separate reusable cloud identities from runtime placement. Terraform creates infrastructure; APEX Load registers and manages capacity.</div>
        </div>
        {canManage ? <div className="pp-row-actions">
          <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={() => setConnectionDraft(emptyConnection())}>Cloud Connection</button>
          <button type="button" className="btn btn-primary btn-sm" disabled={busy || connections.length === 0} onClick={() => setTargetDraft(emptyTarget())}>Execution Target</button>
          <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => setShowManifest(value => !value)}>Import Terraform Manifest</button>
        </div> : null}
      </div>

      {!canManage ? <div className="pp-muted">Compute infrastructure requires the manage_system permission.</div> : null}
      {error ? <div className="pp-error" role="alert">{error}</div> : null}
      {oneTimeTokens.length > 0 ? <div className="pp-error" role="status">
        <strong>One-time reverse enrollment token{oneTimeTokens.length === 1 ? '' : 's'}</strong>
        {oneTimeTokens.map(item => <div key={item.target}>
          <div>{item.target}</div>
          <div className="cell-mono" data-testid="one-time-enrollment-token">{item.token}</div>
        </div>)}
        <div>Store each token as <code>APEXLOAD_GENERATOR_GATEWAY_TOKEN</code> in its pre-provisioned Container App/Job or ECS task secret. They will not be shown again.</div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => setOneTimeTokens([])}>I stored them</button>
      </div> : null}

      {showManifest ? <div className="pp-section">
        <label className="pp-field"><span>Credential-free Terraform registration manifest</span>
          <textarea rows={8} value={manifest} onChange={event => setManifest(event.target.value)} placeholder='{"cloud_connection": {...}, "execution_targets": [...]}' />
        </label>
        <div className="pp-muted">The manifest contains resource identifiers and ambient/workload-identity settings, never secret values. Reverse targets return a one-time token after import.</div>
        <div className="pp-row-actions"><button type="button" className="btn btn-primary btn-sm" disabled={busy || !manifest.trim()} onClick={() => void importManifest()}>Import</button></div>
      </div> : null}

      <div className="pp-section">
        <div className="pp-section-header"><span>Cloud Connections</span><span className="pp-muted">{connections.length}</span></div>
        {connections.length === 0 ? <div className="pp-empty pp-empty-compact">Register an Azure subscription or AWS account identity first.</div> : <div className="pp-list">{connections.map(connection =>
          <div className="pp-row" key={connection.id}><div className="pp-row-main"><div className="pp-row-title">{connection.name}<span className={statusPill(connection.status)}>{connection.status}</span></div>
            <div className="pp-row-meta"><span>{connection.provider}</span><span>{connection.auth_mode}</span><span>{connection.azure_subscription_id || connection.aws_account_id || 'ambient scope'}</span><span>{connection.aws_region || connection.azure_resource_group || 'provider default scope'}</span></div>
          </div></div>)}</div>}
      </div>

      <div className="pp-section">
        <div className="pp-section-header"><span>Execution Targets</span><span className="pp-muted">{targets.length}</span></div>
        {targets.length === 0 ? <div className="pp-empty pp-empty-compact">No PaaS execution targets registered.</div> : <div className="pp-list">{targets.map(target => {
          const tested = testResult?.target === target.id ? testResult.result : null
          return <div className="pp-row" key={target.id}><div className="pp-row-main">
            <div className="pp-row-title">{target.name}<span className={statusPill(target.last_check_status || target.status)}>{target.last_check_status || target.status}</span></div>
            <div className="pp-row-meta"><span>{target.kind}</span><span>{target.connectivity_mode} connectivity</span><span>{target.capabilities.can_scale ? 'warm scale' : 'no warm scale'}</span><span>{target.capabilities.can_launch ? 'burst launch' : 'no burst launch'}</span>{target.enrollment_token_expires_at ? <span>token expires {new Date(target.enrollment_token_expires_at).toLocaleDateString()}</span> : null}</div>
            {tested ? <div className="pp-list">{tested.probes.map(probe => <div key={probe.probe} className={probe.ok ? 'pp-muted' : 'pp-error'}>{probe.probe}: {probe.message || (probe.ok ? 'ready' : 'failed')}</div>)}</div> : null}
          </div>{canManage ? <div className="pp-row-actions"><button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void testTarget(target)}>Test</button></div> : null}</div>
        })}</div>}
      </div>

      {connectionDraft ? <div className="pp-section" aria-label="Cloud connection editor">
        <div className="pp-section-header"><span>New Cloud Connection</span></div>
        <div className="pp-field-row"><label className="pp-field"><span>Name</span><input value={connectionDraft.name} onChange={event => setConnectionDraft({ ...connectionDraft, name: event.target.value })} /></label>
          <label className="pp-field"><span>Provider</span><select value={connectionDraft.provider} onChange={event => { const provider = event.target.value as 'azure' | 'aws'; setConnectionDraft(provider === 'azure' ? emptyConnection() : { name: '', provider: 'aws', auth_mode: 'aws_ambient', aws_partition: 'aws', aws_region: '', status: 'active' }) }}><option value="azure">Azure</option><option value="aws">AWS</option></select></label></div>
        {connectionDraft.provider === 'azure' ? <>
          <div className="pp-field-row"><label className="pp-field"><span>Subscription ID</span><input value={connectionDraft.azure_subscription_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, azure_subscription_id: event.target.value })} /></label><label className="pp-field"><span>Default resource group</span><input value={connectionDraft.azure_resource_group || ''} onChange={event => setConnectionDraft({ ...connectionDraft, azure_resource_group: event.target.value })} /></label></div>
          <label className="pp-field"><span>Authentication</span><select value={connectionDraft.auth_mode} onChange={event => setConnectionDraft({ ...connectionDraft, auth_mode: event.target.value as CloudConnectionInput['auth_mode'] })}><option value="azure_identity">Managed/workload identity</option><option value="azure_service_principal">Service principal</option></select></label>
          {connectionDraft.auth_mode === 'azure_service_principal' ? <div className="pp-field-row"><label className="pp-field"><span>Tenant ID</span><input value={connectionDraft.azure_tenant_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, azure_tenant_id: event.target.value })} /></label><label className="pp-field"><span>Client ID</span><input value={connectionDraft.azure_client_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, azure_client_id: event.target.value })} /></label><label className="pp-field"><span>Client secret (write-only)</span><input type="password" value={connectionDraft.client_secret || ''} onChange={event => setConnectionDraft({ ...connectionDraft, client_secret: event.target.value })} /></label></div> : null}
          {connectionDraft.auth_mode === 'azure_service_principal' ? <label className="pp-field"><span>Credential environment variable (alternative to client secret)</span><input value={connectionDraft.credential_env_var || ''} onChange={event => setConnectionDraft({ ...connectionDraft, credential_env_var: event.target.value })} /></label> : null}
        </> : <>
          <div className="pp-field-row"><label className="pp-field"><span>AWS region</span><input value={connectionDraft.aws_region || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_region: event.target.value })} /></label><label className="pp-field"><span>Account ID</span><input value={connectionDraft.aws_account_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_account_id: event.target.value })} /></label></div>
          <label className="pp-field"><span>Authentication</span><select value={connectionDraft.auth_mode} onChange={event => { const authMode = event.target.value as CloudConnectionInput['auth_mode']; setConnectionDraft({ ...connectionDraft, auth_mode: authMode, credential_env_var: authMode === 'aws_static_keys' ? connectionDraft.credential_env_var : '' }) }}><option value="aws_ambient">Task/workload role</option><option value="aws_assume_role">Assume role</option><option value="aws_static_keys">Static keys</option></select></label>
          {connectionDraft.auth_mode === 'aws_assume_role' ? <label className="pp-field"><span>Role ARN</span><input value={connectionDraft.aws_role_arn || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_role_arn: event.target.value })} /></label> : null}
          {connectionDraft.auth_mode === 'aws_assume_role' ? <label className="pp-field"><span>External ID (write-only, optional)</span><input type="password" value={connectionDraft.aws_external_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_external_id: event.target.value })} /></label> : null}
          {connectionDraft.auth_mode === 'aws_static_keys' ? <div className="pp-field-row"><label className="pp-field"><span>Access key ID</span><input value={connectionDraft.aws_access_key_id || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_access_key_id: event.target.value })} /></label><label className="pp-field"><span>Secret access key (write-only)</span><input type="password" value={connectionDraft.aws_secret_access_key || ''} onChange={event => setConnectionDraft({ ...connectionDraft, aws_secret_access_key: event.target.value })} /></label></div> : null}
          {connectionDraft.auth_mode === 'aws_static_keys' ? <label className="pp-field"><span>Credential environment variable (alternative to secret access key)</span><input value={connectionDraft.credential_env_var || ''} onChange={event => setConnectionDraft({ ...connectionDraft, credential_env_var: event.target.value })} /></label> : null}
        </>}
        <div className="pp-row-actions"><button type="button" className="btn btn-secondary btn-sm" onClick={() => setConnectionDraft(null)}>Cancel</button><button type="button" className="btn btn-primary btn-sm" disabled={busy || Boolean(cloudConnectionError(connectionDraft))} onClick={() => void saveConnection()}>Save Connection</button></div>
      </div> : null}

      {targetDraft ? <div className="pp-section" aria-label="Execution target editor">
        <div className="pp-section-header"><span>New Execution Target</span></div>
        <div className="pp-field-row"><label className="pp-field"><span>Name</span><input value={targetDraft.name} onChange={event => setTargetDraft({ ...targetDraft, name: event.target.value })} /></label>
          <label className="pp-field"><span>Runtime</span><select value={targetDraft.kind} onChange={event => { const kind = event.target.value; setTargetDraft(kind === 'azure_container_apps' ? emptyTarget() : { ...emptyTarget(), kind: 'aws_ecs_fargate', connectivity_mode: 'direct', azure_container_apps: undefined, aws_ecs_fargate: { cluster: '', warm_service: '', burst_task_definition: '', subnets: [], security_groups: [], assign_public_ip: 'disabled', expected_task_role_arn: '' } }) }}><option value="azure_container_apps">Azure Container Apps + Jobs</option><option value="aws_ecs_fargate">AWS ECS Fargate + RunTask</option></select></label>
          <label className="pp-field"><span>Cloud connection</span><select value={targetDraft.cloud_connection_id || ''} onChange={event => setTargetDraft({ ...targetDraft, cloud_connection_id: event.target.value })}><option value="">Select…</option>{availableConnections.map(connection => <option key={connection.id} value={connection.id}>{connection.name}</option>)}</select></label></div>
        <div className="pp-field-row"><label className="pp-field"><span>Connectivity</span><select value={targetDraft.connectivity_mode} onChange={event => setTargetDraft({ ...targetDraft, connectivity_mode: event.target.value as 'direct' | 'reverse' })}><option value="reverse">Reverse (outbound-only)</option><option value="direct" disabled={targetDraft.kind !== 'aws_ecs_fargate'}>{targetDraft.kind === 'aws_ecs_fargate' ? 'Direct private IP' : 'Direct unavailable — Container Apps replicas have no stable address'}</option></select></label>
          <label className="pp-field"><span>Warm owner</span><select value={targetDraft.standing_owner} onChange={event => setTargetDraft({ ...targetDraft, standing_owner: event.target.value as 'apex' | 'external' })}><option value="apex">APEX Load</option><option value="external">External autoscaler</option></select></label>
          <label className="pp-field"><span>Archive mode</span><select value={targetDraft.archive_mode} disabled><option value="controller_pull">Controller pull (archive-fenced)</option></select></label></div>
        {targetDraft.kind === 'azure_container_apps' && targetDraft.azure_container_apps ? <>
          <div className="pp-field-row"><label className="pp-field"><span>Resource group</span><input value={targetDraft.azure_container_apps.resource_group} onChange={event => setTargetDraft({ ...targetDraft, azure_container_apps: { ...targetDraft.azure_container_apps!, resource_group: event.target.value } })} /></label><label className="pp-field"><span>Container Apps environment</span><input value={targetDraft.azure_container_apps.environment_name} onChange={event => setTargetDraft({ ...targetDraft, azure_container_apps: { ...targetDraft.azure_container_apps!, environment_name: event.target.value } })} /></label></div>
          <div className="pp-field-row"><label className="pp-field"><span>Warm Container App</span><input value={targetDraft.azure_container_apps.warm_container_app || ''} onChange={event => setTargetDraft({ ...targetDraft, azure_container_apps: { ...targetDraft.azure_container_apps!, warm_container_app: event.target.value } })} /></label><label className="pp-field"><span>Burst Job</span><input value={targetDraft.azure_container_apps.burst_job || ''} onChange={event => setTargetDraft({ ...targetDraft, burst_owner: event.target.value ? 'apex' : 'disabled', azure_container_apps: { ...targetDraft.azure_container_apps!, burst_job: event.target.value } })} /></label></div>
          <label className="pp-field"><span>Expected managed-identity principal ID</span><input value={targetDraft.azure_container_apps.expected_principal_id || ''} onChange={event => setTargetDraft({ ...targetDraft, azure_container_apps: { ...targetDraft.azure_container_apps!, expected_principal_id: event.target.value } })} /></label>
        </> : null}
        {targetDraft.kind === 'aws_ecs_fargate' && targetDraft.aws_ecs_fargate ? <>
          <div className="pp-field-row"><label className="pp-field"><span>ECS cluster</span><input value={targetDraft.aws_ecs_fargate.cluster} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, cluster: event.target.value } })} /></label><label className="pp-field"><span>Warm service</span><input value={targetDraft.aws_ecs_fargate.warm_service || ''} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, warm_service: event.target.value } })} /></label><label className="pp-field"><span>Burst task definition</span><input value={targetDraft.aws_ecs_fargate.burst_task_definition || ''} onChange={event => setTargetDraft({ ...targetDraft, burst_owner: event.target.value ? 'apex' : 'disabled', aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, burst_task_definition: event.target.value } })} /></label></div>
          <div className="pp-field-row"><label className="pp-field"><span>Subnets (comma-separated)</span><input value={(targetDraft.aws_ecs_fargate.subnets || []).join(', ')} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, subnets: splitList(event.target.value) } })} /></label><label className="pp-field"><span>Security groups</span><input value={(targetDraft.aws_ecs_fargate.security_groups || []).join(', ')} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, security_groups: splitList(event.target.value) } })} /></label><label className="pp-field"><span>Public IP assignment</span><select value={targetDraft.aws_ecs_fargate.assign_public_ip || 'disabled'} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, assign_public_ip: event.target.value as 'enabled' | 'disabled' } })}><option value="disabled">Disabled (private subnets)</option><option value="enabled">Enabled</option></select></label></div>
          <label className="pp-field"><span>Expected task role ARN</span><input value={targetDraft.aws_ecs_fargate.expected_task_role_arn || ''} onChange={event => setTargetDraft({ ...targetDraft, aws_ecs_fargate: { ...targetDraft.aws_ecs_fargate!, expected_task_role_arn: event.target.value } })} /></label>
        </> : null}
        <div className="pp-muted">Unsupported combinations are disabled by provider capabilities after registration. Connection tests verify identity, provider API access, runtime sidecars, and the registered template.</div>
        <div className="pp-row-actions"><button type="button" className="btn btn-secondary btn-sm" onClick={() => setTargetDraft(null)}>Cancel</button><button type="button" className="btn btn-primary btn-sm" disabled={busy || Boolean(executionTargetError(targetDraft))} onClick={() => void saveTarget()}>Register Target</button></div>
      </div> : null}
    </div>
  )
}
