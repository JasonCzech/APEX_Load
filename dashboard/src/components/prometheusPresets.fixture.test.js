// @vitest-environment node

import { readFileSync, writeFileSync } from "node:fs";
import { expect, it } from "vitest";
import { buildPrometheusPreset, prometheusPresets } from "./prometheusPresets";

// pkg/prometheus validates every query in this fixture with the backend
// Selection.Validate, so a preset the controller would reject fails CI.
// Regenerate with UPDATE_PROMETHEUS_PRESET_FIXTURE=1.
const fixture = new URL("../../../pkg/prometheus/testdata/dashboard-presets.json", import.meta.url);

it("keeps the backend-validated preset fixture current", () => {
  const built = prometheusPresets.map((preset) => buildPrometheusPreset(preset, { job: "kube-state-metrics", namespace: "checkout" }));
  if (process.env.UPDATE_PROMETHEUS_PRESET_FIXTURE) writeFileSync(fixture, JSON.stringify(built, null, 2) + "\n");
  expect(built).toEqual(JSON.parse(readFileSync(fixture, "utf8")));
});
