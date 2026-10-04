package salesforce

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func nativeReferenceFixture() (*dsl.Script, map[int]*actionRef) {
	script := &dsl.Script{Name: "synthetic", Protocol: "http", Actions: []dsl.Action{{Type: "GET", Target: "/producer", Metadata: map[string]any{"salesforce": map[string]any{"sequence": 1, "har_match_id": 1}}}, {Type: "POST", Target: "/consumer", Metadata: map[string]any{"salesforce": map[string]any{"sequence": 2, "har_match_id": 2}}}}}
	refs := map[int]*actionRef{1: {Sequence: 1, ActionPath: "actions[0]", Action: &script.Actions[0]}, 2: {Sequence: 2, ActionPath: "actions[1]", Action: &script.Actions[1]}}
	return script, refs
}

func TestNativeReferencesCountUnmappedBeforeFilteringAndRequireExactJoins(t *testing.T) {
	script, refs := nativeReferenceFixture()
	mapped := correlate.Correlation{Name: "synthetic_token", Source: "body", Expression: "$.token", FoundInID: 1, UsedInIDs: []int{2, 999}}
	all := []correlate.Correlation{mapped, {Name: "missing_producer", FoundInID: 998, UsedInIDs: []int{2}}}
	preview := newNativeReferencePreview(script, all, []correlate.Correlation{mapped}, refs, map[string]correlationApplicationStatus{mapped.Name: {preApplied: true}})
	source := &sourceview.Review{Mappings: []sourceview.Mapping{{ID: "producer", SourceFile: "synthetic.har", TargetPath: "actions[0]"}, {ID: "consumer", SourceFile: "synthetic.har", TargetPath: "actions[1]"}}}
	BindNativePreviewReferences(preview, source, script, FinalNativePathDomain)
	c := preview.CorrelationCoverage
	if c.DiscoveredCorrelations != 2 || c.ImportMatchedProducerCorrelations != 1 || c.ImportUnmatchedProducerCorrelations != 1 || c.ObservedConsumerReferences != 3 || c.ImportMatchedConsumerReferences != 2 || c.ImportUnmatchedConsumerReferences != 1 || c.PreAppliedCorrelations != 1 || c.ManualCorrelations != 1 || c.SourceLinkedProducerCorrelations != 1 || c.SourceLinkedConsumerReferences != 2 || c.NativeMappedConsumerReferences != 2 || c.OmittedCorrelations != 1 || c.OmittedConsumerReferences != 1 || c.DetailsComplete {
		t.Fatalf("full reference accounting lost unmapped evidence: %+v", c)
	}
	row := preview.Correlations[0]
	if row.ProducerReference.SourceMappingID != "producer" || row.ProducerReference.OriginalActionPath != "actions[0]" || row.ProducerReference.NativeActionPath != "actions[0]" || row.ConsumerReferences[1].HAREntryID != 999 || row.ConsumerReferences[1].OriginalActionPath != "" {
		t.Fatal("exact mapped or missing source reference changed")
	}
	for _, mappings := range [][]sourceview.Mapping{
		{{ID: "producer", SourceFile: "synthetic.har", TargetPath: "actions[0]"}, {ID: "another", SourceFile: "synthetic.har", TargetPath: "actions[0]"}},
		{{ID: "duplicate", SourceFile: "synthetic.har", TargetPath: "actions[0]"}, {ID: "duplicate", SourceFile: "synthetic.har", TargetPath: "actions[1]"}},
	} {
		BindNativePreviewReferences(preview, &sourceview.Review{Mappings: mappings}, script, FinalNativePathDomain)
		if preview.CorrelationCoverage.SourceLinkedProducerCorrelations != 0 || preview.Correlations[0].ProducerReference.SourceMappingID != "" {
			t.Fatal("ambiguous path or ID became a verified source link")
		}
	}
	duplicate := script.Actions[0]
	script.Actions = append(script.Actions, duplicate)
	BindNativePreviewReferences(preview, source, script, FinalNativePathDomain)
	if preview.Correlations[0].ProducerReference.NativeActionPath != "" || preview.CorrelationCoverage.NativeMappedProducerCorrelations != 0 {
		t.Fatal("duplicate native identity became a guessed path")
	}
	BindNativePreviewReferences(preview, nil, script, FinalNativePathDomain)
	if preview.CorrelationCoverage.SourceReviewAvailable || preview.Correlations[0].ProducerReference.SourceMappingID != "" {
		t.Fatal("unattached recorder HAR review manufactured source navigation")
	}
}

