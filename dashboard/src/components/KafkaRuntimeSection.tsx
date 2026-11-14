import { RuntimeSectionIcon } from './RuntimeSectionIcon'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import {
  CheckboxField,
  CredentialField,
  IntegerField,
  LineListField,
  SecretNameField,
  SelectField,
  TextField,
} from './RuntimeConfigFieldControls'
import { KAFKA_OAUTH_MODE, kafkaRuntimeSummary, patchConfigBlock, type AuthorKafkaConfig } from './protocolRuntimeOptions'
import type { RuntimeSettingsSectionProps } from './ScriptRuntimeSettingsProtocolSections'
import { KAFKA_EVENTHUBS_PROFILE_DEFAULTS, PROTOCOL_OVERRIDE_ENGINE_DEFAULTS, isKafkaEventHubsProfile } from './runtime-settings/protocolOverrideFieldSpecs'

// What pkg/protocol/kafka/kafka.go runs with when the script leaves a value unset.
const ENGINE_TIMEOUT_MS = Number(PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['kafka.timeout_ms'])
const ENGINE_MOCK_PARTITIONS = String(PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['kafka.partitions'])

const PRODUCER_FIELDS: { key: keyof NonNullable<AuthorKafkaConfig['producer']>; label: string }[] = [
  { key: 'retries', label: 'Delivery retries' },
  { key: 'linger_ms', label: 'Batch linger (ms)' },
  { key: 'batch_bytes', label: 'Batch size (bytes)' },
  { key: 'buffer_bytes', label: 'Memory buffer (bytes)' },
  { key: 'admission_timeout_ms', label: 'Buffer admission timeout (ms)' },
  { key: 'delivery_timeout_ms', label: 'Delivery timeout (ms)' },
  { key: 'request_timeout_ms', label: 'Broker request timeout (ms)' },
  { key: 'max_in_flight_requests', label: 'Max in-flight requests' },
]

/**
 * Kafka runtime settings (config.kafka), laid out in the canonical runtime
 * groups the Go catalog assigns each field. Writes go through
 * patchConfigBlock, so clearing the last OAuth, TLS or producer field drops
 * that nested block instead of leaving an empty object behind.
 */
