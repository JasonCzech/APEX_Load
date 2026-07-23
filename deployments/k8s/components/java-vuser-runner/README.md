# Java Vuser runner component

This opt-in component adds the loopback-only, credential-minimized Java Vuser
runner to an APEX Load generator pod. Add it to a local, on-prem, or AKS
overlay:

```yaml
components:
  - ../components/java-vuser-runner
```

The published image includes JDK 21 and a Java 17-compatible harness. User
classes must be precompiled into a signed custom runner image, or supplied by a
separate read-only volume patch. Do not mount generator state, gRPC credentials,
service-account tokens, or workload-identity files into this container.

This stock non-privileged component does not receive writable cgroup v2
delegation. It therefore supports legacy `full_lifecycle` execution only;
combined persistent Java imports fail sidecar capability preflight with
`persistent_lifecycle_unavailable`. Do not add a privileged host-cgroup mount
as a workaround. Persistent lifecycle execution requires a separately governed
Windows runner or Linux runner with a narrowly delegated cgroup subtree. See
[Java Vuser Runtime](../../../../docs/JAVA_VUSER_RUNTIME.md) for the platform
and containment contract.

The sidecar separates those credentials from reviewed Java code; it is not a
sandbox for mutually untrusted scripts because it still shares the pod network.
