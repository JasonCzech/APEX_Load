package api

import (
	"maps"
	"slices"
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
		copied.CoverageRule = scrubTelemetryIdentity(record.CoverageRule)
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
				segment.Unavailable, segment.PolicyHash = scrubTelemetryIdentity(segment.Unavailable), scrubTelemetryIdentity(segment.PolicyHash)
				copied.Segments[i] = segment
			}
		}
		out.TelemetryCoverage = &copied
	}
	if manifest.TelemetryPolicySegments != nil {
		out.TelemetryPolicySegments = make([]TelemetryPolicySegmentReceipt, len(manifest.TelemetryPolicySegments))
		for i, segment := range manifest.TelemetryPolicySegments {
			segment.Source, segment.PolicyID = scrubTelemetryIdentity(segment.Source), scrubTelemetryIdentity(segment.PolicyID)
			segment.RequestedBy = s.label(segment.RequestedBy)
			segment.Policy = scrubTelemetryBudgetPolicy(segment.Policy)
			out.TelemetryPolicySegments[i] = segment
		}
	}
	if manifest.TelemetryStageDigests != nil {
		// Keys that scrub alike keep the first, in key order.
		out.TelemetryStageDigests = make(map[string]engine.TDigestSnapshot, len(manifest.TelemetryStageDigests))
		for _, stage := range slices.Sorted(maps.Keys(manifest.TelemetryStageDigests)) {
			if key := scrubTelemetryIdentity(stage); !telemetryDigestKeyTaken(out.TelemetryStageDigests, key) {
				out.TelemetryStageDigests[key] = manifest.TelemetryStageDigests[stage]
			}
		}
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
			stage.DeliverySignal = scrubTelemetryIdentity(stage.DeliverySignal)
			stage.Reason = s.text(stage.Reason)
			stage.Excluded = scrubTelemetryCounts(stage.Excluded)
		}
	}
	if population := manifest.TelemetryRenderPopulation.clone(); population != nil {
		scrubTelemetryRenderPopulation(population)
		out.TelemetryRenderPopulation = population
	}
	out.TelemetryEvidenceFile = scrubTelemetryIdentity(manifest.TelemetryEvidenceFile)
	findings := manifest.TelemetryValidityFindings
	if len(findings) == 0 && (manifest.TelemetryCoverage != nil || len(manifest.TelemetryStageDistributions) > 0 || manifest.TelemetryRenderPopulation != nil) {
		findings = buildTelemetryValidityFindings(manifest)
	}
	if findings != nil {
		out.TelemetryValidityFindings = scrubTelemetryValidityFindings(findings, s)
	}
	if manifest.TelemetryAcceptance != nil {
		out.TelemetryAcceptance = scrubTelemetryVerdict(manifest.TelemetryAcceptance, s)
	} else if verdict := telemetryAcceptanceForManifest(manifest); verdict != nil {
		out.TelemetryAcceptance = scrubTelemetryVerdict(verdict, s)
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

func scrubTelemetryRenderPopulation(population *TelemetryRenderPopulation) {
	if population == nil {
		return
	}
	population.Population = scrubTelemetryIdentity(population.Population)
	population.Epoch, population.ReferenceClient = scrubTelemetryIdentity(population.Epoch), scrubTelemetryIdentity(population.ReferenceClient)
	population.Unqualified = scrubTelemetryCounts(population.Unqualified)
	for i := range population.PriorEpochs {
		scrubTelemetryRenderPopulation(&population.PriorEpochs[i])
	}
}

// scrubTelemetryBudgetPolicy returns a copy of a budget policy with its keys
// scrubbed as identities (a real one, and so its hash, passes unchanged).
func scrubTelemetryBudgetPolicy(policy *TelemetryBudgetPolicy) *TelemetryBudgetPolicy {
	policy = cloneTelemetryBudgetPolicy(policy)
	if policy == nil {
		return nil
	}
	for _, key := range []*string{&policy.SchemaVersion, &policy.PolicyID, &policy.Hash, &policy.CaptureBoundary, &policy.ClassificationAlgorithm, &policy.CadenceScope, &policy.CoverageRule} {
		*key = scrubTelemetryIdentity(*key)
	}
	for i := range policy.Budgets {
		policy.Budgets[i].Stage = scrubTelemetryIdentity(policy.Budgets[i].Stage)
	}
	return policy
}

func telemetryDigestKeyTaken(digests map[string]engine.TDigestSnapshot, key string) bool {
	_, taken := digests[key]
	return taken
}

// scrubTelemetryProvenance scrubs the policy and generator text a run's
// provenance carries; the slices and snapshot it changes are copied.
func scrubTelemetryProvenance(provenance RunArchiveProvenance, s telemetryTextScrubber) RunArchiveProvenance {
	provenance.TelemetryPolicy = scrubTelemetryBudgetPolicy(provenance.TelemetryPolicy)
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
	scrubber := newTelemetryTextScrubber(manifest)
	verdict := scrubTelemetryVerdict(telemetryAcceptanceForManifest(manifest), scrubber)
	scrubbedManifest := scrubTelemetryText(manifest, scrubber)
	var scrubbedValidity *ciRunValidity
	if validity != nil {
		copied := *validity
		if validity.Reasons != nil {
			copied.Reasons = make([]string, len(validity.Reasons))
			for i, reason := range validity.Reasons {
				copied.Reasons[i] = scrubber.text(reason)
			}
		}
		scrubbedValidity = &copied
	}
	return &ciValidatedArchiveEvidence{
		Manifest:            scrubbedManifest,
		FinalSummary:        finalSummary,
		Validity:            scrubbedValidity,
		ValidityUnavailable: validityUnavailable,
		TelemetryVerdict:    verdict,
	}
}

func newManagedTestTelemetryTextScrubber(test *ManagedTest) telemetryTextScrubber {
	if test == nil {
		return telemetryTextScrubber{}
	}
	manifest := &RunArchiveManifest{
		Provenance: RunArchiveProvenance{
			RuntimeSettings: cloneRuntimeSettings(test.RuntimeSettings),
			Groups:          cloneArchiveTestGroups(test.Groups),
		},
	}
	if test.initialArchiveProvenance != nil {
		initial := *test.initialArchiveProvenance
		manifest.InitialProvenance = &initial
	}
	return newTelemetryTextScrubber(manifest)
}

func scrubTelemetryValidityFindings(findings []TelemetryValidityFinding, s telemetryTextScrubber) []TelemetryValidityFinding {
	if findings == nil {
		return nil
	}
	out := cloneTelemetryValidityFindings(findings)
	for i := range out {
		finding := &out[i]
		finding.Criterion = scrubTelemetryIdentity(finding.Criterion)
		finding.Status = scrubTelemetryIdentity(finding.Status)
		finding.Summary = s.text(finding.Summary)
		for j := range finding.Details {
			finding.Details[j] = s.text(finding.Details[j])
		}
	}
	return out
}

func cloneTelemetryPolicySegments(in []TelemetryPolicySegmentReceipt) []TelemetryPolicySegmentReceipt {
	if in == nil {
		return nil
	}
	out := make([]TelemetryPolicySegmentReceipt, len(in))
	for i, seg := range in {
		seg.Policy = cloneTelemetryBudgetPolicy(seg.Policy)
		out[i] = seg
	}
	return out
}

func cloneTelemetryCoverageRecord(in *TelemetryCoverageRecord) *TelemetryCoverageRecord {
	if in == nil {
		return nil
	}
	out := *in
	if in.Coverage.Sources != nil {
		out.Coverage.Sources = append([]TelemetrySourceCoverage(nil), in.Coverage.Sources...)
	}
	if in.BreachedKnownFraction != nil {
		val := *in.BreachedKnownFraction
		out.BreachedKnownFraction = &val
	}
	if in.Segments != nil {
		out.Segments = append([]TelemetryPolicySegment(nil), in.Segments...)
	}
	return &out
}

func scrubTelemetryPolicySegments(segments []TelemetryPolicySegmentReceipt, s telemetryTextScrubber) []TelemetryPolicySegmentReceipt {
	if segments == nil {
		return nil
	}
	out := make([]TelemetryPolicySegmentReceipt, len(segments))
	for i, segment := range segments {
		segment.Source, segment.PolicyID = scrubTelemetryIdentity(segment.Source), scrubTelemetryIdentity(segment.PolicyID)
		segment.RequestedBy = s.label(segment.RequestedBy)
		segment.Policy = scrubTelemetryBudgetPolicy(segment.Policy)
		out[i] = segment
	}
	return out
}

func scrubTelemetryStageDistributions(stages []TelemetryStageDistribution, s telemetryTextScrubber) []TelemetryStageDistribution {
	if stages == nil {
		return nil
	}
	out := cloneTelemetryStageDistributions(stages)
	for i := range out {
		stage := &out[i]
		stage.Stage, stage.Unit, stage.Availability = scrubTelemetryIdentity(stage.Stage), scrubTelemetryIdentity(stage.Unit), scrubTelemetryIdentity(stage.Availability)
		stage.DeliverySignal = scrubTelemetryIdentity(stage.DeliverySignal)
		stage.Reason = s.text(stage.Reason)
		stage.Excluded = scrubTelemetryCounts(stage.Excluded)
	}
	return out
}

func scrubTelemetryCoverageValue(in *TelemetryCoverage) *TelemetryCoverage {
	if in == nil {
		return nil
	}
	copied := *in
	copied.ObserverLost = scrubTelemetryIdentity(in.ObserverLost)
	if in.Sources != nil {
		copied.Sources = make([]TelemetrySourceCoverage, len(in.Sources))
		for i, source := range in.Sources {
			source.Source = scrubTelemetryIdentity(source.Source)
			copied.Sources[i] = source
		}
	}
	return &copied
}

func scrubLiveTelemetryHealth(in *TelemetryHealth, s telemetryTextScrubber) *TelemetryHealth {
	out := cloneTelemetryHealth(in)
	if out == nil {
		return nil
	}
	out.Coverage = scrubTelemetryCoverageValue(out.Coverage)
	if out.Loss != nil {
		scrubTelemetryLoss(out.Loss, s)
	}
	out.StageDistributions = scrubTelemetryStageDistributions(out.StageDistributions, s)
	out.Acceptance = scrubTelemetryVerdict(out.Acceptance, s)
	if out.RenderPopulation != nil {
		scrubTelemetryRenderPopulation(out.RenderPopulation)
	}
	out.PolicySegments = scrubTelemetryPolicySegments(out.PolicySegments, s)
	for i := range out.StageBudgets {
		out.StageBudgets[i].Stage = scrubTelemetryIdentity(out.StageBudgets[i].Stage)
	}
	for i := range out.IntervalConditionLabels {
		out.IntervalConditionLabels[i] = scrubTelemetryIdentity(out.IntervalConditionLabels[i])
	}
	out.ValidityFindings = scrubTelemetryValidityFindings(out.ValidityFindings, s)
	return out
}

func scrubTelemetryRunHealth(in *TelemetryRunHealth, s telemetryTextScrubber) *TelemetryRunHealth {
	out := cloneTelemetryRunHealth(in)
	if out == nil {
		return nil
	}
	out.Status = scrubTelemetryIdentity(out.Status)
	out.Severity = scrubTelemetryIdentity(out.Severity)
	out.Summary = s.text(out.Summary)
	out.CurrentSignature = scrubTelemetryIdentity(out.CurrentSignature)
	for i := range out.Conditions {
		out.Conditions[i] = s.text(out.Conditions[i])
	}
	for i := range out.Unscored {
		out.Unscored[i] = s.text(out.Unscored[i])
	}
	for i := range out.ConditionLabels {
		out.ConditionLabels[i] = scrubTelemetryIdentity(out.ConditionLabels[i])
	}
	for i := range out.StageBudgets {
		out.StageBudgets[i].Stage = scrubTelemetryIdentity(out.StageBudgets[i].Stage)
	}
	return out
}

func scrubTelemetryDiagnostic(in *TelemetryDiagnostic, fallbackTel *TelemetryHealth, s telemetryTextScrubber) *TelemetryDiagnostic {
	if in == nil {
		return nil
	}
	out := *in
	out.Severity = scrubTelemetryIdentity(out.Severity)
	out.Summary = s.text(out.Summary)
	if in.Conditions != nil {
		out.Conditions = make([]string, len(in.Conditions))
		for i, cond := range in.Conditions {
			out.Conditions[i] = s.text(cond)
		}
	}
	if in.Unscored != nil {
		out.Unscored = make([]string, len(in.Unscored))
		for i, u := range in.Unscored {
			out.Unscored[i] = s.text(u)
		}
	}
	if in.Samples != nil {
		out.Samples = make([]DiagnosticSample, len(in.Samples))
		for i, sample := range in.Samples {
			sample.Issue = s.text(sample.Issue)
			sample.Transaction = scrubTelemetryIdentity(sample.Transaction)
			sample.DependencyType = scrubTelemetryIdentity(sample.DependencyType)
			sample.DependencyTarget = scrubTelemetryIdentity(sample.DependencyTarget)
			if sample.CorrelationIDs != nil {
				sample.CorrelationIDs = make([]string, len(sample.CorrelationIDs))
				for j, id := range in.Samples[i].CorrelationIDs {
					sample.CorrelationIDs[j] = scrubTelemetryIdentity(id)
				}
			}
			out.Samples[i] = sample
		}
	}
	if out.Telemetry == nil && fallbackTel != nil {
		out.Telemetry = cloneTelemetryHealth(fallbackTel)
	}
	out.Telemetry = scrubLiveTelemetryHealth(out.Telemetry, s)
	return &out
}

func scrubLiveTelemetry(live *LiveMetrics, s telemetryTextScrubber) {
	if live == nil {
		return
	}
	if live.Telemetry != nil {
		if len(live.Telemetry.ValidityFindings) == 0 && !live.Telemetry.restoredFromHistory && (live.Telemetry.Coverage != nil || len(live.Telemetry.StageDistributions) > 0 || live.Telemetry.RenderPopulation != nil) {
			live.Telemetry.ValidityFindings = buildLiveTelemetryValidityFindings(live.Telemetry, live.TelemetryStatus)
		}
		live.Telemetry = scrubLiveTelemetryHealth(live.Telemetry, s)
	}
	if live.TelemetryStatus != nil {
		live.TelemetryStatus = scrubTelemetryRunHealth(live.TelemetryStatus, s)
	}
	if live.TelemetryPolicy != nil {
		copied := *live.TelemetryPolicy
		copied.Availability = scrubTelemetryIdentity(copied.Availability)
		copied.Policy = scrubTelemetryBudgetPolicy(copied.Policy)
		live.TelemetryPolicy = &copied
	}
}

func scrubTelemetryEvidenceDocument(doc *TelemetryEvidenceDocument, s telemetryTextScrubber) *TelemetryEvidenceDocument {
	if doc == nil {
		return nil
	}
	out := *doc
	out.SchemaVersion = scrubTelemetryIdentity(doc.SchemaVersion)
	out.TestID = scrubTelemetryIdentity(doc.TestID)
	out.CoverageAlgorithm = scrubTelemetryIdentity(doc.CoverageAlgorithm)
	out.CoverageRule = scrubTelemetryIdentity(doc.CoverageRule)
	if doc.Coverage != nil {
		tmp := &RunArchiveManifest{TelemetryCoverage: doc.Coverage}
		out.Coverage = scrubTelemetryText(tmp, s).TelemetryCoverage
	}
	out.PolicySegments = scrubTelemetryPolicySegments(doc.PolicySegments, s)
	if doc.SegmentCoverage != nil {
		out.SegmentCoverage = make([]TelemetryPolicySegment, len(doc.SegmentCoverage))
		for i, seg := range doc.SegmentCoverage {
			seg.Unavailable, seg.PolicyHash = scrubTelemetryIdentity(seg.Unavailable), scrubTelemetryIdentity(seg.PolicyHash)
			out.SegmentCoverage[i] = seg
		}
	}
	if doc.Intervals != nil {
		out.Intervals = make([]TelemetryEvidenceInterval, len(doc.Intervals))
		for i, iv := range doc.Intervals {
			iv.PolicyHash = scrubTelemetryIdentity(iv.PolicyHash)
			iv.Status = scrubTelemetryIdentity(iv.Status)
			if iv.ConditionLabels != nil {
				iv.ConditionLabels = make([]string, len(iv.ConditionLabels))
				for j, lbl := range doc.Intervals[i].ConditionLabels {
					iv.ConditionLabels[j] = scrubTelemetryIdentity(lbl)
				}
			}
			if iv.Unscored != nil {
				iv.Unscored = make([]string, len(iv.Unscored))
				for j, u := range doc.Intervals[i].Unscored {
					iv.Unscored[j] = s.text(u)
				}
			}
			out.Intervals[i] = iv
		}
	}
	if doc.ConditionLabelCounts != nil {
		out.ConditionLabelCounts = make(map[string]int, len(doc.ConditionLabelCounts))
		for _, k := range slices.Sorted(maps.Keys(doc.ConditionLabelCounts)) {
			out.ConditionLabelCounts[scrubTelemetryIdentity(k)] += doc.ConditionLabelCounts[k]
		}
	}
	if doc.ResourceSamples != nil {
		out.ResourceSamples = make([]TelemetryResourceSample, len(doc.ResourceSamples))
		for i, sample := range doc.ResourceSamples {
			sample.Component = scrubTelemetryIdentity(sample.Component)
			sample.Source = scrubTelemetryIdentity(sample.Source)
			out.ResourceSamples[i] = sample
		}
	}
	if pop := doc.RenderPopulation.clone(); pop != nil {
		scrubTelemetryRenderPopulation(pop)
		out.RenderPopulation = pop
	}
	out.StageDistributions = scrubTelemetryStageDistributions(doc.StageDistributions, s)
	out.ValidityFindings = scrubTelemetryValidityFindings(doc.ValidityFindings, s)
	out.Acceptance = scrubTelemetryVerdict(doc.Acceptance, s)
	return &out
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