export function KafkaRuntimeSection({ script, sectionIsActive, onSelectSection, onUpdateField }: RuntimeSettingsSectionProps) {
  const kafka = (script.config.kafka || {}) as AuthorKafkaConfig
  const set = (path: string, value: unknown) => onUpdateField('config.kafka', patchConfigBlock(kafka, path, value))
  const active = sectionIsActive('kafka')
  const oauthEnabled = kafka.oauth !== undefined
  // The eventhubs profile fills an unset protocol and mechanism with SASL_SSL +
  // PLAIN ($ConnectionString) before connecting (pkg/protocol/kafka/kafka.go).
  const eventHubsProfile = isKafkaEventHubsProfile(kafka.profile)

  return (
    <div className={`settings-section ${active ? 'open' : ''}`}>
      <button type="button" className="section-toggle" onClick={() => onSelectSection('kafka')}>
        <RuntimeSectionIcon section="kafka" />
        <span className="section-label">Kafka runtime</span>
        <span className="section-summary">{kafkaRuntimeSummary(kafka)}</span>
        <span className="chevron">{active ? '▾' : '▸'}</span>
      </button>
      {active && (
        <div className="section-body" data-testid="kafka-runtime-section">
          <div className="settings-sub-group" data-canonical-group="target_identity">
            <h5>Target &amp; identity</h5>
            <div className="form-grid">
              <LineListField label="Bootstrap brokers" path="config.kafka.brokers" value={kafka.brokers} onChange={value => set('brokers', value)} placeholder={'broker-1.example.test:9092\nbroker-2.example.test:9092'} />
              <TextField label="Kafka client ID" path="config.kafka.client_id" value={kafka.client_id} onChange={value => set('client_id', value)} placeholder="apexload-producer" />
              <TextField label="Default topic" path="config.kafka.topic" value={kafka.topic} onChange={value => set('topic', value)} placeholder="orders" />
              <TextField label="Schema Registry URL" path="config.kafka.schema_registry_url" value={kafka.schema_registry_url} onChange={value => set('schema_registry_url', value)} placeholder="https://registry.example.test" />
            </div>
          </div>

          <div className="settings-sub-group" data-canonical-group="transport_connections">
            <h5>Transport &amp; connections</h5>
            <div className="form-grid">
              <label data-catalog-path="config.kafka.timeout_ms"><span>Operation timeout (s)</span>
                <SecondsFromMillisecondsInput aria-label="Kafka operation timeout" valueMs={kafka.timeout_ms} fallbackMs={ENGINE_TIMEOUT_MS}
                  onChangeMs={value => set('timeout_ms', value || undefined)} /></label>
              <IntegerField label="Max record size (bytes)" path="config.kafka.max_message_bytes" value={kafka.max_message_bytes} min={0} max={67108864} placeholder="1048576" onChange={value => set('max_message_bytes', value)} />
              <SelectField label="Batch compression" path="config.kafka.compression" value={kafka.compression} defaultValue="none" onChange={value => set('compression', value)} />
            </div>
            <h5>Producer batching</h5>
            <div className="form-grid">
              {PRODUCER_FIELDS.map(field => (
                <IntegerField key={field.key} label={field.label} path={`config.kafka.producer.${field.key}`} value={kafka.producer?.[field.key]} min={0} placeholder="Client default"
                  onChange={value => set(`producer.${field.key}`, value)} />
              ))}
            </div>
          </div>

          <div className="settings-sub-group" data-canonical-group="security_proxy_auth">
            <h5>Security, proxy &amp; auth</h5>
            <div className="form-grid">
              <SelectField label="Security protocol" path="config.kafka.security_protocol" value={kafka.security_protocol} defaultValue={eventHubsProfile ? KAFKA_EVENTHUBS_PROFILE_DEFAULTS.security_protocol : 'plaintext'} onChange={value => set('security_protocol', value)} />
              <SelectField label="SASL mechanism" path="config.kafka.sasl_mechanism" value={kafka.sasl_mechanism} defaultValue={eventHubsProfile ? KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_mechanism : undefined} onChange={value => set('sasl_mechanism', value)} />
              <TextField label="SASL username" path="config.kafka.sasl_username" value={kafka.sasl_username} onChange={value => set('sasl_username', value)} />
              <CredentialField label="SASL password" path="config.kafka.sasl_password" value={kafka.sasl_password} onChange={value => set('sasl_password', value)} placeholder="{{secret.KAFKA_PASSWORD}}" />
              <SecretNameField label="SASL password secret name" path="config.kafka.sasl_password_secret" value={kafka.sasl_password_secret} onChange={value => set('sasl_password_secret', value)} placeholder="KAFKA_PASSWORD" />
              <TextField label="Managed identity OAuth scope" path="config.kafka.oauth_scope" value={kafka.oauth_scope} onChange={value => set('oauth_scope', value)} placeholder="https://eventhubs.azure.net/.default" />
            </div>
            <div className="runtime-checkbox-list" data-catalog-path="config.kafka.oauth.mode">
              <label className="checkbox-label">
                <input type="checkbox" checked={oauthEnabled}
                  onChange={event => set('oauth', event.target.checked ? { mode: KAFKA_OAUTH_MODE } : undefined)} />
                <span>Use OAuth client credentials</span>
              </label>
            </div>
            {oauthEnabled && (
              <div className="form-grid" data-testid="kafka-oauth-fields">
                <TextField label="OAuth token URL" path="config.kafka.oauth.token_url" value={kafka.oauth?.token_url} onChange={value => set('oauth.token_url', value)} placeholder="https://login.example.test/oauth2/token" />
                <TextField label="OAuth client ID" path="config.kafka.oauth.client_id" value={kafka.oauth?.client_id} onChange={value => set('oauth.client_id', value)} />
                <CredentialField label="OAuth client secret" path="config.kafka.oauth.client_secret" value={kafka.oauth?.client_secret} onChange={value => set('oauth.client_secret', value)} placeholder="{{secret.KAFKA_OAUTH_SECRET}}" />
                <SecretNameField label="OAuth client secret name" path="config.kafka.oauth.client_secret_ref" value={kafka.oauth?.client_secret_ref} onChange={value => set('oauth.client_secret_ref', value)} placeholder="KAFKA_OAUTH_SECRET" />
                <TextField label="OAuth token scope" path="config.kafka.oauth.scope" value={kafka.oauth?.scope} onChange={value => set('oauth.scope', value)} />
                <SelectField label="OAuth authentication method" path="config.kafka.oauth.authentication_method" value={kafka.oauth?.authentication_method} onChange={value => set('oauth.authentication_method', value)} />
              </div>
            )}
            <h5>Broker TLS</h5>
            <div className="form-grid">
              <SelectField label="Minimum TLS version" path="config.kafka.tls.min_version" value={kafka.tls?.min_version} defaultValue="1.2" onChange={value => set('tls.min_version', value)} />
              <SelectField label="Maximum TLS version" path="config.kafka.tls.max_version" value={kafka.tls?.max_version} defaultValue="1.3" onChange={value => set('tls.max_version', value)} />
              <TextField label="CA certificate file" path="config.kafka.tls.ca_cert_file" value={kafka.tls?.ca_cert_file} onChange={value => set('tls.ca_cert_file', value)} placeholder="certs/kafka-ca.pem" />
              <TextField label="Client certificate file" path="config.kafka.tls.client_cert_file" value={kafka.tls?.client_cert_file} onChange={value => set('tls.client_cert_file', value)} />
              <TextField label="Client private key file" path="config.kafka.tls.client_key_file" value={kafka.tls?.client_key_file} onChange={value => set('tls.client_key_file', value)} />
              <CheckboxField label="Skip broker TLS verification (test brokers only)" path="config.kafka.tls.insecure_skip_verify" checked={Boolean(kafka.tls?.insecure_skip_verify)} onChange={value => set('tls.insecure_skip_verify', value || undefined)} />
            </div>
          </div>

          <div className="settings-sub-group" data-canonical-group="protocol_behavior_session">
            <h5>Protocol behavior &amp; session</h5>
            <div className="form-grid">
              <SelectField label="Broker profile" path="config.kafka.profile" value={kafka.profile} onChange={value => set('profile', value)} />
              <SelectField label="Producer acks" path="config.kafka.acks" value={kafka.acks} defaultValue="all" onChange={value => set('acks', value)} />
              <SelectField label="Idempotent producer" path="config.kafka.idempotence" value={kafka.idempotence} onChange={value => set('idempotence', value)} />
              <TextField label="Transactional ID" path="config.kafka.transactional_id" value={kafka.transactional_id} onChange={value => set('transactional_id', value)} />
              <SelectField label="Partition routing" path="config.kafka.partition_strategy" value={kafka.partition_strategy} defaultValue="key-hash" onChange={value => set('partition_strategy', value)} />
              <IntegerField label="Mock topic partitions" path="config.kafka.partitions" value={kafka.partitions} min={0} placeholder={ENGINE_MOCK_PARTITIONS} onChange={value => set('partitions', value)} />
              <TextField label="Consumer group ID" path="config.kafka.consumer_group" value={kafka.consumer_group} onChange={value => set('consumer_group', value)} placeholder="orders-workers" />
              <SelectField label="Initial offset reset" path="config.kafka.auto_offset_reset" value={kafka.auto_offset_reset} defaultValue="latest" onChange={value => set('auto_offset_reset', value)} />
              <SelectField label="Consumer isolation level" path="config.kafka.isolation_level" value={kafka.isolation_level} onChange={value => set('isolation_level', value)} />
              <SelectField label="Value serialization" path="config.kafka.serialization" value={kafka.serialization} onChange={value => set('serialization', value)} />
              <TextField label="Schema Registry subject" path="config.kafka.subject" value={kafka.subject} onChange={value => set('subject', value)} placeholder="orders-value" />
              <IntegerField label="Static schema ID" path="config.kafka.schema_id" value={kafka.schema_id} min={0} max={2147483647} onChange={value => set('schema_id', value)} />
              <label className="full-width" data-catalog-path="config.kafka.schema"><span>Inline schema</span>
                <textarea aria-label="Inline schema" rows={4} spellCheck={false} value={kafka.schema || ''} onChange={event => set('schema', event.target.value)} /></label>
            </div>
            <div className="runtime-checkbox-list">
              <CheckboxField label="Transactional consumer-producer commits" path="config.kafka.consumer_transactions" checked={Boolean(kafka.consumer_transactions)} onChange={value => set('consumer_transactions', value || undefined)} />
              <CheckboxField label="Use in-memory mock broker for replay" path="config.kafka.mock" checked={Boolean(kafka.mock)} onChange={value => set('mock', value || undefined)} />
            </div>
            {kafka.properties && Object.keys(kafka.properties).length > 0 && (
              <div className="parameter-empty-state" role="status" data-catalog-path="config.kafka.properties" data-testid="kafka-preserved-properties">
                <strong>Preserved client properties</strong> ({Object.keys(kafka.properties).join(', ')}) are not executable: validation rejects them. Move each one to its typed setting above, then remove them.
                <button type="button" className="btn btn-sm btn-ghost" onClick={() => set('properties', undefined)}>Remove preserved properties</button>
              </div>
            )}
          </div>
          <div className="parameter-empty-state" style={{ marginTop: 12 }}>
            Kafka real execution requires a build that includes the Kafka adapter (-tags kafka_real); use mock replay for local shakeout.
          </div>
        </div>
      )}
    </div>
  )
}
