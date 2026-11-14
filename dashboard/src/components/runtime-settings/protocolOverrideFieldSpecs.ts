import type { RuntimeProtocolSectionKey } from '../../types/runtimeSettings'

// The override fields the scenario and group runtime editor offers for the
// native protocol sections, the complete websocket section and the Java Env
// timeouts and parameter delimiters. Each entry is a pkg/dsl/config_catalog.go
// field: its path, label, subgroup, select options and documented default.
// protocolOverrideFieldSpecs.parity.test.ts reads the Go catalog, so a field
// the catalog adds (or an option list it changes) fails until it is mirrored.

/**
 * - text, textarea, number, select, bool, list: a plain leaf.
 * - secret: a credential (SecretOverrideInput: stored values come back masked).
 * - secret_name: names a project secret (NAME or {{secret.NAME}}), never the value.
 * - identity: a username the server may return masked.
 * - url: a URL whose credentials the server masks.
 * - brokers: the Kafka broker list, whose credential brokers come back masked.
 * - map: a string map edited key by key (`path.KEY` override paths).
 * - capabilities: the Appium capability map (values may be JSON).
 */
export type ProtocolOverrideFieldKind =
  | 'text'
  | 'textarea'
  | 'number'
  | 'select'
  | 'bool'
  | 'list'
  | 'secret'
  | 'secret_name'
  | 'identity'
  | 'url'
  | 'brokers'
  | 'map'
  | 'capabilities'

export interface ProtocolOverrideFieldSpec {
  /** Canonical override path (catalog path without `config.`). */
  path: string
  label: string
  kind: ProtocolOverrideFieldKind
  /** Catalog subgroup, the heading the field is listed under. */
  group?: string
  /**
   * Select values (an Inherit choice is always offered). "" is the catalog's
   * explicit none: it lists the path with no value, which clears what a lower
   * layer sets, and is a different choice from Inherit.
   */
  options?: readonly string[]
  /** The catalog's documented engine default, shown while no layer sets the field. */
  defaultValue?: string | number | boolean
  /**
   * A map the engine refuses entries in (Kafka generic properties are not
   * executable: pkg/protocol/kafka/kafka.go), so the editor only removes or
   * resets keys the layers below supply and never adds or edits one.
   */
  removeOnly?: boolean
  /** The note a removeOnly map shows in place of the generic-properties one. */
  removeOnlyNote?: string
  /**
   * The validator refuses any value here (pkg/dsl/catalog_java_http_validation.go
   * rejects these for embedded java_http), so the editor offers only Inherit and
   * the explicit none for a scalar, and removal for a map: a stored value can be
   * cleared but nothing new can be written.
   */
  unsupported?: boolean
  /**
   * How a map key is judged credential-named, so its value is typed hidden
   * (protocolCredentialLeaves): a setting name (properties), an HTTP-style
   * header (gRPC metadata) or an Appium capability (device-cloud keys too).
   */
  credentialKeys?: 'setting' | 'header' | 'capability'
}

/** Sections the editor renders from these specs. */
export type ProtocolOverrideSpecSection = RuntimeProtocolSectionKey | 'websocket' | 'java_vuser'

/**
 * Select values for fields the catalog documents as free text but the
 * validator restricts (catalog_kafka_validation.go accepts only
 * client_credentials for kafka.oauth.mode).
 */
export const PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS: Readonly<Record<string, readonly string[]>> = {
  'kafka.oauth.mode': ['client_credentials'],
}

/**
 * Engine defaults that differ from the catalog's documented default. The
 * catalog says gRPC reflection defaults on, but GRPCConfig.UseReflection is a
 * plain omitempty bool and the engine reads an unset value as false
 * (pkg/protocol/grpc/grpc.go), so a run with no layer setting it has
 * reflection disabled. The parity test pins both sides.
 */
