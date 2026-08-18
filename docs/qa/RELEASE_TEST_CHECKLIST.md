# Release Test Checklist

_Draft created in M0. It remains a living checklist until M8 records an executed result._

- [ ] Repository quality and generated-artifact gates pass on the release commit.
- [ ] Coverage ratchet and changed-line coverage pass.
- [ ] Tier-0 assertion matrix and full-arc journey are green.
- [ ] Converter golden inventory and protected import journey are green.
- [ ] Run-control, archive-integrity, PostgreSQL migration, and restore evidence pass.
- [ ] kind scale/failure matrix and all four performance measurements are committed.
- [ ] AKS prerequisites, 10/10 readiness gates, and HA disruption evidence pass.
- [ ] Multi-region placement and attribution reconcile.
- [ ] Four-mode authentication matrix and production fail-closed checks pass.
- [ ] 72-hour soak meets the committed thresholds.
- [ ] Every open high-severity risk has an owner and no expired deferral.
- [ ] Release evidence manifest links every published claim to a committed artifact.
