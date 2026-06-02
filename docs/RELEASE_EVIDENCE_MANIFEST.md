# Release Evidence Manifest

This manifest links competitive claims to artifacts that can be reproduced for a release candidate.

## Required Evidence Before Raising Claims

| Claim area | Required artifact |
| --- | --- |
| HTTP density | Raw generator benchmark output with commit SHA, CPU, memory, container image, protocol mix, VUsers, duration, errors, and p95/p99. |
| Browser canary envelope | Raw output from `docs/BROWSER_BENCHMARK_ARTIFACTS.md` profile commands for the exact generator image. |
| Load zones | `/api/v1/fleet/load-zones` snapshot plus generator inventory showing zone region, capacity, protocol compatibility, and benchmark evidence id. |
| CI/CD gating | JUnit XML and JSON output from `apexctl run`, `status`, or `wait`. |
| Migration import | Import response archive with warnings, unsupported elements, source review, and generated DSL validation output. |

## Current Code-Owned Evidence

| Evidence | Location |
| --- | --- |
| Evaluator seed pack | `scripts/seed-evaluator-demo.sh` |
| Evaluator one-pager | `docs/EVALUATOR_COMPETITIVE_ONE_PAGER.md` |
| Browser benchmark artifact contract | `docs/BROWSER_BENCHMARK_ARTIFACTS.md` |
| Harness overhead benchmark | `docs/BENCHMARK_HARNESS_OVERHEAD.md` |
| CI/CD integration | `docs/CICD_INTEGRATION.md` |
| Competitive support matrix | `docs/COMPETITIVE_COMPARISON.md` |

## Claim Discipline

Do not promote a `Partial` or `Evidence required` item to a customer-facing `Supported` claim unless the manifest contains reproducible artifacts for that release candidate. Keep high-density browser load, managed SaaS control plane, and unsupported legacy desktop protocols out of marketing copy unless a product decision changes scope.
