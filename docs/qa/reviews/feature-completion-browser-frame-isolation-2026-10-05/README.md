# Browser document finalization isolation

A detached old frame could trigger page-wide censoring before a live sibling supplied its final document observation. Finalization now attempts each live frame before applying the shared end boundary. Frame collection is deterministic, excludes ended or missing frames, and removes duplicates. The adjacent regression explicitly exercises stale-first and live-first order.

Gate index (`evidence/frame-isolation-gate-index-v1.json`) records the exact two-file source freeze and unchanged Go source bookends. Required-mode native checks repeated the existing Chromium and WebKit identity/no-interaction oracle three times each, with zero skips and exact fixture counters of two main-document and four frame requests per engine attempt. The physically closed/joined-worker regression also passed. Ordered-frame and joined-worker regressions passed three times under the race detector. The helper, raw logs, and source inventory are retained.

This is a prerequisite bug fix. Extended HAR working-file ownership, archive/export, aggregate writer/RSS limits, cross-platform qualification, and the composed full Go/dashboard gates remain separate acceptance work.

The independent repeat review (`evidence/frame-isolation-independent-repeat-review.json`) found no additional actionable issues. The composed documentation check (`evidence/frame-isolation-docs-composed-1.json`) retained an existing missing authoring-qualification link; it remains pending until that separate slice is integrated.
