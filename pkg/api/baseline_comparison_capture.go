package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

func comparisonDigest(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("v1:%x", sha256.Sum256(payload))
}

func captureBaselineComparisonMetadata(p *RunArchiveProvenance, test *ManagedTest) {
	groups := make([]TestGroup, 0, len(p.Groups))
	for _, g := range p.Groups {
		g.ScriptID = ""
		g.ScriptName = ""
		g.ActiveStage = nil
		g.RuntimeState = ""
		g.EndedAt = nil
		g.EffectiveEndAt = nil
		groups = append(groups, g)
	}
	scripts := map[string]string{}
	for _, g := range test.Groups {
		if g.script != nil {
			if projected, err := scriptdisclosure.Project(g.script); err == nil {
				projected.ID, projected.Name, projected.ProjectID = "", "", ""
				scripts[g.Name] = comparisonDigest(projected)
			}
		}
	}
	// This fingerprints the disclosure-safe projection, not secret values or mutable IDs.
	p.ComparisonFingerprint = comparisonDigest(struct {
		Groups  []TestGroup
		Runtime any
		Scripts map[string]string
	}{groups, p.RuntimeSettings, scripts})
	p.ComparisonMetrics = map[string]BaselineMetricDefinition{}
	for _, target := range externalMetricTargetsForTest(test) {
		parsed, err := url.Parse(target.URL)
		if err != nil {
			continue
		}
		unit, kind := "", ""
		switch {
		case strings.HasSuffix(target.MetricName, "_bytes"):
			unit, kind = "bytes", "gauge"
		case strings.HasSuffix(target.MetricName, "_seconds"):
			unit, kind = "s", "gauge"
		case strings.HasSuffix(target.MetricName, "_percent"):
			unit, kind = "%", "gauge"
		}
		identity := comparisonDigest(struct {
			Scheme, Host, Path, Metric string
			Labels                     map[string]string
		}{parsed.Scheme, parsed.Host, parsed.Path, target.MetricName, target.Labels})
		// Query parameters and credentials are deliberately absent from the safe
		// identity. Their presence therefore requires explicit operator mapping.
		if parsed.RawQuery != "" || parsed.User != nil {
			kind = ""
		}
		if _, duplicate := p.ComparisonMetrics[target.Name]; duplicate {
			unit, kind = "", ""
		}
		p.ComparisonMetrics[target.Name] = BaselineMetricDefinition{Identity: identity, Unit: unit, Kind: kind, Source: "application", Aggregation: "single"}
	}
}

// Derived expressions must not be differentiated again merely because a backend
// preserves the input counter's metric label. Unknown transforms need attestation.
var baselineRateExpression = regexp.MustCompile(`^\s*(?:i?rate|increase)\s*\(`)
var baselineSelectorExpression = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(?:\s*\{[^{}]*\})?$`)

func baselineCapturedMetricKind(expression, name string) string {
	if baselineRateExpression.MatchString(expression) {
		return "gauge"
	}
	if !baselineSelectorExpression.MatchString(strings.TrimSpace(expression)) {
		return ""
	}
	switch {
	case strings.HasSuffix(name, "_total"):
		return "counter"
	case strings.HasSuffix(name, "_bytes"), strings.HasSuffix(name, "_percent"), strings.HasSuffix(name, "_ratio"):
		return "gauge"
	}
	return ""
}
