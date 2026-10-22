package api

import (
	"sort"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Telemetry evidence carries text into reports, comparisons, the archive
// manifest view and safe export, and CI output: generator source and target
// names, policy names and descriptions, counter and stage reasons, and the
// verdict text built from them. Each projection scrubs it (TEL-B), in one of
// three forms:
//   - text (reasons, descriptions): the runs' own redaction rules (the
//     scenario's and each group's), then the shared credential scrubber;
//   - label (policy names): the runs' rules, then the shared scrubber's label
//     form, which removes credentials, sensitive query values and secret
//     pairs but keeps a long name;
//   - identity (generator targets, IDs and source names, and controller keys
//     such as states and exclusion reasons): the label form alone. Run rules
//     are payload rules: one masking addresses would make every generator
//     the same label and break the joins an imported archive makes on them.
// Control and bidirectional formatting characters become spaces. Verdicts
// are judged on the recorded evidence and only their text is scrubbed; the
// archive keeps what was recorded.

// telemetryTextWithheld replaces rule-scrubbed text when the runs' rules
// are more than this projection reads: it cannot be scrubbed with them, so
// it is not shown.
const telemetryTextWithheld = "[withheld: too many redaction rules]"

// maxTelemetryRuleCandidates bounds the distinct rules, valid or not, read
// from the runs' provenance, and so the compiling they cost: eight settings'
// worth at the engine's per-settings limit, more than two runs with several
// groups of distinct rules hold.
const maxTelemetryRuleCandidates = 8 * engine.MaxRedactionRules

// telemetryTextScrubber scrubs telemetry text with a set of runs' rules.
type telemetryTextScrubber struct {
	// rules holds the runs' distinct valid rules in order, in configurations
	// within the engine's per-configuration limits; all of them run before
	// the shared scrubber.
	rules []*engine.RedactionConfig
	// withheld says the runs hold more distinct rules than are read, or
	// their rules could not all be configured.
	withheld bool
}

// newTelemetryTextScrubber is the scrubber for the given runs' telemetry
// text: their scenario and group redaction rules together, each distinct
// rule once. A rule that no longer compiles is skipped, never the others.
func newTelemetryTextScrubber(manifests ...*RunArchiveManifest) telemetryTextScrubber {
	type ruleKey struct{ pattern, replacement string }
	seen := map[ruleKey]bool{}
	var out telemetryTextScrubber
	var chunk []engine.RedactionRule
	chunkBytes := 0
	flush := func() {
		// Each rule compiled alone and the chunk keeps to the engine's
		// limits; a chunk it still refuses withholds the text rather than
		// showing it without those rules.
		if config, err := engine.NewRedactionConfig(chunk); err != nil || config == nil {
			out.withheld = true
		} else {
			out.rules = append(out.rules, config)
		}
		chunk, chunkBytes = nil, 0
	}
	add := func(settings *RuntimeSettingsOverride) {
		if settings == nil {
			return
		}
		for _, rule := range settings.RedactionRules {
			key := ruleKey{strings.TrimSpace(rule.Pattern), rule.Replacement}
			if out.withheld || seen[key] {
				continue
			}
			if len(seen) == maxTelemetryRuleCandidates {
				out.withheld = true
				continue
			}
			seen[key] = true
			if config, err := engine.NewRedactionConfig([]engine.RedactionRule{rule}); err != nil || config == nil {
				continue
			}
			size := len(rule.Name) + len(rule.Pattern) + len(rule.Replacement)
			if len(chunk) == engine.MaxRedactionRules || chunkBytes+size > engine.MaxRedactionPolicyBytes {
				flush()
			}
			chunk, chunkBytes = append(chunk, rule), chunkBytes+size
		}
	}
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		for _, provenance := range []*RunArchiveProvenance{&manifest.Provenance, manifest.InitialProvenance} {
			if provenance == nil {
				continue
			}
			add(provenance.RuntimeSettings)
			for _, group := range provenance.Groups {
				add(group.RuntimeSettings)
			}
		}
	}
	if len(chunk) > 0 && !out.withheld {
		flush()
	}
	if out.withheld {
		return telemetryTextScrubber{withheld: true}
	}
	return out
}

func (s telemetryTextScrubber) clean(value string, shared func(string, scrub.Policy) string, withRules bool) string {
	if value == "" {
		return value
	}
	value = strings.ToValidUTF8(value, "\uFFFD")
	if withRules {
		if s.withheld {
			return telemetryTextWithheld
		}
		for _, rules := range s.rules {
			value = rules.ApplyPatterns(value)
		}
	}
	value = shared(value, scrub.Policy{})
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || telemetryBidiControl(r) {
			return ' '
		}
		return r
	}, value)
}

