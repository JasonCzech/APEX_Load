# Project Secrets

APEX Load stores scripts with `{{secret.NAME}}` references. Secret values are
managed separately and are never returned by the Project Secrets APIs, written
into script JSON, archives, logs, fingerprints, or distributed script caches.

## Storage providers

The controller supports two writable production providers:

| Provider | Configuration | Persistence |
|---|---|---|
| Encrypted database | Configure `APEXLOAD_WRAPPING_MASTER_KEY`, or a versioned `APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID`. | Encrypted value records in the configured SQLite/PostgreSQL store. |
| Azure Key Vault | Set `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED=true` and `APEXLOAD_AZURE_KEY_VAULT_URL`; use workload/managed identity. | Values in Key Vault; managed logical-name metadata in the APEX Load database. |

The `APEXLOAD_BRUNO_*` wrapping-key names are shared with encrypted Bruno source
artifacts for compatibility. A writable encrypted database Project Secrets
store requires the wrapping key even when Bruno execution is disabled.

Generate a local wrapping key with:

```bash
openssl rand -base64 32
```

Keep that key stable, backed up, and available to every controller replica.
Losing or changing it makes existing encrypted database values unreadable.
Inject it through a Kubernetes Secret, secret manager, or protected process
environment; do not put production key material in values files or source
control.

For Azure Key Vault, grant the controller identity `Key Vault Secrets Officer`.
Generators do not need write permission. The provided AKS Terraform grants the
controller that role and retains `Key Vault Secrets User` for generators.

## Import migration policy

Source credential migration is controlled in the application by
`system_settings.script_imports.allow_source_secret_migration`. It defaults to
`false` and only a system administrator can enable it. Enabling the policy does
not bypass project authorization: a project or system administrator must still
explicitly select migration for an import.

Imports create missing logical names only. They never replace an existing
Project Secret. Literal `[REDACTED]` values are unrecoverable and remain
deferred for later configuration.

## Distributed execution

The controller resolves only the logical names referenced by the selected
script and sends a bounded, memory-only bundle to generators that advertise the
secret-bundle capability. Delivery requires authenticated TLS: mTLS, or TLS
combined with the configured gRPC bearer token.

Production deployments must configure:

- `APEXLOAD_GRPC_TLS_CERT`
- `APEXLOAD_GRPC_TLS_KEY`
- `APEXLOAD_GRPC_TLS_CA`
- `APEXLOAD_GRPC_AUTH_TOKEN` when client authentication is not provided by mTLS

The Helm, AKS, and on-prem production manifests mount TLS identities and share
the bearer token through `apexload-data-platform`. They deliberately do not
enable Key Vault writes in generator pods.

Scripts may still be saved when the selected fleet is legacy or insecure. A
secret-bearing launch then fails closed with provisioning guidance; secret
values are not downgraded to plaintext transport.

## Project deletion

Deleting a project removes its encrypted database secret records. Azure Key
Vault values are retained by design and the deletion response identifies the
logical names that require administrative cleanup.