func TestNativeReferenceRetentionPreservesFullCountsAndActualSerializedBound(t *testing.T) {
	script, refs := nativeReferenceFixture()
	correlations := make([]correlate.Correlation, 3000)
	for i := range correlations {
		correlations[i] = correlate.Correlation{Name: fmt.Sprintf("synthetic_%d", i), Source: "body", Expression: strings.Repeat("<", 128), FoundInID: 1, UsedInIDs: []int{2}}
	}
	preview := newNativeReferencePreview(script, correlations, correlations, refs, nil)
	source := &sourceview.Review{Mappings: []sourceview.Mapping{{ID: strings.Repeat("<", 3500), SourceFile: "synthetic.har", TargetPath: "actions[0]"}, {ID: "consumer", SourceFile: "synthetic.har", TargetPath: "actions[1]"}}}
	BindNativePreviewReferences(preview, source, script, FinalNativePathDomain)
	c := preview.CorrelationCoverage
	encoded, err := json.Marshal(preview.Correlations)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > NativeCorrelationDetailByteLimit || len(preview.Correlations) > NativeCorrelationDetailLimit || c.RetainedConsumerReferences > NativeConsumerReferenceDetailLimit || c.DetailsComplete || c.DiscoveredCorrelations != 3000 || c.SourceLinkedProducerCorrelations != 3000 || c.NativeMappedProducerCorrelations != 3000 || c.ObservedConsumerReferences != 3000 || c.OmittedCorrelations != 3000-len(preview.Correlations) || c.OmittedConsumerReferences != 3000-c.RetainedConsumerReferences {
		t.Fatal("bounded retained details changed full inspected totals or serialized ceiling")
	}
	wire, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "producerWeights") || strings.Contains(string(wire), "referenceCatalog") {
		t.Fatal("private rebind catalog was exported")
	}
	var reopened NativePreview
	if err := json.Unmarshal(wire, &reopened); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(reopened)
	BindNativePreviewReferences(&reopened, source, &dsl.Script{}, FinalNativePathDomain)
	after, _ := json.Marshal(reopened)
	if string(before) != string(after) {
		t.Fatal("reopened report manufactured destination verification without trusted catalog")
	}
}

func TestNativeCatalogLimitKeepsOriginalHAREntryOrdinal(t *testing.T) {
	script, refs := nativeReferenceFixture()
	all := make([]correlate.Correlation, maxNativeReferenceIdentities+1)
	for i := range all {
		all[i] = correlate.Correlation{Name: fmt.Sprintf("omitted_%d", i), FoundInID: 100 + i}
	}
	mapped := correlate.Correlation{Name: "retained", FoundInID: 1, UsedInIDs: []int{987654}}
	all = append(all, mapped)
	preview := newNativeReferencePreview(script, all, []correlate.Correlation{mapped}, refs, nil)
	BindNativePreviewReferences(preview, nil, script, FinalNativePathDomain)
	if preview.CorrelationCoverage.Complete || !preview.CorrelationCoverage.CatalogLimited || len(preview.Correlations) != 1 || preview.Correlations[0].ProducerReference.HAREntryID != 1 || preview.Correlations[0].ConsumerReferences[0].HAREntryID != 987654 || preview.Correlations[0].ConsumerReferences[0].NativeActionPath != "" {
		t.Fatal("limited catalog lost original ordinals or implied complete coverage")
	}
}

func TestNativeReferencesResolveNestedControlAndHTTPSequenceWithoutRewritingSource(t *testing.T) {
	script, refs := nativeReferenceFixture()
	first, second := script.Actions[0], script.Actions[1]
	script.Actions = []dsl.Action{{Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{first, second}}}}}}}
	refs[1].ActionPath = "actions[0].control.steps[0].params.requests[0]"
	refs[2].ActionPath = "actions[0].control.steps[0].params.requests[1]"
	c := correlate.Correlation{Name: "synthetic", FoundInID: 1, UsedInIDs: []int{2}}
	before, _ := json.Marshal(script)
	preview := newNativeReferencePreview(script, []correlate.Correlation{c}, []correlate.Correlation{c}, refs, nil)
	if preview.Correlations[0].ProducerReference.NativeActionPath != refs[1].ActionPath || preview.Correlations[0].ConsumerReferences[0].NativeActionPath != refs[2].ActionPath {
		t.Fatal("nested sequence identities did not resolve to their exact paths")
	}
	after, _ := json.Marshal(script)
	if string(before) != string(after) {
		t.Fatal("reference inspection changed the source script")
	}
}

func assertNativeReferencePath(t *testing.T, preview *NativePreview, harID int, original, native string) {
	t.Helper()
	source := &sourceview.Review{}
	for _, identity := range preview.referenceCatalog.identities {
		if identity.path != "" {
			source.Mappings = append(source.Mappings, sourceview.Mapping{ID: identity.path, SourceFile: "synthetic.har", TargetPath: identity.path})
		}
	}
	BindNativePreviewReferences(preview, source, preview.Script, FinalNativePathDomain)
	found := false
	for _, row := range preview.Correlations {
		for _, ref := range row.ConsumerReferences {
			if ref.HAREntryID == harID {
				found = true
				if ref.OriginalActionPath != original || ref.NativeActionPath != native || ref.SourceMappingID != original {
					t.Fatal("transformed request reused an old path or lost its exact original source link")
				}
			}
		}
	}
	if !found {
		t.Fatal("expected captured consumer reference missing")
	}
}

