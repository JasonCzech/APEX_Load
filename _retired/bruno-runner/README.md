# Retired bruno-runner

This is a cold source archive, excluded from product builds and ordinary tests.
The manifest records the original paths and hashes from the working tree before retirement;
`snapshot/` contains original mixed files as well as feature-specific sources.

Restore by deliberately reintegrating the implementation into the current architecture,
updating dependencies, contracts, deployment settings, security policy and tests.
Archived code is not a maintained optional build. Stored application data, artifacts,
volumes and encryption keys are preserved separately in the active product.

`main-snapshot/` preserves the integration inputs from the current main revision;
[`main-integration-manifest.json`](../main-integration-manifest.json) records that revision and their checksums.