// telemetryBidiControl reports the bidirectional formatting characters (and
// the byte-order mark) that can make displayed text read differently from
// what it holds; other format characters, such as the zero-width joiners
// names rely on, are kept.
func telemetryBidiControl(r rune) bool {
	return r == '\u061C' || r == '\u200E' || r == '\u200F' || r >= '\u202A' && r <= '\u202E' || r >= '\u2066' && r <= '\u2069' || r == '\uFEFF'
}

// text scrubs free text (a reason, a description).
func (s telemetryTextScrubber) text(value string) string { return s.clean(value, scrub.Value, true) }

// label scrubs a policy name.
func (s telemetryTextScrubber) label(value string) string {
	return s.clean(value, scrub.ValueLabel, true)
}

// scrubTelemetryIdentity scrubs an identity (a generator target, ID or
// source name, or a controller key): credentials only, never run rules.
func scrubTelemetryIdentity(value string) string {
	return telemetryTextScrubber{}.clean(value, scrub.ValueLabel, false)
}

// scrubTelemetryText returns manifest with its telemetry text scrubbed for a
// projection; everything it changes is copied, so the caller's manifest is
// unchanged.
func scrubTelemetryText(manifest *RunArchiveManifest, s telemetryTextScrubber) *RunArchiveManifest {
	if manifest == nil {
		return nil
	}
	out := *manifest
	out.Provenance = scrubTelemetryProvenance(manifest.Provenance, s)
	if manifest.InitialProvenance != nil {
		initial := scrubTelemetryProvenance(*manifest.InitialProvenance, s)
		out.InitialProvenance = &initial
	}
	if record := manifest.TelemetryCoverage; record != nil {
		copied := *record
		copied.Algorithm = scrubTelemetryIdentity(record.Algorithm)
		copied.Coverage.ObserverLost = scrubTelemetryIdentity(record.Coverage.ObserverLost)
		if record.Coverage.Sources != nil {
			copied.Coverage.Sources = make([]TelemetrySourceCoverage, len(record.Coverage.Sources))
			for i, source := range record.Coverage.Sources {
				source.Source = scrubTelemetryIdentity(source.Source)
				copied.Coverage.Sources[i] = source
			}
		}
		if record.Segments != nil {
			copied.Segments = make([]TelemetryPolicySegment, len(record.Segments))
			for i, segment := range record.Segments {
				segment.Unavailable = scrubTelemetryIdentity(segment.Unavailable)
				copied.Segments[i] = segment
			}
		}
		out.TelemetryCoverage = &copied
	}
	if loss := manifest.TelemetryLoss.clone(); loss != nil {
		scrubTelemetryLoss(loss, s)
		out.TelemetryLoss = loss
	}
	if manifest.TelemetryStageDistributions != nil {
		out.TelemetryStageDistributions = cloneTelemetryStageDistributions(manifest.TelemetryStageDistributions)
		for i := range out.TelemetryStageDistributions {
			stage := &out.TelemetryStageDistributions[i]
			stage.Stage, stage.Unit, stage.Availability = scrubTelemetryIdentity(stage.Stage), scrubTelemetryIdentity(stage.Unit), scrubTelemetryIdentity(stage.Availability)
			stage.Reason = s.text(stage.Reason)
			stage.Excluded = scrubTelemetryCounts(stage.Excluded)
		}
	}
	if population := manifest.TelemetryRenderPopulation.clone(); population != nil {
		population.Population = scrubTelemetryIdentity(population.Population)
		population.Epoch, population.ReferenceClient = scrubTelemetryIdentity(population.Epoch), scrubTelemetryIdentity(population.ReferenceClient)
		population.Unqualified = scrubTelemetryCounts(population.Unqualified)
		out.TelemetryRenderPopulation = population
	}
	if coverage := manifest.SourceCoverage; coverage != nil {
		copied := *coverage
		if coverage.Sources != nil {
			copied.Sources = make([]RunArchiveSource, len(coverage.Sources))
			for i, source := range coverage.Sources {
				source.Target, source.GeneratorID = scrubTelemetryIdentity(source.Target), scrubTelemetryIdentity(source.GeneratorID)
				copied.Sources[i] = source
			}
		}
		out.SourceCoverage = &copied
	}
	if manifest.HostNetwork != nil {
		out.HostNetwork = append(manifest.HostNetwork[:0:0], manifest.HostNetwork...)
		for i := range out.HostNetwork {
			out.HostNetwork[i].Source = scrubTelemetryIdentity(out.HostNetwork[i].Source)
		}
	}
	return &out
}