func TestNativeReferenceDetailWorkIsNotRecycledAcrossOversizedRows(t *testing.T) {
	script, refs := nativeReferenceFixture()
	refs[2].ActionPath = "actions[0]" + strings.Repeat(".control.steps[0]", 24)
	used := make([]int, NativeConsumerReferenceDetailLimit)
	for i := range used {
		used[i] = 2
	}
	correlations := make([]correlate.Correlation, 96)
	for i := range correlations {
		correlations[i] = correlate.Correlation{Name: fmt.Sprintf("synthetic_%d", i), FoundInID: 1, UsedInIDs: used}
	}
	preview := newNativeReferencePreview(script, correlations, correlations, refs, nil)
	if len(preview.referenceCatalog.rows) != 1 || len(preview.referenceCatalog.rows[0].ConsumerReferences) == 0 || len(preview.referenceCatalog.rows[0].ConsumerReferences) >= len(used) {
		t.Fatal("oversized detail did not retain a bounded partial first row")
	}
	coverage := preview.CorrelationCoverage
	if len(preview.Correlations) != 1 || coverage.DiscoveredCorrelations != len(correlations) || coverage.ObservedConsumerReferences != len(correlations)*len(used) || coverage.OmittedConsumerReferences != coverage.ObservedConsumerReferences-coverage.RetainedConsumerReferences || coverage.DetailsComplete {
		t.Fatal("detail admission recycled work or lost full aggregate/omission counts")
	}
	encoded, err := json.Marshal(preview.Correlations)
	if err != nil || len(encoded) > NativeCorrelationDetailByteLimit {
		t.Fatal("partial detail exceeded the actual serialized ceiling")
	}
}

func TestNativeReferenceReasonsAreAdmittedBeforeCopyAndEscapedEncoding(t *testing.T) {
	script, refs := nativeReferenceFixture()
	correlations := make([]correlate.Correlation, 96)
	for i := range correlations {
		correlations[i] = correlate.Correlation{Name: fmt.Sprintf("synthetic_%d", i), FoundInID: 1, UsedInIDs: []int{2}, Reasons: []string{strings.Repeat("<", NativeCorrelationDetailByteLimit/6)}}
	}
	correlations = append(correlations, correlate.Correlation{Name: "small_later_row", FoundInID: 1, UsedInIDs: []int{2}})
	preview := newNativeReferencePreview(script, correlations, correlations, refs, nil)
	coverage := preview.CorrelationCoverage
	if len(preview.referenceCatalog.rows) != 0 || len(preview.Correlations) != 0 || coverage.DiscoveredCorrelations != len(correlations) || coverage.ObservedConsumerReferences != len(correlations) || coverage.OmittedCorrelations != len(correlations) || coverage.OmittedConsumerReferences != len(correlations) || coverage.DetailsComplete {
		t.Fatal("oversized escaped reason recycled detail work or lost full counts")
	}
}

func TestNativeFinalSourceLinksHaveOneSharedDetailWorkBudget(t *testing.T) {
	script, refs := nativeReferenceFixture()
	used := make([]int, NativeConsumerReferenceDetailLimit)
	for i := range used {
		used[i] = 2
	}
	correlations := []correlate.Correlation{{Name: "many_references", FoundInID: 1, UsedInIDs: used}, {Name: "later_reference", FoundInID: 1, UsedInIDs: []int{2}}}
	preview := newNativeReferencePreview(script, correlations, correlations, refs, nil)
	source := &sourceview.Review{Mappings: []sourceview.Mapping{{ID: "producer", SourceFile: "synthetic.har", TargetPath: refs[1].ActionPath}, {ID: strings.Repeat("<", 3500), SourceFile: "synthetic.har", TargetPath: refs[2].ActionPath}}}
	BindNativePreviewReferences(preview, source, script, FinalNativePathDomain)
	coverage := preview.CorrelationCoverage
	if len(preview.Correlations) != 1 || len(preview.Correlations[0].ConsumerReferences) == 0 || len(preview.Correlations[0].ConsumerReferences) >= len(preview.referenceCatalog.rows[0].ConsumerReferences) || coverage.SourceLinkedConsumerReferences != len(used)+1 || coverage.ObservedConsumerReferences != len(used)+1 || coverage.OmittedConsumerReferences != coverage.ObservedConsumerReferences-coverage.RetainedConsumerReferences || coverage.DetailsComplete {
		t.Fatal("expanded source links recycled work or changed full verified-link counts")
	}
	encoded, err := json.Marshal(preview.Correlations)
	if err != nil || len(encoded) > NativeCorrelationDetailByteLimit {
		t.Fatal("expanded source links exceeded the actual serialized ceiling")
	}
}
