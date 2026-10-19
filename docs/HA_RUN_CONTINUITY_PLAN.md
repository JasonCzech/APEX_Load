# HA run continuity plan

Status: Historical design and implementation record.

Current operations: [HA controller behavior](ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior) and [storage recovery](NODE_STORAGE_RECOVERY.md). Open worker-loss qualification remains in the [node-failure readiness plan](NODE_FAILURE_READINESS_PLAN.md). Controller-only adoption does not certify combined controller/generator recovery.

The [complete dated design, decisions, P0–P4 results and subsequent reviews](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md) are preserved as recorded, including baseline statements and proposed values that are not current operating guidance. Original section anchors below remain available for code and evidence references.

## 1. Problem

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#1-problem).

## 2. Why a run cannot survive today (verified in code)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#2-why-a-run-cannot-survive-today-verified-in-code).

## 3. What is already on our side

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#3-what-is-already-on-our-side).

## 4. Answer to the open question: who drives the load shape?

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#4-answer-to-the-open-question-who-drives-the-load-shape).

## 5. Design

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#5-design).

### 5.1 Run continuity policy (opt-in)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#51-run-continuity-policy-opt-in).

### 5.2 Generator: bounded orphan grace

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#52-generator-bounded-orphan-grace).

### 5.3 Adoption protocol (atomic with the epoch claim)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#53-adoption-protocol-atomic-with-the-epoch-claim).

### 5.4 Controller: persist what adoption needs

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#54-controller-persist-what-adoption-needs).

### 5.5 Controller: takeover path

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#55-controller-takeover-path).

### 5.6 Operator actions during the gap

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#56-operator-actions-during-the-gap).

### 5.7 Results and UX

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#57-results-and-ux).

### 5.8 Reverse-connect generators (B6)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#58-reverse-connect-generators-b6).

### Out of scope

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#out-of-scope).

## 6. Phases

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#6-phases).

## 7. Acceptance criteria

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#7-acceptance-criteria).

## 8. Decisions (accepted 2026-09-25, as recommended)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#8-decisions-accepted-2026-09-25-as-recommended).

## 9. P0 results (2026-09-25)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#9-p0-results-2026-09-25).

### Baseline timeline (today's behaviour, 10 VUsers, 150s run, leader killed at +45.9s)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#baseline-timeline-todays-behaviour-10-vusers-150s-run-leader-killed-at-459s).

### Open questions answered

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#open-questions-answered).

### Findings (outside the continuity feature itself)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#findings-outside-the-continuity-feature-itself).

### Next

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#next).

## 10. P1 results (2026-09-25): persistence, no behaviour change

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#10-p1-results-2026-09-25-persistence-no-behaviour-change).

## 11. P2 results (2026-09-25): generator orphan grace and adoption

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#11-p2-results-2026-09-25-generator-orphan-grace-and-adoption).

## 12. P3 results (2026-09-25): controller takeover, **a run survives leader loss**

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#12-p3-results-2026-09-25-controller-takeover-a-run-survives-leader-loss).

### How takeover works (`pkg/api/run_adoption.go`)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#how-takeover-works-pkgapirun_adoptiongo).

### Bug found only end to end (fixed, with a regression test)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#bug-found-only-end-to-end-fixed-with-a-regression-test).

### End-to-end evidence (`scripts/ha-failover-harness.sh`, Postgres, `kill -9` of the leader at +45s)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#end-to-end-evidence-scriptsha-failover-harnesssh-postgres-kill--9-of-the-leader-at-45s).

### §7 acceptance status: all ten exercised end to end (harness, Postgres, real processes)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#7-acceptance-status-all-ten-exercised-end-to-end-harness-postgres-real-processes).

### Bugs the end-to-end scenarios found (all fixed, with regression coverage)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#bugs-the-end-to-end-scenarios-found-all-fixed-with-regression-coverage).

### Still open

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#still-open).

## 13. P4 results (2026-09-26): surface

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#13-p4-results-2026-09-26-surface).

## 14. In-flight stop across failover (2026-09-26)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#14-in-flight-stop-across-failover-2026-09-26).

## 15. `continue` + `tolerate_generator_loss` (2026-09-26)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#15-continue--tolerate_generator_loss-2026-09-26).

## Interrupted adoption token rotations (2026-09-28)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#interrupted-adoption-token-rotations-2026-09-28).

### Interrupted generator evacuation guard

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#interrupted-generator-evacuation-guard).

## 16. Graceful handoff and takeover liveness (2026-10-07)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#16-graceful-handoff-and-takeover-liveness-2026-10-07).

## 17. Review Phase 2 (2026-10-08)

[Historical design and evidence](archive/plans/HA_RUN_CONTINUITY_2026-10-19.md#17-review-phase-2-2026-10-08).
