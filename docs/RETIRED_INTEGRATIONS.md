# Retired integrations

AppDynamics monitoring and Bruno Runner are retired and excluded from product builds, API configuration workflows, CLI/MCP registrations and dashboard authoring controls. Their original implementation, tests, deployment examples and design documentation are preserved in the repository under [`_retired/appdynamics/`](../_retired/appdynamics/README.md) and [`_retired/bruno-runner/`](../_retired/bruno-runner/README.md), outside active build and test discovery.

## Existing data

Retirement does not delete database tables, connections, credentials, monitor sources, source artifacts, scripts or archives. Existing AppDynamics evidence remains available through generic monitoring history, archive tables and downloads. Sources attached to scenarios or runs must be explicitly removed before launch, whether required or optional. Archived verdicts and rule results remain readable.

Canonical Bruno scripts remain visible as unavailable for execution. Native HTTP scripts already imported from Bruno remain usable. To migrate an old collection, export its retained source, import it through the native Bruno workflow, review unsupported source behavior and credentials, then debug a controlled iteration before load testing. Canonical execution, synchronization and runner projections are unavailable.

## Deployment upgrade

Preserve the source artifact PVC or Docker volume and the existing wrapping key. The controller keeps native import artifact storage; generators no longer mount it and no runner container is built or started. Native import remains independently gated by the import policy.

Helm upgrades from a runner-enabled release must explicitly set `bruno.enabled=false`, including upgrades with `--reuse-values`. The chart rejects true values. Keep `bruno.importEnabled`, `bruno.artifactPersistence.existingClaim`, `bruno.artifactRoot` and existing wrapping-key settings when native import is required. Existing physical artifact volume and directory names remain stable.

## Restoration

Each archive's `manifest.json` records the original repository paths, baseline commit (`0a480cee9d80efd68c984e2cbc1627e25443b2fd`), and SHA-256 checksums of the retained feature-owned source files; shared integration hooks from retirement time remain inspectable in git history. Restoration requires a reviewed implementation change that reintegrates API/runtime/UI hooks, regenerates contracts and passes current security, compatibility and browser checks. Archived code is not an optional supported build profile.
