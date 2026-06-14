# APEX Load on production on-prem Kubernetes — hardened deployment

Production-grade manifests for self-managed clusters. This is the hardened
counterpart to `deployments/k8s/local` (dev) and the on-prem sibling of
`deployments/k8s/aks`. What it adds over the dev set:

| Control | Where |
|---|---|
| mTLS + bearer-token auth on controller↔generator gRPC | `APEXLOAD_GRPC_*` in configmap/statefulset; certs via [certificates.example.yaml](certificates.example.yaml) |
| TLS-terminating ingress (no plaintext dashboard/API) | [controller-ingress.yaml](controller-ingress.yaml) |
| Metrics on a dedicated port, scoped to the monitoring namespace | `APEXLOAD_METRICS_ADDR`, [network-policy.yaml](network-policy.yaml), [servicemonitor.yaml](servicemonitor.yaml) |
| Pod Security Admission `restricted` enforced on the namespace | [namespace.yaml](namespace.yaml) |
| Dedicated ServiceAccounts, no API tokens mounted | [serviceaccounts.yaml](serviceaccounts.yaml) |
| Scoped ingress NetworkPolicies + opt-in egress templates | [network-policy.yaml](network-policy.yaml), [egress-policies.example.yaml](egress-policies.example.yaml) |
| Opt-in audited pod/workload restart controls | [restart-controls.example.yaml](restart-controls.example.yaml), [Kubernetes restart controls](../../../docs/KUBERNETES_RESTART_CONTROLS.md) |
| ephemeral-storage limits + bounded emptyDirs (no DiskPressure evictions) | deployment/statefulset |
| PDBs for controller and generators | [pdb.yaml](pdb.yaml) |
| Fail-closed production posture (`APEXLOAD_ENV=production`) | deployment/statefulset env |

## Deployment order

1. **Data platform** — PostgreSQL 16+, Redis/Valkey **with a password and TLS**
   (see `../ha/redis-valkey.example.yaml` for an in-cluster Valkey), and S3-compatible
   object storage (MinIO/Ceph RGW) or a shared RWX volume for archives.
2. **Images** — build and push to your registry, ideally via the supply-chain
   pipeline (`deployments/ci/github-actions-onprem-registry.yml`: SBOM, scan,
   sign, push by digest), or `make docker-push REGISTRY=registry.example.internal/apexload`.
3. **Certificates** — apply [certificates.example.yaml](certificates.example.yaml)
   (cert-manager) or provision `apexload-generator-grpc-tls` /
   `apexload-controller-grpc-tls` secrets from your PKI (`tls.crt`/`tls.key`/`ca.crt`).
   Rotation needs no pod restart: the generator re-reads its cert per TLS
   handshake and the controller re-reads per dial.
4. **Secrets** — create `apexload-data-platform` per [secret.example.yaml](secret.example.yaml)
   (DSN, Redis URL, gRPC token, object-store keys).
5. **Edit the CHANGE-ME values** — registry in `kustomization.yaml`, ingress
   hostname/TLS secret, object-store endpoint, ingress/monitoring namespace
   names in `network-policy.yaml`, KEDA `maxReplicaCount` sized to your nodes.
6. **Apply** — `kubectl apply -k deployments/k8s/onprem`
7. **KEDA + Prometheus** — install the KEDA operator and kube-prometheus-stack;
   the ServiceMonitor targets the dedicated metrics port automatically.
8. **Smoke test** — `kubectl -n apexload port-forward svc/apexload-controller 18080:8080`
   then `./deployments/k8s/smoke-test.sh`.

## Verify your CNI actually enforces NetworkPolicy

NetworkPolicies are silently inert on non-enforcing CNIs (plain flannel,
bridge). The gRPC layer still has mTLS + token auth, but verify the network
layer too:

```bash
# From a scratch pod NOT labeled as the controller, this must FAIL/time out:
kubectl -n apexload run netpol-probe --rm -it --restart=Never \
  --image=busybox:1.36 -- nc -z -w 3 apexload-generator 9090 \
  && echo "NETWORKPOLICY NOT ENFORCED — install Calico/Cilium" \
  || echo "NetworkPolicy enforced"
```

## Egress containment (recommended on shared clusters)

The default posture leaves egress open because generators must reach the
systems under test. On a shared cluster, apply
[egress-policies.example.yaml](egress-policies.example.yaml) after filling in
your service/node CIDRs — it stops a mistyped target from load-testing
kube-dns or the apiserver, and confines a compromised controller to its
enumerable destination set (DB, cache, generators, HTTPS).

## Operational checklist

- [ ] CNI enforcement probe passes (above)
- [ ] `kubectl -n apexload logs deploy/apexload-controller | grep grpc_transport`
      shows `mtls+token-auth` (a plaintext warning here means the cert/token
      wiring is wrong)
- [ ] Dashboard reachable only via HTTPS through the ingress
- [ ] Prometheus targets page shows the controller scrape on :9091 UP
- [ ] KEDA scale-up observed under a large queued test; fleet returns to
      minReplicaCount afterward
- [ ] `GET /api/v1/system/services` lists every generator pod after a scale event
- [ ] PostgreSQL backups scheduled (pg_dump or your platform's PITR) and
      object-store archive retention configured
- [ ] OIDC or trusted-header auth configured — `APEXLOAD_ENV=production`
      refuses to start in bypass mode

## Browser-protocol generators

The generator image runs browser VUsers under the restricted securityContext
(Playwright driver + system Chromium are baked in), but Chromium runs with
`--no-sandbox` — the restricted profile cannot start its sandbox, so a
malicious page rendered by a browser VUser executes as the generator process.
On shared clusters, treat browser generators as a separate workload class:

- run them in a dedicated StatefulSet/node pool with a gVisor or Kata
  `runtimeClassName` (pod-level isolation replaces the browser sandbox), or
- if your nodes allow unprivileged user namespaces, set
  `APEXLOAD_BROWSER_SANDBOX=true` on the generator to re-enable Chromium's
  own sandbox, and
- point browser scripts only at trusted targets.
