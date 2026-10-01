# Restricted Java monitoring helper

Compile with a supported JDK (17 or newer):

```sh
javac -d /opt/apex-monitor runners/monitor-java/ApexMonitor.java
```

Approve the absolute Java executable and classpath in the collector's local
adapter-targets file. These paths are operator-controlled, never supplied by a
scenario or signed job. The collector sets a 64 MiB Java heap, one active
processor, a 15-second deadline, and bounded output. Credentials arrive on stdin.
The helper inherits no process environment.

`jmx` exposes only `jvm-basic-v1`: ThreadCount, Runtime.Uptime, and HeapMemoryUsage.used.
Use `jmx://host:port`. The registry and remote JMX object must share this fixed
TLS port. Certificates must be trusted by the configured Java runtime and match
the approved hostname. A remote stub containing a socket factory is rejected;
the collector's fixed TLS socket factory is the only permitted network transport.
No MBean invocation, expression, or user-selected attribute is accepted.

`jdbc` exposes only `connection-health-v1`: a read-only transaction executing
`SELECT 1`, always rolled back. Use `postgresql://host:port/database`.
The operator must add a pinned, approved PostgreSQL JDBC jar to the classpath.
The connection uses `sslmode=verify-full`, five-second connect/socket/query
limits, and a single-row result. Arbitrary SQL, connection properties, and driver
classes are prohibited. This pack measures connection/query health; it does not
claim to measure database server load.

Example local approval:

```json
[
  {"pack":"jmx","destination":"jmx://jvm.example:1099","java_executable":"/usr/bin/java","helper_class_path":"/opt/apex-monitor","query_pack":"jvm-basic-v1"},
  {"pack":"jdbc","destination":"postgresql://db.example:5432/app","java_executable":"/usr/bin/java","helper_class_path":"/opt/apex-monitor:/opt/apex-drivers/postgresql.jar","query_pack":"connection-health-v1"}
]
```

Use a JDK with the appropriate enterprise CA configuration. Do not put passwords
in command arguments or destination URLs. Source credentials are project secrets
containing a JSON object with `username` and `password`.

For a private CA, set `tls_ca_file` to an approved absolute PEM certificate path
for JDBC, or `jvm_trust_store` to an approved absolute Java certificate-only
trust store for JMX. The latter uses the standard `changeit` store password;
never place private keys in this store. These are local operator paths and cannot
be changed by a controller job.

`make monitor-java-helper` creates the packaged helper jar. `make test-monitor-java`
runs a real authenticated TLS JMX fixture. Set `APEX_MONITOR_POSTGRES_BIN` to a
PostgreSQL binary directory and `APEX_MONITOR_JDBC_JAR` to the pinned JDBC jar to
also run disposable TLS PostgreSQL acceptance. The fixture checks the JDBC jar's
SHA-256 before use and tests credential rejection and refusal of arbitrary packs.
