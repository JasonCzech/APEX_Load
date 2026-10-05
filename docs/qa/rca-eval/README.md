# Root-cause evaluation corpus

Status: harness built; corpus not collected

This directory holds the labelled runs that the root-cause engine is
calibrated and judged on (WP3.9, G26 and G29). Gate D-b opens when an
immutable replay of the held-out split meets the release bars below. No
trials have been collected yet. They need the reference system under test
and fault injection described in [Collecting trials](#collecting-trials).

## Layout

| Path | Holds |
| --- | --- |
| `manifest.json` | `apexload.rca_eval_manifest.v1`: one entry per trial |
| `assessments/<trial>.json` | The trial's assessment: bare, or an analysis or bundle response |
| `scores/<date>-<split>.json` | `apexload.rca_eval_scores.v1` results worth keeping |

Run archives stay in the archive store as frozen evidence revisions; the
corpus refers to them by run ID.

## Trial labels

Each trial records:

- `id` and `seed`. The seed fixes the split; both are unique.
- `class`: the injected fault class, or `healthy` for a control.
- `case`, optional: `misleading_correlation`, `missing_evidence` or
  `generator_bottleneck`.
- `run_id`, `target_entity`, `fault_start` and `fault_end`.
- `baseline_run_id`, optional: the run a replay compares the trial's levels
  with.
- What the right answer is, at least one per fault trial:
  - `expected_signature`: the signature that should rank first. When
    `target_entity` is set, the hypothesis must also be about it, matched
    against the graph nodes its evidence measured, by node ID or by the
    entity ID that ends one.
  - `expected_outcome`: for a missing-evidence trial, usually
    `insufficient_evidence`.
  - `expected_validity`: for a generator bottleneck, `suspect` or `invalid`.

  Controls expect no signature.
- `assessment`: the trial's assessment file, inside this directory.
- `split`, optional. When recorded, it must equal the seed's split.

## Split

A trial's split is fixed by its seed before any tuning. The first eight
bytes of the seed's SHA-256, modulo 100, below 30 calibrate (`calibration`);
the rest are `held_out` (`rca.EvalSplit`). Calibration tooling reads only the
calibration split (`EvalManifest.TrialsIn`). The harness scores held-out
trials only when asked to, with `--split held_out` or `--split all`.

## Minimums and release bars

| Measure | Bar |
| --- | --- |
| Fault classes | at least 8, each with at least 20 trials |
| Healthy controls | at least 100 |
| Special cases | at least one each of misleading correlation, missing evidence and generator bottleneck |
| Top-1 recovery on held-out controlled faults | ≥ 80% |
| Top-3 recovery on held-out controlled faults | ≥ 90% |
| Unsupported high-confidence conclusions | 0 |
| False attribution on healthy controls | ≤ 5% |

The minimums are judged on the whole corpus, both splits, whichever split
is scored. The scores also report abstention, per-class recall, agreement
with expected outcomes and validity verdicts, how often each special case
got its right answer, and onset error. Onset
error is given as the share of top-1 recoveries whose earliest cause began
within one minute of the fault, plus its median and 90th percentile. Every
rate carries a Wilson 95% interval. A wrong conclusion is any high-confidence
hypothesis on a control, or one whose signature is not the expected one on a
fault trial. False attribution is a control whose outcome is `supported`.

## Running the harness

Score saved assessments:

```bash
apexctl rca eval --corpus docs/qa/rca-eval --split calibration
```

Replay the held-out split with the controller's current engine, then score
it and fail unless the corpus is ready and every bar is met. `--require-bars`
accepts only the held-out split:

```bash
apexctl rca eval --corpus docs/qa/rca-eval --split held_out --replay --api https://controller.example --require-bars
```

The same manifest and assessments always give byte-identical scores. A
replay re-analyzes each trial's frozen run archive. Scores list each engine
version they came from; a mix of versions is a corpus gap.

## Collecting trials

Collecting trials needs a non-production reference system: a gateway, a
service, a database and Kafka, with Dynatrace or OpenTelemetry agents and
Elasticsearch logs. Faults are injected with network latency and drops, CPU
stress, a capped connection pool, a paused consumer, heap pressure and DNS
delay. Fault trials and healthy controls are interleaved on the shared
environment. The set also includes misleading-correlation, missing-evidence
and generator-bottleneck cases. Every provider the corpus uses needs a
qualification record in [`../qualification`](../qualification).
