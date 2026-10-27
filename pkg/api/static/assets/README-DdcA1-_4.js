var e=`# APEX Load on production on-prem Kubernetes — hardened deployment

Production-grade manifests for self-managed clusters. This is the hardened
counterpart to \`deployments/k8s/local\` (dev) and the on-prem sibling of
\`deployments/k8s/aks\`. What it adds over the dev set:

Controller-loss fencing is mandatory. Follow the maintenance-window rollout
gate and acceptance drill in [Generator run supervision](../../../docs/ENTERPRISE_FLEET_AND_CAPACITY.md#generator-run-supervision)
when upgrading from a tokenless release.

| Control | Where |
|---|---|
| mTLS + bearer-token auth on controller↔generator gRPC | \`APEXLOAD_GRPC_*\` in configmap/statefulset; certs via \`certificates.example.yaml\` |
| TLS-terminating ingress (no plaintext dashboard/API) | \`controller-ingress.yaml\` |
| Metrics on a dedicated port, scoped to the monitoring namespace | \`APEXLOAD_METRICS_ADDR\`, \`network-policy.yaml\`, optional \`servicemonitor.yaml\` |
| Pod Security Admission \`restricted\` enforced on the namespace | \`namespace.yaml\` |
| Dedicated ServiceAccounts; controller-only namespaced API token | \`serviceaccounts.yaml\`, \`restart-controls.yaml\` |
| Scoped ingress NetworkPolicies + opt-in egress templates | \`network-policy.yaml\`, \`egress-policies.example.yaml\` |
| Default audited pod/workload restart controls | \`restart-controls.yaml\`, [Kubernetes restart controls](../../../docs/CLUSTERS_AND_CLOUD.md#kubernetes-restart-controls) |
| ephemeral-storage limits + bounded emptyDirs (no DiskPressure evictions) | deployment/statefulset |
| PDBs for controller and generators | \`pdb.yaml\` |
| Fail-closed production posture (\`APEXLOAD_ENV=production\`) | deployment/statefulset env |

## Deployment order

1. **Data platform** — PostgreSQL 16+, Redis/Valkey **with a password and TLS**
   (see \`../ha/redis-valkey.example.yaml\` for an in-cluster Valkey), and S3-compatible
   object storage (MinIO/Ceph RGW) or a shared RWX volume for archives.
2. **Images** — publish a signed semantic release through
   \`.github/workflows/release.yml\`, then mirror its exact
   manifest digests with
   \`.github/workflows/publish-onprem.yml\`. The
   workflow scans and signs candidate digests before exposing release tags.
3. **Certificates** — apply \`certificates.example.yaml\`
   (cert-manager) or provision \`apexload-generator-grpc-tls\` /
   \`apexload-controller-grpc-tls\` secrets from your PKI (\`tls.crt\`/\`tls.key\`/\`ca.crt\`).
   Rotation needs no pod restart: the generator re-reads its cert per TLS
   handshake and the controller re-reads per dial.
4. **Secrets** — create \`apexload-data-platform\` per \`secret.example.yaml\`
   (DSN, Redis URL, gRPC token, Project Secrets wrapping key, object-store
   keys). The wrapping key enables encrypted database-backed Project Secrets
   even when Bruno execution is disabled.
5. **Edit the CHANGE-ME values** — registry in \`kustomization.yaml\`, ingress
   hostname/TLS secret, object-store endpoint, and ingress/monitoring namespace
   names in \`network-policy.yaml\`.
6. **Apply the base** — choose a unique, stable installation ID and render the
   edited base before applying it. Raw \`kubectl apply -k\` leaves the fleet ID
   placeholder literal and disables installation-scoped demand metrics.

   \`\`\`sh
   export APEXLOAD_FLEET_ID=onprem-production-1
   scripts/render-onprem-manifests.sh --output /tmp/apexload-onprem.yaml
   kubectl apply -f /tmp/apexload-onprem.yaml
   \`\`\`
7. **Optional controller HA** — with shared PostgreSQL and Redis configured,
   render \`--overlay-dir deployments/k8s/onprem-ha\` to a new output path and
   apply that file. The HA overlay creates
   a selectorless leader-only Service and a generation-fenced EndpointSlice for
   CI mutations; authentication and read traffic remain available across all
   ready replicas.
8. **Optional autoscaling + Prometheus** — install the KEDA operator and
   kube-prometheus-stack, size KEDA \`maxReplicaCount\` for your nodes, then
   render \`--overlay-dir deployments/k8s/onprem-autoscaling\` to a new output
   path and apply that file. For HA plus autoscaling, use
   \`deployments/k8s/onprem-ha-autoscaling\` instead: it retains the HA Deployment
   while adding KEDA. Use the same \`APEXLOAD_FLEET_ID\` for every render;
   re-render when changing the installation ID. Check the rendered ConfigMap
   and ScaledObject queries before applying.
9. **Smoke test** — \`kubectl -n apexload port-forward svc/apexload-controller 18080:8080\`
   then \`./deployments/k8s/smoke-test.sh\`.

## Verify your CNI actually enforces NetworkPolicy

NetworkPolicies are silently inert on non-enforcing CNIs (plain flannel,
bridge). The gRPC layer still has mTLS + token auth, but verify the network
layer too:

\`\`\`bash
# From a scratch pod NOT labeled as the controller, this must FAIL/time out:
kubectl -n apexload run netpol-probe --rm -it --restart=Never \\
  --image=busybox:1.36 -- nc -z -w 3 apexload-generator 9090 \\
  && echo "NETWORKPOLICY NOT ENFORCED — install Calico/Cilium" \\
  || echo "NetworkPolicy enforced"
\`\`\`

## Egress containment (recommended on shared clusters)

The default posture leaves egress open because generators must reach the
systems under test. On a shared cluster, apply
\`egress-policies.example.yaml\` after filling in
your service/node CIDRs — it stops a mistyped target from load-testing
kube-dns or the apiserver, and confines a compromised controller to its
enumerable destination set (DB, cache, generators, HTTPS).

## Operational checklist

- [ ] CNI enforcement probe passes (above)
- [ ] \`kubectl -n apexload logs deploy/apexload-controller | grep grpc_transport\`
      shows \`mtls+token-auth\` (a plaintext warning here means the cert/token
      wiring is wrong)
- [ ] Dashboard reachable only via HTTPS through the ingress
- [ ] If autoscaling is enabled, Prometheus targets page shows the controller scrape on :9091 UP
- [ ] If autoscaling is enabled, KEDA scale-up observed under a large queued test; fleet returns to
      minReplicaCount afterward
- [ ] \`GET /api/v1/system/services\` lists every generator pod after a scale event
- [ ] Project Secrets reports a writable \`database\` provider and a
      secret-bearing smoke run reaches only capability-advertised generators
- [ ] PostgreSQL backups scheduled (pg_dump or your platform's PITR) and
      object-store archive retention configured
- [ ] OIDC or trusted-header auth configured — \`APEXLOAD_ENV=production\`
      refuses to start in bypass mode

## Browser-protocol generators

The generator image runs browser VUsers under the restricted securityContext
(Playwright driver + system Chromium are baked in), but Chromium runs with
\`--no-sandbox\` — the restricted profile cannot start its sandbox, so a
malicious page rendered by a browser VUser executes as the generator process.
On shared clusters, treat browser generators as a separate workload class:

- run them in a dedicated StatefulSet/node pool with a gVisor or Kata
  \`runtimeClassName\` (pod-level isolation replaces the browser sandbox), or
- if your nodes allow unprivileged user namespaces, set
  \`APEXLOAD_BROWSER_SANDBOX=true\` on the generator to re-enable Chromium's
  own sandbox, and
- point browser scripts only at trusted targets.
`;export{e as default};