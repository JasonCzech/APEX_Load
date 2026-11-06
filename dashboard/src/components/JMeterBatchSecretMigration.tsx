import { useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { JMeterImportResult, SecretBindingRequirement } from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'
import { batchSecretKey } from './jmeterImportUtils'
import { isValidSecretBindingName } from './secretBindingUtils'
import { formatSourceSecretSessionFailure, getSourceSecretSessionFailureMetadata, sourceSecretSessionIsExpired } from './sourceSecretSession'

/** A credential value the batch review migrated into Project Secrets. */
export interface MigratedBatchSecret { script_index: number; path: string; secret_name: string }

// The batch repair that binds the credential at a requirement's path.
const BIND_REPAIR_BY_PATH: Record<string, { type: string; label: string }> = {
  '/config/eventhubs/connection_string': { type: 'bind_eventhubs_credentials', label: 'Bind Event Hubs connection string' },
  '/config/kafka/oauth/client_secret_ref': { type: 'bind_kafka_credentials', label: 'Bind Kafka credentials' },
  '/config/kafka/oauth/client_id': { type: 'bind_kafka_credentials', label: 'Bind Kafka credentials' },
}

// The names a failed migrate response lists under "migrated": secrets it
// created before the failure.
function createdSecretNames(cause: unknown): Set<string> {
  const migrated = (cause as { payload?: { migrated?: unknown } } | null)?.payload?.migrated
  return new Set(Array.isArray(migrated) ? migrated.map(item => (item as { name?: unknown } | null)?.name).filter((name): name is string => typeof name === 'string') : [])
}

const toMigrated = (selections: { requirement: SecretBindingRequirement; secret_name: string }[]): MigratedBatchSecret[] =>
  selections.map(selection => ({ script_index: selection.requirement.script_index ?? 0, path: selection.requirement.path, secret_name: selection.secret_name }))

// The migrate endpoint takes 1-256 selections.
const MAX_MIGRATION_SELECTIONS = 256

interface Props {
  result: JMeterImportResult
  projectId: string
  /** Secrets this review already migrated, for the binding step that follows. */
  migrated: readonly MigratedBatchSecret[]
  disabled?: boolean
  onBusyChange: (busy: boolean) => void
  /** The values were written; the server consumed the session. A failure message means a partial migration wrote only these. */
  onMigrated: (sessionId: string, migrated: MigratedBatchSecret[], failureMessage?: string) => void
  /** The session can no longer migrate: consumed by a failed attempt, or its state is unknown. */
  onSessionEnded: (sessionId: string, consumed: boolean, message: string) => void
  /** Opens the batch repair that binds one script's credential. */
  onBind: (type: string, group: number) => void
}

/**
 * Batch mode has no Credentials tab: the batch repairs bind each script's
 * credential references. This moves the plan's recorded credential values
 * into Project Secrets (only that; it binds nothing), so the bind repairs
 * that follow can reference real secrets. Names start as the importer's
 * suggestion, the same name the bind repair fills in, and a migrated name
 * replaces that suggestion there.
 */
export default function JMeterBatchSecretMigration({ result, projectId, migrated, disabled, onBusyChange, onMigrated, onSessionEnded, onBind }: Props) {
  const api = useApi()
  const [names, setNames] = useState<Record<string, string>>({})
  const [skipped, setSkipped] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const requirements = result.secret_binding_requirements || []
  const sessionId = result.source_secret_session_id || ''
  const expired = sourceSecretSessionIsExpired(result.source_secret_session_expires_at)
  const offered = Boolean(sessionId) && result.source_secret_migration_allowed === true
  const available = offered ? requirements.filter(requirement => requirement.source_value_available === true) : []
  // Migrated secrets still to bind (their requirement is still reported),
  // one entry per bind repair and script: Kafka's client ID and secret bind
  // together.
  const toBind: { key: string; scriptIndex: number; names: string[]; repair?: { type: string; label: string } }[] = []
  for (const secret of migrated) {
    if (!requirements.some(requirement => (requirement.script_index ?? 0) === secret.script_index && requirement.path === secret.path)) continue
    const repair = BIND_REPAIR_BY_PATH[secret.path]
    const key = repair ? `${repair.type}\u0000${secret.script_index}` : batchSecretKey(secret.script_index, secret.path)
    const entry = toBind.find(item => item.key === key)
    if (entry) entry.names.push(secret.secret_name)
    else toBind.push({ key, scriptIndex: secret.script_index, names: [secret.secret_name], repair })
  }
  if (available.length === 0 && toBind.length === 0) return null

  const scriptName = (requirement: SecretBindingRequirement) => result.scripts[requirement.script_index ?? 0]?.name || requirement.script_name || 'Generated script'
  const nameFor = (requirement: SecretBindingRequirement) => names[requirement.id] ?? requirement.suggested_name ?? ''
  const selected = available.filter(requirement => !skipped[requirement.id])

  const validate = (): string => {
    if (selected.length === 0) return 'Select at least one credential to migrate.'
    if (selected.length > MAX_MIGRATION_SELECTIONS) return `Migrate at most ${MAX_MIGRATION_SELECTIONS} credentials at a time.`
    const invalid = selected.filter(requirement => !isValidSecretBindingName(nameFor(requirement)))
    if (invalid.length > 0) return `Enter a secret name of capital letters, digits and underscores for ${invalid.map(requirement => `${requirement.label} (${scriptName(requirement)})`).join(', ')}.`
    // One request creates each name once. Two credentials may share a name
    // only when the server proved their values equal (same script and value
    // group); otherwise the request would fail and use up the offer.
    const owners = new Map<string, SecretBindingRequirement>()
    for (const requirement of selected) {
      const name = nameFor(requirement).trim()
      const owner = owners.get(name)
      if (!owner) { owners.set(name, requirement); continue }
      const provenEqual = Boolean(owner.value_group_id) && owner.value_group_id === requirement.value_group_id && (owner.script_index ?? 0) === (requirement.script_index ?? 0)
      if (!provenEqual) return `${name} is chosen for more than one credential. Give each its own name, or leave one out and bind both scripts to the same secret.`
    }
    return ''
  }

  const migrate = async () => {
    if (busy || disabled || !offered || expired) return
    const problem = validate()
    if (problem) { setError(problem); return }
    const selections = selected.map(requirement => ({ requirement, secret_name: nameFor(requirement).trim() }))
    const uniqueNames = Array.from(new Set(selections.map(selection => selection.secret_name)))
    setBusy(true); onBusyChange(true); setError('')
    try {
      // Migration is create-only and uses the offer up even when it fails, so
      // the names are checked first and nothing is sent while one exists.
      let statuses
      try {
        statuses = await api.getProjectSecretStatuses(projectId, uniqueNames)
      } catch (cause: unknown) {
        setError(getErrorMessage(cause, 'Project Secrets could not be checked. Nothing was migrated; try again.'))
        return
      }
      const capabilities = statuses.capabilities
      if (!capabilities?.writable || capabilities.create_supported !== true) {
        setError('This project\'s secret store cannot create secrets here. Nothing was migrated: create the secrets in Project Secrets, then bind them with the batch repairs.')
        return
      }
      if (capabilities.source_secret_migration_policy_enabled === false) {
        setError('Migrating recorded credential values is turned off for this project. Nothing was migrated: create the secrets in Project Secrets, then bind them with the batch repairs.')
        return
      }
      const existing = uniqueNames.filter(name => statuses.secrets?.some(status => status.name === name && status.configured))
      if (existing.length > 0) {
        setError(`${existing.join(', ')} already ${existing.length === 1 ? 'exists' : 'exist'} in Project Secrets. Nothing was migrated: choose another name, or leave that credential out and bind the existing secret with a batch repair.`)
        return
      }
      try {
        await api.migrateImportSecrets({
          project_id: projectId,
          source_secret_session_id: sessionId,
          secrets: selections.map(selection => ({ requirement_id: selection.requirement.id, secret_name: selection.secret_name })),
        })
      } catch (cause: unknown) {
        const failure = getSourceSecretSessionFailureMetadata(cause)
        const message = formatSourceSecretSessionFailure(cause)
        if (failure.retained) { setError(message); return }
        // A mid-batch failure still created the secrets it lists: record
        // them so the bind repairs fill in their names.
        const created = createdSecretNames(cause)
        const written = selections.filter(selection => created.has(selection.secret_name))
        if (written.length > 0) { onMigrated(sessionId, toMigrated(written), message); return }
        onSessionEnded(sessionId, failure.consumed, message)
        return
      }
      onMigrated(sessionId, toMigrated(selections))
    } finally {
      setBusy(false); onBusyChange(false)
    }
  }

  return <section aria-label="Recorded credential migration" className="lr-warnings">
    <h4>Recorded credential values</h4>
    {available.length > 0 && <>
      <p>The plan holds {available.length === 1 ? 'a credential value' : `${available.length} credential values`} that can be moved into Project Secrets without showing {available.length === 1 ? 'it' : 'them'} here. This only creates the secrets; bind each script to its secret with the batch repairs, which fill in the names chosen here.</p>
      {expired
        ? <p role="status">The offer to migrate the recorded values has expired. Create the secrets in Project Secrets, or inspect the plan again to migrate them.</p>
        : <fieldset disabled={disabled || busy}>
          {available.map(requirement => <div key={requirement.id}>
            <label><input type="checkbox" checked={!skipped[requirement.id]} onChange={event => setSkipped(current => ({ ...current, [requirement.id]: !event.target.checked }))} /> {requirement.label} · {scriptName(requirement)}</label>
            <label>Project secret name <input value={nameFor(requirement)} onChange={event => setNames(current => ({ ...current, [requirement.id]: event.target.value }))} disabled={Boolean(skipped[requirement.id])} autoComplete="off" spellCheck={false} /></label>
          </div>)}
          <button type="button" className="btn btn-secondary" onClick={() => void migrate()}>{busy ? 'Migrating…' : `Migrate ${selected.length} value${selected.length === 1 ? '' : 's'} into Project Secrets`}</button>
        </fieldset>}
      {error && <p role="alert">{error}</p>}
    </>}
    {toBind.length > 0 && <div aria-label="Migrated secrets to bind">
      <p>Migrated into Project Secrets; bind {toBind.length === 1 ? 'it' : 'them'} to apply:</p>
      <ul>
        {toBind.map(entry => <li key={entry.key}>
          {entry.names.map((name, index) => <span key={name}>{index > 0 ? ', ' : ''}<code>{name}</code></span>)} for {result.scripts[entry.scriptIndex]?.name || 'Generated script'}
          {' '}{entry.repair
            ? <button type="button" className="btn btn-ghost btn-sm" disabled={disabled} onClick={() => onBind(entry.repair!.type, entry.scriptIndex)}>{entry.repair.label}</button>
            : <span>(bind it in the Visual Editor after saving)</span>}
        </li>)}
      </ul>
    </div>}
  </section>
}