// scrubTelemetryProvenance scrubs the policy and generator text a run's
// provenance carries; the slices and snapshot it changes are copied.
func scrubTelemetryProvenance(provenance RunArchiveProvenance, s telemetryTextScrubber) RunArchiveProvenance {
	if provenance.PolicyPins != nil {
		pins := make([]RunPolicyPin, len(provenance.PolicyPins))
		for i, pin := range provenance.PolicyPins {
			pins[i] = scrubRunPolicyPin(pin, s)
		}
		provenance.PolicyPins = pins
	}
	if snapshot := cloneTelemetryAcceptancePolicySnapshot(provenance.TelemetryAcceptancePolicy); snapshot != nil {
		snapshot.Pin = scrubRunPolicyPin(snapshot.Pin, s)
		definition := &snapshot.Definition
		definition.Description, definition.Mode = s.text(definition.Description), scrubTelemetryIdentity(definition.Mode)
		for i := range definition.StageLagPercentiles {
			rule := &definition.StageLagPercentiles[i]
			rule.Stage, rule.Percentile = scrubTelemetryIdentity(rule.Stage), scrubTelemetryIdentity(rule.Percentile)
		}
		provenance.TelemetryAcceptancePolicy = snapshot
	}
	provenance.GeneratorShards, provenance.GeneratorInventory = scrubGeneratorShardTargets(provenance.GeneratorShards), scrubGeneratorInventoryTargets(provenance.GeneratorInventory)
	provenance.Assignment, provenance.Groups = scrubGeneratorAssignment(provenance.Assignment), scrubTestGroupAssignments(provenance.Groups)
	return provenance
}

// scrubRunPolicyPin scrubs a policy pin: its name with the runs' rules, its
// keys as identities.
func scrubRunPolicyPin(pin RunPolicyPin, s telemetryTextScrubber) RunPolicyPin {
	pin.Name = s.label(pin.Name)
	pin.Kind, pin.ID, pin.Source = scrubTelemetryIdentity(pin.Kind), scrubTelemetryIdentity(pin.ID), scrubTelemetryIdentity(pin.Source)
	return pin
}

// scrubGeneratorAssignment returns a copy of an assignment with its selected
// targets scrubbed as identities.
func scrubGeneratorAssignment(assignment *GeneratorAssignmentSettings) *GeneratorAssignmentSettings {
	if assignment == nil || assignment.SelectedTargets == nil {
		return assignment
	}
	copied := *assignment
	copied.SelectedTargets = make([]string, len(assignment.SelectedTargets))
	for i, target := range assignment.SelectedTargets {
		copied.SelectedTargets[i] = scrubTelemetryIdentity(target)
	}
	return &copied
}

// scrubTestGroupAssignments returns groups with each group's selected
// targets scrubbed; the slice is copied when a group has any.
func scrubTestGroupAssignments(groups []TestGroup) []TestGroup {
	var out []TestGroup
	for i := range groups {
		if groups[i].Assignment == nil || groups[i].Assignment.SelectedTargets == nil {
			continue
		}
		if out == nil {
			out = append(groups[:0:0], groups...)
		}
		out[i].Assignment = scrubGeneratorAssignment(groups[i].Assignment)
	}
	if out == nil {
		return groups
	}
	return out
}

// scrubGeneratorShardTargets returns a copy of shards with their targets
// scrubbed as identities.
func scrubGeneratorShardTargets(shards []GeneratorShardSummary) []GeneratorShardSummary {
	if shards == nil {
		return nil
	}
	out := make([]GeneratorShardSummary, len(shards))
	for i, shard := range shards {
		shard.Target = scrubTelemetryIdentity(shard.Target)
		out[i] = shard
	}
	return out
}

// scrubGeneratorInventoryTargets returns a copy of inventory with its
// targets and generator IDs scrubbed as identities.
func scrubGeneratorInventoryTargets(inventory []GeneratorInventoryEntry) []GeneratorInventoryEntry {
	if inventory == nil {
		return nil
	}
	out := make([]GeneratorInventoryEntry, len(inventory))
	for i, entry := range inventory {
		entry.Target, entry.GeneratorID = scrubTelemetryIdentity(entry.Target), scrubTelemetryIdentity(entry.GeneratorID)
		out[i] = entry
	}
	return out
}