export const PROTOCOL_OVERRIDE_ENGINE_DEFAULTS: Readonly<Record<string, string | number | boolean>> = {
  'grpc.use_reflection': false,
  // Runtime fallbacks when no layer sets the value (pkg/protocol/kafka/kafka.go
  // 5 s timeout and defaultMockPartitions, pkg/protocol/jms/jms.go,
  // pkg/protocol/eventhubs/eventhubs.go defaultTimeout, pkg/protocol/database
  // defaultDatabaseMaxRows, pkg/protocol/grpc dsl.MaxGRPCStreamBytes). The
  // database pool limits have no engine default: unset leaves them unlimited,
  // so those specs carry no defaultValue and the placeholder reads Inherit.
  'kafka.timeout_ms': 5000,
  'kafka.partitions': 4,
  'jms.timeout_ms': 5000,
  'eventhubs.timeout_ms': 5000,
  'database.max_rows': 100,
  'grpc.max_recv_msg_size': 8388608,
}

export const PROTOCOL_OVERRIDE_FIELD_SPECS: Readonly<Record<ProtocolOverrideSpecSection, readonly ProtocolOverrideFieldSpec[]>> = {
  kafka: [
    { path: 'kafka.brokers', label: 'Bootstrap broker addresses', kind: 'brokers', group: 'Kafka' },
    { path: 'kafka.profile', label: 'Kafka broker profile', kind: 'select', group: 'Kafka', options: ['', 'eventhubs'] },
    { path: 'kafka.client_id', label: 'Kafka client ID', kind: 'text', group: 'Kafka' },
    { path: 'kafka.topic', label: 'Default Kafka topic', kind: 'text', group: 'Kafka' },
    { path: 'kafka.consumer_group', label: 'Consumer group ID', kind: 'text', group: 'Kafka consumer' },
    { path: 'kafka.consumer_transactions', label: 'Transactional consumer-producer commits', kind: 'bool', group: 'Kafka consumer' },
    { path: 'kafka.auto_offset_reset', label: 'Initial offset reset policy', kind: 'select', group: 'Kafka consumer', options: ['earliest', 'latest'], defaultValue: 'latest' },
    { path: 'kafka.isolation_level', label: 'Consumer transaction isolation level', kind: 'select', group: 'Kafka consumer', options: ['', 'read_uncommitted', 'read_committed'] },
    { path: 'kafka.acks', label: 'Producer acknowledgement level', kind: 'select', group: 'Kafka producer', options: ['none', 'leader', 'all'], defaultValue: 'all' },
    { path: 'kafka.idempotence', label: 'Idempotent producer mode', kind: 'select', group: 'Kafka producer', options: ['', 'enabled', 'disabled'] },
    { path: 'kafka.transactional_id', label: 'Producer transactional ID', kind: 'text', group: 'Kafka producer' },
    { path: 'kafka.compression', label: 'Record batch compression', kind: 'select', group: 'Kafka producer', options: ['none', 'gzip', 'snappy', 'lz4', 'zstd'], defaultValue: 'none' },
    { path: 'kafka.partition_strategy', label: 'Partition routing strategy', kind: 'select', group: 'Kafka producer', options: ['key-hash', 'round-robin', 'explicit'], defaultValue: 'key-hash' },
    { path: 'kafka.partitions', label: 'Mock topic partition count', kind: 'number', group: 'Kafka', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['kafka.partitions'] },
    { path: 'kafka.timeout_ms', label: 'Kafka operation timeout', kind: 'number', group: 'Kafka', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['kafka.timeout_ms'] },
    { path: 'kafka.max_message_bytes', label: 'Maximum record size', kind: 'number', group: 'Kafka producer', defaultValue: 1048576 },
    { path: 'kafka.producer.retries', label: 'Producer delivery retries', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.linger_ms', label: 'Producer batch linger delay', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.batch_bytes', label: 'Producer batch size', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.buffer_bytes', label: 'Producer memory buffer', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.admission_timeout_ms', label: 'Buffer admission timeout', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.delivery_timeout_ms', label: 'End-to-end delivery timeout', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.request_timeout_ms', label: 'Broker request timeout', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.producer.max_in_flight_requests', label: 'Maximum in-flight requests per connection', kind: 'number', group: 'Kafka producer' },
    { path: 'kafka.security_protocol', label: 'Kafka security protocol', kind: 'select', group: 'Kafka security', options: ['plaintext', 'ssl', 'sasl_plaintext', 'sasl_ssl'], defaultValue: 'plaintext' },
    { path: 'kafka.sasl_mechanism', label: 'SASL authentication mechanism', kind: 'select', group: 'Kafka security', options: ['', 'plain', 'scram-sha-256', 'scram-sha-512', 'oauthbearer'] },
    { path: 'kafka.sasl_username', label: 'SASL username', kind: 'identity', group: 'Kafka security' },
    { path: 'kafka.sasl_password', label: 'SASL password', kind: 'secret', group: 'Kafka security' },
    { path: 'kafka.sasl_password_secret', label: 'SASL password secret name', kind: 'secret_name', group: 'Kafka security' },
    { path: 'kafka.oauth_scope', label: 'Managed identity OAuth scope', kind: 'text', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.mode', label: 'Kafka OAuth mode', kind: 'select', group: 'Kafka OAuth', options: PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS['kafka.oauth.mode'] },
    { path: 'kafka.oauth.token_url', label: 'OAuth token endpoint URL', kind: 'url', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.client_id', label: 'OAuth client ID', kind: 'text', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.client_secret', label: 'OAuth client secret', kind: 'secret', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.client_secret_ref', label: 'OAuth client secret name', kind: 'secret_name', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.scope', label: 'OAuth token scope', kind: 'text', group: 'Kafka OAuth' },
    { path: 'kafka.oauth.authentication_method', label: 'OAuth token authentication method', kind: 'select', group: 'Kafka OAuth', options: ['', 'client_secret_basic', 'client_secret_post', 'kafka_3_3_1_basic'] },
    { path: 'kafka.tls.min_version', label: 'Kafka minimum TLS version', kind: 'select', group: 'Kafka TLS', options: ['1.2', '1.3'], defaultValue: '1.2' },
    { path: 'kafka.tls.max_version', label: 'Kafka maximum TLS version', kind: 'select', group: 'Kafka TLS', options: ['1.2', '1.3'], defaultValue: '1.3' },
    { path: 'kafka.tls.insecure_skip_verify', label: 'Skip Kafka TLS certificate verification', kind: 'bool', group: 'Kafka TLS' },
    { path: 'kafka.tls.client_cert_file', label: 'Kafka client certificate file', kind: 'text', group: 'Kafka TLS' },
    { path: 'kafka.tls.client_key_file', label: 'Kafka client private key file', kind: 'text', group: 'Kafka TLS' },
    { path: 'kafka.tls.ca_cert_file', label: 'Kafka CA certificate file', kind: 'text', group: 'Kafka TLS' },
    { path: 'kafka.serialization', label: 'Record value serialization', kind: 'select', group: 'Kafka schema registry', options: ['', 'json_schema', 'avro'] },
    { path: 'kafka.schema_registry_url', label: 'Schema Registry URL', kind: 'url', group: 'Kafka schema registry' },
    { path: 'kafka.subject', label: 'Schema Registry subject', kind: 'text', group: 'Kafka schema registry' },
    { path: 'kafka.schema', label: 'Inline schema definition', kind: 'textarea', group: 'Kafka schema registry' },
    { path: 'kafka.schema_id', label: 'Static schema ID', kind: 'number', group: 'Kafka schema registry' },
    { path: 'kafka.properties', label: 'Additional Kafka client properties', kind: 'map', group: 'Kafka', removeOnly: true, credentialKeys: 'setting' },
    { path: 'kafka.mock', label: 'Use in-memory mock Kafka broker', kind: 'bool', group: 'Kafka' },
  ],
  jms: [
    { path: 'jms.provider', label: 'JMS broker provider', kind: 'select', group: 'JMS', options: ['solace', 'servicebus', 'ibmmq'] },
    { path: 'jms.broker_url', label: 'JMS broker URL', kind: 'url', group: 'JMS' },
    { path: 'jms.message_vpn', label: 'Solace message VPN', kind: 'text', group: 'JMS' },
    { path: 'jms.username', label: 'JMS broker username', kind: 'identity', group: 'JMS' },
    { path: 'jms.password', label: 'JMS broker password', kind: 'secret', group: 'JMS' },
    { path: 'jms.client_id', label: 'JMS durable client ID', kind: 'text', group: 'JMS' },
    { path: 'jms.destination', label: 'Default JMS destination', kind: 'text', group: 'JMS' },
    { path: 'jms.destination_type', label: 'JMS destination type', kind: 'select', group: 'JMS', options: ['queue', 'topic'], defaultValue: 'queue' },
    { path: 'jms.subscription', label: 'Topic subscription name', kind: 'text', group: 'JMS' },
    { path: 'jms.reply_to', label: 'Default JMSReplyTo destination', kind: 'text', group: 'JMS' },
    { path: 'jms.acknowledgement', label: 'Session acknowledgement mode', kind: 'select', group: 'JMS', options: ['auto', 'dups_ok', 'client', 'transacted'], defaultValue: 'auto' },
    { path: 'jms.delivery_mode', label: 'Message delivery mode', kind: 'select', group: 'JMS', options: ['persistent', 'non_persistent'], defaultValue: 'persistent' },
    { path: 'jms.timeout_ms', label: 'JMS operation timeout', kind: 'number', group: 'JMS', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['jms.timeout_ms'] },
    { path: 'jms.properties', label: 'JMS connection properties', kind: 'map', group: 'JMS', credentialKeys: 'setting' },
    { path: 'jms.tls.ca_cert_secret', label: 'JMS CA certificate secret', kind: 'secret_name', group: 'JMS TLS' },
    { path: 'jms.tls.trust_store_ref', label: 'JMS trust store reference', kind: 'text', group: 'JMS TLS' },
    { path: 'jms.tls.insecure_skip_verify', label: 'Skip JMS TLS certificate verification', kind: 'bool', group: 'JMS TLS' },
    { path: 'jms.mock', label: 'Use in-memory mock JMS broker', kind: 'bool', group: 'JMS' },
  ],
  eventhubs: [
    { path: 'eventhubs.namespace', label: 'Event Hubs namespace', kind: 'text', group: 'Event Hubs' },
    { path: 'eventhubs.event_hub', label: 'Event Hub name', kind: 'text', group: 'Event Hubs' },
    { path: 'eventhubs.auth_type', label: 'Event Hubs authentication mode', kind: 'select', group: 'Event Hubs', options: ['connection_string', 'aad'], defaultValue: 'connection_string' },
    { path: 'eventhubs.connection_string', label: 'Event Hubs SAS connection string', kind: 'secret', group: 'Event Hubs' },
    { path: 'eventhubs.connection_string_secret', label: 'Connection string secret name', kind: 'secret_name', group: 'Event Hubs' },
    { path: 'eventhubs.consumer_group', label: 'Event Hubs consumer group', kind: 'text', group: 'Event Hubs', defaultValue: '$Default' },
    { path: 'eventhubs.consumer_mode', label: 'Consumer execution mode', kind: 'select', group: 'Event Hubs', options: ['observer', 'processor', 'exclusive'], defaultValue: 'observer' },
    { path: 'eventhubs.checkpoint_container_url', label: 'Checkpoint Blob container URL', kind: 'url', group: 'Event Hubs' },
    { path: 'eventhubs.processor_max_partitions', label: 'Maximum processor partitions', kind: 'number', group: 'Event Hubs', defaultValue: 32 },
    { path: 'eventhubs.owner_level', label: 'Exclusive consumer owner level', kind: 'number', group: 'Event Hubs' },
    { path: 'eventhubs.observe_partition_tail', label: 'Observe partition tail lag', kind: 'bool', group: 'Event Hubs' },
    { path: 'eventhubs.tail_poll_interval_ms', label: 'Partition tail poll interval', kind: 'number', group: 'Event Hubs', defaultValue: 10000 },
    { path: 'eventhubs.partition_key', label: 'Default partition routing key', kind: 'text', group: 'Event Hubs' },
    { path: 'eventhubs.partition_id', label: 'Default explicit partition ID', kind: 'text', group: 'Event Hubs' },
    { path: 'eventhubs.start_position', label: 'Observer start position', kind: 'select', group: 'Event Hubs', options: ['latest', 'earliest'], defaultValue: 'latest' },
    { path: 'eventhubs.timeout_ms', label: 'Event Hubs operation timeout', kind: 'number', group: 'Event Hubs', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['eventhubs.timeout_ms'] },
    { path: 'eventhubs.mock', label: 'Use in-memory mock Event Hub', kind: 'bool', group: 'Event Hubs' },
  ],
  grpc: [
    { path: 'grpc.proto_files', label: 'Protobuf descriptor files', kind: 'list', group: 'gRPC' },
    { path: 'grpc.use_reflection', label: 'Use gRPC server reflection', kind: 'bool', group: 'gRPC', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['grpc.use_reflection'] },
    { path: 'grpc.plain_text', label: 'Use plaintext h2c connection', kind: 'bool', group: 'gRPC' },
    { path: 'grpc.max_recv_msg_size', label: 'Maximum receive message size', kind: 'number', group: 'gRPC', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['grpc.max_recv_msg_size'] },
    { path: 'grpc.timeout_ms', label: 'Default RPC timeout', kind: 'number', group: 'gRPC', defaultValue: 30000 },
    { path: 'grpc.metadata', label: 'Default gRPC metadata', kind: 'map', group: 'gRPC', credentialKeys: 'header' },
    { path: 'grpc.authority', label: 'HTTP/2 :authority override', kind: 'text', group: 'gRPC' },
  ],
  database: [
    { path: 'database.driver', label: 'Database driver', kind: 'select', group: 'Database', options: ['sqlite', 'postgres'] },
    { path: 'database.dsn', label: 'Database connection string (DSN)', kind: 'secret', group: 'Database' },
    { path: 'database.dsn_secret', label: 'DSN project secret name', kind: 'secret_name', group: 'Database' },
    { path: 'database.max_open_conns', label: 'Maximum open database connections', kind: 'number', group: 'Database' },
    { path: 'database.max_idle_conns', label: 'Maximum idle database connections', kind: 'number', group: 'Database' },
    { path: 'database.conn_max_lifetime_ms', label: 'Connection maximum lifetime', kind: 'number', group: 'Database' },
    { path: 'database.query_timeout_ms', label: 'Query execution timeout', kind: 'number', group: 'Database', defaultValue: 30000 },
    { path: 'database.max_rows', label: 'Maximum rows fetched per query', kind: 'number', group: 'Database', defaultValue: PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['database.max_rows'] },
    { path: 'database.read_only', label: 'Read-only session mode', kind: 'bool', group: 'Database' },
    { path: 'database.rollback_on_teardown', label: 'Rollback uncommitted transactions on teardown', kind: 'bool', group: 'Database' },
  ],
  network: [
    { path: 'network.subprotocol', label: 'Network subprotocol', kind: 'select', group: 'Network', options: ['dns', 'tcp', 'udp', 'ftp', 'smtp', 'pop3', 'imap', 'ldap'] },
    { path: 'network.host', label: 'Target host', kind: 'text', group: 'Network' },
    { path: 'network.port', label: 'Target port', kind: 'number', group: 'Network' },
    { path: 'network.address', label: 'Target host:port address', kind: 'url', group: 'Network' },
    { path: 'network.timeout_ms', label: 'Network operation timeout', kind: 'number', group: 'Network', defaultValue: 30000 },
    { path: 'network.tls', label: 'Enable TLS encryption', kind: 'bool', group: 'Network' },
    { path: 'network.username', label: 'Network username or bind DN', kind: 'identity', group: 'Network' },
    { path: 'network.password', label: 'Network password', kind: 'secret', group: 'Network' },
    { path: 'network.from', label: 'SMTP sender address', kind: 'text', group: 'Network' },
    { path: 'network.to', label: 'SMTP recipient addresses', kind: 'list', group: 'Network' },
    { path: 'network.base_dn', label: 'LDAP base DN', kind: 'text', group: 'Network' },
  ],
  appium: [
    { path: 'appium.server_url', label: 'Appium WebDriver server URL', kind: 'url', group: 'Appium' },
    { path: 'appium.capabilities', label: 'Desired device capabilities', kind: 'capabilities', group: 'Appium', credentialKeys: 'capability' },
    { path: 'appium.session_timeout_ms', label: 'Session provisioning timeout', kind: 'number', group: 'Appium', defaultValue: 180000 },
    { path: 'appium.element_timeout_ms', label: 'Element wait timeout', kind: 'number', group: 'Appium', defaultValue: 15000 },
  ],
  java_http: [
    { path: 'java_http.codec', label: 'Java payload codec', kind: 'select', group: 'Java over HTTP', options: ['java_serialized_object_base64'], defaultValue: 'java_serialized_object_base64' },
    { path: 'java_http.helper_mode', label: 'Java serializer helper mode', kind: 'select', group: 'Java over HTTP', options: ['', 'embedded'], defaultValue: 'embedded' },
    { path: 'java_http.content_type', label: 'Default Content-Type header', kind: 'text', group: 'Java over HTTP', defaultValue: 'application/x-java-serialized-object' },
    { path: 'java_http.accept', label: 'Default Accept header', kind: 'text', group: 'Java over HTTP' },
    { path: 'java_http.body_encoding', label: 'Request body encoding', kind: 'select', group: 'Java over HTTP', options: ['base64'], defaultValue: 'base64' },
    { path: 'java_http.object_schema', label: 'Java object class or schema', kind: 'select', group: 'Java over HTTP', options: [''], unsupported: true },
    {
      path: 'java_http.request_mapping',
      label: 'Request field mapping',
      kind: 'map',
      group: 'Java over HTTP',
      removeOnly: true,
      unsupported: true,
      removeOnlyNote: 'Embedded Java over HTTP does not support request mapping, so no entry can be added or edited here. This list only removes entries a lower layer supplies.',
    },
    { path: 'java_http.response_mapping', label: 'Response field mapping', kind: 'map', group: 'Java over HTTP' },
    { path: 'java_http.extractor_type', label: 'Default response extractor type', kind: 'select', group: 'Java over HTTP', options: [''], unsupported: true },
    { path: 'java_http.preserve_binary_result', label: 'Preserve binary response payload', kind: 'bool', group: 'Java over HTTP' },
  ],
  websocket: [
    { path: 'websocket.callback_interval_ms', label: 'Callback dispatch interval', kind: 'number', group: 'WebSocket' },
    { path: 'websocket.read_timeout_ms', label: 'WebSocket read timeout', kind: 'number', group: 'WebSocket', defaultValue: 30000 },
    { path: 'websocket.write_timeout_ms', label: 'WebSocket write timeout', kind: 'number', group: 'WebSocket', defaultValue: 10000 },
    { path: 'websocket.max_message_size', label: 'Maximum WebSocket message size', kind: 'number', group: 'WebSocket', defaultValue: 1048576 },
    { path: 'websocket.subprotocol', label: 'WebSocket subprotocol', kind: 'text', group: 'WebSocket' },
    { path: 'websocket.origin', label: 'WebSocket Origin header', kind: 'text', group: 'WebSocket' },
    { path: 'websocket.compression', label: 'Enable per-message deflate compression', kind: 'bool', group: 'WebSocket' },
    { path: 'websocket.reconnect', label: 'Reconnect on unexpected close', kind: 'bool', group: 'WebSocket' },
  ],
  java_vuser: [
    { path: 'java_vuser.startup_timeout_ms', label: 'JVM startup timeout', kind: 'number', group: 'Java Env', defaultValue: 30000 },
    { path: 'java_vuser.action_timeout_ms', label: 'Java action timeout', kind: 'number', group: 'Java Env', defaultValue: 60000 },
    { path: 'java_vuser.param_left_brace', label: 'Parameter left delimiter', kind: 'text', group: 'Java Env', defaultValue: '{' },
    { path: 'java_vuser.param_right_brace', label: 'Parameter right delimiter', kind: 'text', group: 'Java Env', defaultValue: '}' },
  ],
}

/** Option labels that read better than the stored value (`path:` is the "" choice, "Not set" otherwise). */
export const PROTOCOL_OVERRIDE_OPTION_LABELS: Readonly<Record<string, string>> = {
  'database.driver:sqlite': 'SQLite',
  'database.driver:postgres': 'PostgreSQL (pgx)',
  'kafka.profile:eventhubs': 'Azure Event Hubs',
  // The catalog's "" choice: listed with no value, clearing what a lower layer sets.
  'kafka.profile:': 'None (plain Kafka)',
  'kafka.sasl_mechanism:': 'None (no SASL)',
  'kafka.serialization:': 'None (raw)',
  'eventhubs.auth_type:connection_string': 'Connection string',
  'eventhubs.auth_type:aad': 'Microsoft Entra ID',
  'jms.provider:solace': 'Solace',
  'jms.provider:servicebus': 'Azure Service Bus',
  'jms.provider:ibmmq': 'IBM MQ',
  // Embedded java_http refuses any value here: the only override is clearing one below.
  'java_http.object_schema:': 'Override with no value',
  'java_http.extractor_type:': 'Override with no value',
}

/** The tab label for each section. */
export const PROTOCOL_OVERRIDE_TAB_LABELS: Readonly<Record<ProtocolOverrideSpecSection, string>> = {
  kafka: 'Kafka',
  jms: 'JMS',
  eventhubs: 'Event Hubs',
  grpc: 'gRPC',
  database: 'Database',
  network: 'Network',
  appium: 'Appium',
  java_http: 'Java over HTTP',
  websocket: 'WebSocket',
  java_vuser: 'Java Env',
}

/**
 * What the eventhubs Kafka profile fills in for fields no layer sets, before
 * connecting (pkg/protocol/kafka/kafka.go): SASL_SSL + PLAIN, and the literal
 * username "$ConnectionString" while the mechanism is PLAIN. Author
 * (KafkaRuntimeSection) and the override editor both show these; the parity
 * test pins them against kafka.go.
 */
export const KAFKA_EVENTHUBS_PROFILE_DEFAULTS = {
  security_protocol: 'sasl_ssl',
  sasl_mechanism: 'plain',
  sasl_username: '$ConnectionString',
} as const

/**
 * The profile-dependent default the engine applies at `path` when no layer
 * sets it, given the effective values (`effectiveAt`, resolved by the host
 * through the shared runtime resolver). undefined means the spec's static
 * defaultValue applies.
 */
/**
 * Whether a kafka.profile value selects Azure Event Hubs. The validator
 * (pkg/dsl/catalog_kafka_validation.go) and the engine (kafka.go) trim and
 * lowercase the profile first, so " EventHubs" is the eventhubs profile too.
 */
export function isKafkaEventHubsProfile(profile: unknown): boolean {
  return typeof profile === 'string' && profile.trim().toLowerCase() === 'eventhubs'
}

export function protocolOverrideProfileDefault(path: string, effectiveAt: (path: string) => unknown): string | undefined {
  if (!isKafkaEventHubsProfile(effectiveAt('kafka.profile'))) return undefined
  switch (path) {
    case 'kafka.security_protocol':
      return KAFKA_EVENTHUBS_PROFILE_DEFAULTS.security_protocol
    case 'kafka.sasl_mechanism':
      return KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_mechanism
    case 'kafka.sasl_username': {
      const mechanism = effectiveAt('kafka.sasl_mechanism')
      return mechanism === undefined || mechanism === null || mechanism === '' || mechanism === KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_mechanism
        ? KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_username
        : undefined
    }
    default:
      return undefined
  }
}

/**
 * Fields that apply only together (the lr.eval_string delimiters): setting
 * one pins its partner at its effective value, and resetting one resets both.
 */
export const PROTOCOL_OVERRIDE_PAIRED_PATHS: Readonly<Record<string, string>> = {
  'java_vuser.param_left_brace': 'java_vuser.param_right_brace',
  'java_vuser.param_right_brace': 'java_vuser.param_left_brace',
}
