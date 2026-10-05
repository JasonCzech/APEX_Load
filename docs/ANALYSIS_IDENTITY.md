# Imported measurement identities

Native analytics remains capability gated. These workflows require an enabled
project and a qualified `analytics_duckdb` controller or desktop release. A
CGO-free release reports native Parquet analysis as unavailable. The source and
client changes alone do not qualify a native platform or satisfy the required
reference-load acceptance.

In the Analysis workspace, inspect the Parquet schema, select a timestamp and
measurement columns, then use **Source and labels** to map optional source,
host, service and dimension columns. Identity controls admit scalar columns;
repeated fields and protected credential column paths are excluded. The same
credential classification used by protected previews applies to each selected
path component and its joined path. Existing datasets with such identity
mappings cannot be queried or compared; originals remain unchanged and protected.
Ordinary identity values and dimension label names are not rewritten or merged.
Each path consists of at most 16 schema components.
Source, host and service values are bounded to 256 UTF-8 bytes, and dimension
names and values to 128 bytes. At most 16 dimensions are supported. Names are
exact and unique; slash, tilde, quotes and names such as `__proto__` are labels.

The dashboard enables version-2 mapping controls only when the connected
controller advertises `imported_identity_version >= 2`. Existing version-2
mappings and selectors remain unchanged on an older or unadvertised controller;
commit, query and comparison are explicitly unavailable. Version-1 imports and
queries remain usable. The client never retries an identity request with fields
removed or a lower mapping version.

Changing an identity control selects mapping version 2. Project and dataset
ownership come from the authorized import session. Row values cannot supply
project, dataset or managed-run ownership. Without a mapped source column, the
source defaults to the dataset identity. An omitted host or service has an empty
canonical value. Required source and metric names cannot be blank Unicode text;
NUL and malformed Unicode are rejected.

After import, use **Dataset identity filters** to select a source, host, service
and label combination. Filter text is matched exactly, including surrounding
spaces. A dimension row with an empty value selects an explicit empty label; an
omitted row makes no selection for that name. A metric with several distinct
series requires a narrower selection rather than averaging across identities.
Counters and rates are separated by the same identities. Version 1 historical
datasets remain queryable, but requested labels they cannot represent are
rejected explicitly.
A v2 query requires v2 canonical artifacts; the historical adapter cannot silently
downgrade the pinned mapping version. Empty source, host and service filter text
currently omits that filter. An empty host or service cannot yet be selected as
an explicit filter; presence-aware selectors remain part of the next analytics
slice. Empty dimension values are supported as described above.

Comparison provides separate filters for both imported sides and retains them
when saving and reopening the executed definition. Later edits to controls do
not alter the definition of the displayed result or the saved result. Run
comparison still requires an authorized explicit dataset association. Correlation
reports association and coverage; missing measurements never become zero.
Saved comparison bounds use UTC and retain millisecond precision in the dashboard.
A saved bound with a nonzero fraction finer than a millisecond is refused before
changing either selector or time control; use the authenticated API or CLI for
that bound. This client limit does not change the immutable source artifact.
Canonical stale, skewed, dropped, unknown or inconsistent quality/value rows
cause a qualified query error rather than being silently discarded into a
healthy aggregate.

The existing `apexctl analysis map`, `query`, `compare`, `save-mapping` and
`save-comparison` commands accept a JSON request through `--file`. Identity paths
live in `mapping.identity` (or `definition.identity` for a saved mapping).
The CLI checks authorized dataset metadata and the controller identity capability
before mapping, commit, query or comparison requests that require version 2.
It refuses an older or unadvertised controller without rewriting the request.
Version-1 operations and existing managed-run monitor selectors remain available.

Query filters use `source_id`, `host`, `service` and a `dimensions` object;
comparison carries those fields independently in `left` and `right`. Controller
authorization, mapping-version, cardinality and resource limits still apply.

New saved-definition writes reject unsupported fields, aliases, duplicate keys
and trailing JSON. Historical definitions remain readable; remove unsupported
options explicitly before writing a new revision. Consult the generated API
contract for the complete request shapes.