// scrubTelemetryLoss scrubs a copied loss accounting in place: its states as
// identities, its reasons as text.
func scrubTelemetryLoss(loss *TelemetryLossAccounting, s telemetryTextScrubber) {
	for _, total := range []*TelemetryCounterTotal{loss.DroppedResults, loss.ShedArrivals, loss.DroppedSubscriberBatches, loss.LostPublications, loss.WebsocketDroppedMessages, loss.WebsocketDroppedEvents} {
		if total != nil {
			total.State, total.Reason = scrubTelemetryIdentity(total.State), s.text(total.Reason)
		}
	}
}

// scrubTelemetryCounts scrubs a count map's keys (controller exclusion
// reasons) as identities; counts whose keys scrub alike are added together.
func scrubTelemetryCounts(in map[string]int64) map[string]int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]int64, len(in))
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out[scrubTelemetryIdentity(key)] += in[key]
	}
	return out
}

// scrubTelemetryVerdict returns a copy of a verdict with its text scrubbed:
// the verdict is judged on the recorded evidence, and only what it says is
// projected scrubbed. Criterion IDs and the mode are keys clients match on,
// scrubbed as identities (a real key is unchanged).
func scrubTelemetryVerdict(verdict *TelemetryAcceptanceVerdict, s telemetryTextScrubber) *TelemetryAcceptanceVerdict {
	out := cloneTelemetryAcceptanceVerdict(verdict)
	if out == nil {
		return nil
	}
	// The copy keeps an empty list empty, not absent.
	if verdict.Criteria != nil && out.Criteria == nil {
		out.Criteria = []TelemetryAcceptanceCriterion{}
	}
	if verdict.Reasons != nil && out.Reasons == nil {
		out.Reasons = []string{}
	}
	if verdict.Exclusions != nil && out.Exclusions == nil {
		out.Exclusions = []string{}
	}
	if out.Policy != nil {
		*out.Policy = scrubRunPolicyPin(*out.Policy, s)
	}
	out.Mode = scrubTelemetryIdentity(out.Mode)
	for i := range out.Criteria {
		out.Criteria[i].Criterion = scrubTelemetryIdentity(out.Criteria[i].Criterion)
		out.Criteria[i].Reason = s.text(out.Criteria[i].Reason)
	}
	for i := range out.Reasons {
		out.Reasons[i] = s.text(out.Reasons[i])
	}
	for i := range out.Exclusions {
		out.Exclusions[i] = s.text(out.Exclusions[i])
	}
	return out
}

// newCIValidatedArchiveEvidence is the evidence CI output reads from a
// verified archive; its telemetry verdict is judged on the recorded evidence
// and its text scrubbed.
func newCIValidatedArchiveEvidence(manifest *RunArchiveManifest, finalSummary *IntervalSummaryRecord, validity *ciRunValidity, validityUnavailable bool) *ciValidatedArchiveEvidence {
	return &ciValidatedArchiveEvidence{Manifest: manifest, FinalSummary: finalSummary, Validity: validity, ValidityUnavailable: validityUnavailable,
		TelemetryVerdict: scrubTelemetryVerdict(telemetryAcceptanceForManifest(manifest), newTelemetryTextScrubber(manifest))}
}

// scrubBaselineTelemetryComparison scrubs a comparison's run profiles: a
// comparison saved before telemetry text was scrubbed is projected with the
// shared scrubber (its runs' rules are not at hand).
func scrubBaselineTelemetryComparison(comparison BaselineTelemetryComparison, s telemetryTextScrubber) BaselineTelemetryComparison {
	for _, profile := range []*TelemetryRunProfile{&comparison.Current, &comparison.Baseline} {
		profile.CadenceScope = scrubTelemetryIdentity(profile.CadenceScope)
		if profile.Budgets != nil {
			budgets := make([]TelemetryStageBudget, len(profile.Budgets))
			for i, budget := range profile.Budgets {
				budget.Stage = scrubTelemetryIdentity(budget.Stage)
				budgets[i] = budget
			}
			profile.Budgets = budgets
		}
		if profile.SourceCapabilities != nil {
			capabilities := make([]string, len(profile.SourceCapabilities))
			for i, capability := range profile.SourceCapabilities {
				capabilities[i] = scrubTelemetryIdentity(capability)
			}
			profile.SourceCapabilities = capabilities
		}
		if profile.PolicyPin != nil {
			pin := scrubRunPolicyPin(*profile.PolicyPin, s)
			profile.PolicyPin = &pin
		}
		profile.Acceptance = scrubTelemetryVerdict(profile.Acceptance, s)
		if loss := profile.Loss.clone(); loss != nil {
			scrubTelemetryLoss(loss, s)
			profile.Loss = loss
		}
	}
	return comparison
}
