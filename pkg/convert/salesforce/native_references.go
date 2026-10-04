package salesforce

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

const (
	NativePreviewPathDomain            = "native_preview"
	FinalNativePathDomain              = "final_native"
	NativeCorrelationDetailLimit       = 2048
	NativeConsumerReferenceDetailLimit = 2048
	NativeCorrelationDetailByteLimit   = 256 << 10
	maxNativeReferenceIdentities       = 8192
	maxNativeReferencePathBytes        = 8 << 20
	maxNativeReferenceFieldBytes       = 4096
)

// NativeRequestReference describes captured and imported requests. Native paths
// identify the script AT IMPORT, never a verified destination in an edited draft.
type NativeRequestReference struct {
	HAREntryID         int    `json:"har_entry_id"`
	OriginalActionPath string `json:"original_action_path,omitempty"`
	SourceMappingID    string `json:"source_mapping_id,omitempty"`
	SourceFile         string `json:"source_file,omitempty"`
	NativeActionPath   string `json:"native_action_path,omitempty"`
}

// Complete covers bounded captured correlation inspection and joins. It does
// not establish session freshness, bootstrap portability or successful replay.
type NativeCorrelationCoverage struct {
	Complete                            bool `json:"complete"`
	DetailsComplete                     bool `json:"details_complete"`
	CatalogLimited                      bool `json:"catalog_limited,omitempty"`
	DiscoveredCorrelations              int  `json:"discovered_correlations"`
	ImportMatchedProducerCorrelations   int  `json:"import_matched_producer_correlations"`
	ImportUnmatchedProducerCorrelations int  `json:"import_unmatched_producer_correlations"`
	ObservedConsumerReferences          int  `json:"observed_consumer_references"`
	ImportMatchedConsumerReferences     int  `json:"import_matched_consumer_references"`
	ImportUnmatchedConsumerReferences   int  `json:"import_unmatched_consumer_references"`
	SourceReviewAvailable               bool `json:"source_review_available"`
	SourceLinkedProducerCorrelations    int  `json:"source_linked_producer_correlations"`
	SourceUnlinkedProducerCorrelations  int  `json:"source_unlinked_producer_correlations"`
	SourceLinkedConsumerReferences      int  `json:"source_linked_consumer_references"`
	SourceUnlinkedConsumerReferences    int  `json:"source_unlinked_consumer_references"`
	NativeMappedProducerCorrelations    int  `json:"native_mapped_producer_correlations"`
	NativeUnmappedProducerCorrelations  int  `json:"native_unmapped_producer_correlations"`
	NativeMappedConsumerReferences      int  `json:"native_mapped_consumer_references"`
	NativeUnmappedConsumerReferences    int  `json:"native_unmapped_consumer_references"`
	PreAppliedCorrelations              int  `json:"pre_applied_correlations"`
	ManualCorrelations                  int  `json:"manual_correlations"`
	RetainedCorrelations                int  `json:"retained_correlations"`
	OmittedCorrelations                 int  `json:"omitted_correlations"`
	RetainedConsumerReferences          int  `json:"retained_consumer_references"`
	OmittedConsumerReferences           int  `json:"omitted_consumer_references"`
	CorrelationDetailLimit              int  `json:"correlation_detail_limit"`
	ConsumerReferenceDetailLimit        int  `json:"consumer_reference_detail_limit"`
	DetailByteLimit                     int  `json:"detail_byte_limit"`
}

type nativeReferenceIdentity struct {
	path     string
	sequence int
}

// Weights preserve full aggregates without retaining a producer/consumer row for
// every relationship. The private catalog contains no values, bodies or IDs from
// Aura payloads, and is never serialized or recovered from browser JSON.
type nativeReferenceCatalog struct {
	identities      map[int]nativeReferenceIdentity
	producerWeights map[int]int
	consumerWeights map[int]int
	rows            []CorrelationCandidate
	base            NativeCorrelationCoverage
}

func newNativeReferencePreview(script *dsl.Script, all, mapped []correlate.Correlation, refs map[int]*actionRef, applications map[string]correlationApplicationStatus) *NativePreview {
	catalog := &nativeReferenceCatalog{identities: map[int]nativeReferenceIdentity{}, producerWeights: map[int]int{}, consumerWeights: map[int]int{}}
	catalog.base = NativeCorrelationCoverage{Complete: true, DiscoveredCorrelations: len(all), CorrelationDetailLimit: NativeCorrelationDetailLimit, ConsumerReferenceDetailLimit: NativeConsumerReferenceDetailLimit, DetailByteLimit: NativeCorrelationDetailByteLimit}
	pathBytes := 0
	addIdentity := func(id int) bool {
		if _, exists := catalog.identities[id]; exists {
			return true
		}
		if len(catalog.identities) >= maxNativeReferenceIdentities {
			catalog.base.CatalogLimited = true
			catalog.base.Complete = false
			return false
		}
		identity := nativeReferenceIdentity{}
		if ref := refs[id]; ref != nil {
			identity.sequence = ref.Sequence
			if len(ref.ActionPath) <= maxNativeReferenceFieldBytes && len(ref.ActionPath) <= maxNativeReferencePathBytes-pathBytes {
				identity.path = ref.ActionPath
				pathBytes += len(identity.path)
			} else {
				catalog.base.CatalogLimited = true
				catalog.base.Complete = false
			}
		}
		catalog.identities[id] = identity
		return true
	}
	for _, corr := range all {
		if refs[corr.FoundInID] != nil {
			catalog.base.ImportMatchedProducerCorrelations++
		}
		if addIdentity(corr.FoundInID) {
			catalog.producerWeights[corr.FoundInID]++
		}
		for _, id := range corr.UsedInIDs {
			catalog.base.ObservedConsumerReferences++
			if refs[id] != nil {
				catalog.base.ImportMatchedConsumerReferences++
			}
			if addIdentity(id) {
				catalog.consumerWeights[id]++
			}
		}
	}
	catalog.base.ImportUnmatchedProducerCorrelations = len(all) - catalog.base.ImportMatchedProducerCorrelations
	catalog.base.ImportUnmatchedConsumerReferences = catalog.base.ObservedConsumerReferences - catalog.base.ImportMatchedConsumerReferences
	for _, corr := range mapped {
		if applications[corr.Name].preApplied {
			catalog.base.PreAppliedCorrelations++
		}
	}
	catalog.base.ManualCorrelations = len(all) - catalog.base.PreAppliedCorrelations
	// Keep raw retained detail bounded too. Final source/native links can increase
	// serialized cost, so binding applies the exact byte ceiling again.
	bytesUsed, consumerAttempts, rowAttempts := 2, 0, 0
	detailWork := nativeDetailBudget{remaining: NativeCorrelationDetailByteLimit - 2}
	for _, corr := range mapped {
		if rowAttempts >= NativeCorrelationDetailLimit || bytesUsed >= NativeCorrelationDetailByteLimit || detailWork.remaining == 0 {
			break
		}
		ref := refs[corr.FoundInID]
		if ref == nil {
			continue
		}
		rowAttempts++
		application := applications[corr.Name]
		reviewAction := application.reviewAction
		if !application.preApplied && reviewAction == "" {
			reviewAction = "manual_review_required"
		}
		row := CorrelationCandidate{Name: corr.Name, Source: corr.Source, Expression: corr.Expression, FoundInSeq: ref.Sequence, RuleID: corr.RuleID, Confidence: corr.Confidence, Reasons: corr.Reasons, PreApplied: application.preApplied, ReviewAction: reviewAction, ProducerReference: &NativeRequestReference{HAREntryID: corr.FoundInID, OriginalActionPath: catalog.identities[corr.FoundInID].path}}
		// Charge attempted detail work before copying or encoding it. This budget
		// is never refunded when an invalid or oversized row is not retained.
		if !detailWork.takeRow(row) || !detailWork.takeReference(*row.ProducerReference, false) {
			break
		}
		row.Reasons = append([]string(nil), row.Reasons...)
		for _, id := range corr.UsedInIDs {
			if consumerAttempts >= NativeConsumerReferenceDetailLimit {
				break
			}
			consumerAttempts++
			consumer := NativeRequestReference{HAREntryID: id, OriginalActionPath: catalog.identities[id].path}
			used := refs[id]
			if !detailWork.takeReference(consumer, used != nil) {
				break
			}
			row.ConsumerReferences = append(row.ConsumerReferences, consumer)
			if used != nil {
				row.UsedInSeqs = append(row.UsedInSeqs, used.Sequence)
			}
		}
		row.ConsumerReferencesOmitted = len(corr.UsedInIDs) - len(row.ConsumerReferences)
		encoded, err := json.Marshal(row)
		if err != nil || len(encoded)+1 > NativeCorrelationDetailByteLimit-bytesUsed {
			continue
		}
		bytesUsed += len(encoded) + 1
		catalog.rows = append(catalog.rows, row)
	}
	preview := &NativePreview{Script: script, referenceCatalog: catalog}
	BindNativePreviewReferences(preview, nil, script, NativePreviewPathDomain)
	return preview
}

// BindNativePreviewReferences rebuilds joins after the LAST executable transform.
// It only operates on the trusted in-memory builder catalog; reopened JSON lacks
// that catalog and cannot manufacture new verified paths or mapping identities.
func BindNativePreviewReferences(preview *NativePreview, source *sourceview.Review, script *dsl.Script, domain string) {
	if preview == nil || preview.referenceCatalog == nil {
		return
	}
	catalog := preview.referenceCatalog
	coverage := catalog.base
	coverage.SourceReviewAvailable = source != nil
	preview.NativePathDomain = domain
	if domain != NativePreviewPathDomain && domain != FinalNativePathDomain {
		preview.NativePathDomain = ""
		coverage.Complete = false
	}
	type mappingMatch struct {
		count   int
		mapping sourceview.Mapping
	}
	mappingByPath, mappingIDCounts := map[string]mappingMatch{}, map[string]int{}
	sourceBytes := 0
	sourceLimited := source != nil && len(source.Mappings) > 65536
	if source != nil && !sourceLimited {
		for _, mapping := range source.Mappings {
			cost := len(mapping.ID) + len(mapping.SourceFile) + len(mapping.TargetPath)
			if cost > 16<<20-sourceBytes {
				sourceLimited = true
				break
			}
			sourceBytes += cost
		}
	}
	if sourceLimited {
		coverage.CatalogLimited = true
		coverage.Complete = false
	} else if source != nil {
		for _, mapping := range source.Mappings {
			mappingIDCounts[mapping.ID]++
			match := mappingByPath[mapping.TargetPath]
			match.count++
			if match.count == 1 {
				match.mapping = mapping
			}
			mappingByPath[mapping.TargetPath] = match
		}
	}
	type pathMatch struct {
		count int
		path  string
	}
	nativePaths := map[[2]int]pathMatch{}
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if preview.NativePathDomain == "" {
			return nil
		}
		if ref.Action != nil {
			spec, exists := dsl.SpecForActionType(ref.Action.Type)
			httpRequest := strings.EqualFold(strings.TrimSpace(ref.Action.Type), "request") && nativeReferenceHTTPProtocol(script.Protocol)
			if exists && !strings.EqualFold(ref.Action.Type, "http_sequence") {
				for _, protocol := range spec.Protocols {
					if protocol == "http" {
						httpRequest = true
					}
				}
			}
			if !httpRequest {
				return nil
			}
		}
		metadata, _ := ref.Metadata()["salesforce"].(map[string]any)
		sequence, harID := nativeReferenceInt(metadata["sequence"]), nativeReferenceInt(metadata["har_match_id"])
		if sequence > 0 && harID > 0 && len(ref.Path) <= maxNativeReferenceFieldBytes {
			key := [2]int{sequence, harID}
			match := nativePaths[key]
			match.count++
			if match.count == 1 {
				match.path = ref.Path
			}
			nativePaths[key] = match
		}
		return nil
	})
	if err != nil {
		coverage.Complete = false
		clear(nativePaths)
	}
	bound := map[int]NativeRequestReference{}
	for id, identity := range catalog.identities {
		ref := NativeRequestReference{HAREntryID: id, OriginalActionPath: identity.path}
		match := mappingByPath[identity.path]
		mapping := match.mapping
		if identity.path != "" && match.count == 1 && mapping.ID != "" && mapping.SourceFile != "" && mappingIDCounts[mapping.ID] == 1 && len(mapping.ID)+len(mapping.SourceFile) <= maxNativeReferenceFieldBytes {
			ref.SourceMappingID = mapping.ID
			ref.SourceFile = mapping.SourceFile
		}
		paths := nativePaths[[2]int{identity.sequence, id}]
		if identity.sequence > 0 && paths.count == 1 {
			ref.NativeActionPath = paths.path
		}
		bound[id] = ref
		if ref.SourceMappingID != "" {
			coverage.SourceLinkedProducerCorrelations += catalog.producerWeights[id]
			coverage.SourceLinkedConsumerReferences += catalog.consumerWeights[id]
		}
		if ref.NativeActionPath != "" {
			coverage.NativeMappedProducerCorrelations += catalog.producerWeights[id]
			coverage.NativeMappedConsumerReferences += catalog.consumerWeights[id]
		}
	}
	coverage.SourceUnlinkedProducerCorrelations = coverage.DiscoveredCorrelations - coverage.SourceLinkedProducerCorrelations
	coverage.SourceUnlinkedConsumerReferences = coverage.ObservedConsumerReferences - coverage.SourceLinkedConsumerReferences
	coverage.NativeUnmappedProducerCorrelations = coverage.DiscoveredCorrelations - coverage.NativeMappedProducerCorrelations
	coverage.NativeUnmappedConsumerReferences = coverage.ObservedConsumerReferences - coverage.NativeMappedConsumerReferences
	retained := make([]CorrelationCandidate, 0, len(catalog.rows))
	bytesUsed := 2
	detailWork := nativeDetailBudget{remaining: NativeCorrelationDetailByteLimit - 2}
	for _, original := range catalog.rows {
		if detailWork.remaining == 0 {
			break
		}
		row := original
		producer, exists := bound[original.ProducerReference.HAREntryID]
		if !exists {
			producer.HAREntryID = original.ProducerReference.HAREntryID
		}
		row.ProducerReference = &producer
		row.ConsumerReferences = nil
		row.UsedInSeqs = nil
		if !detailWork.takeRow(row) || !detailWork.takeReference(producer, false) {
			break
		}
		for _, ref := range original.ConsumerReferences {
			consumer, exists := bound[ref.HAREntryID]
			if !exists {
				consumer.HAREntryID = ref.HAREntryID
			}
			sequence := catalog.identities[ref.HAREntryID].sequence
			if !detailWork.takeReference(consumer, sequence > 0) {
				break
			}
			row.ConsumerReferences = append(row.ConsumerReferences, consumer)
			if sequence > 0 {
				row.UsedInSeqs = append(row.UsedInSeqs, sequence)
			}
		}
		row.ConsumerReferencesOmitted += len(original.ConsumerReferences) - len(row.ConsumerReferences)
		encoded, err := json.Marshal(row)
		if err != nil || len(encoded)+1 > NativeCorrelationDetailByteLimit-bytesUsed {
			continue
		}
		bytesUsed += len(encoded) + 1
		retained = append(retained, row)
		coverage.RetainedConsumerReferences += len(row.ConsumerReferences)
	}
	coverage.RetainedCorrelations = len(retained)
	coverage.OmittedCorrelations = coverage.DiscoveredCorrelations - coverage.RetainedCorrelations
	coverage.OmittedConsumerReferences = coverage.ObservedConsumerReferences - coverage.RetainedConsumerReferences
	coverage.DetailsComplete = coverage.OmittedCorrelations == 0 && coverage.OmittedConsumerReferences == 0 && !coverage.CatalogLimited
	preview.Correlations = retained
	preview.CorrelationCoverage = &coverage
}

func nativeReferenceInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		if v > 0 && v <= float64(1<<30) && v == float64(int(v)) {
			return int(v)
		}
	case json.Number:
		n, err := strconv.Atoi(string(v))
		if err == nil {
			return n
		}
	}
	return 0
}

// nativeDetailBudget is an admission-work bound, separate from retained JSON
// bytes. Fixed overheads exceed this schema's punctuation, booleans and numeric
// fields; strings charge their JSON-escaped bytes before any copy or marshal.
// Failed admission consumes the remainder, so expensive work cannot be recycled.
type nativeDetailBudget struct{ remaining int }

func (budget *nativeDetailBudget) take(n int) bool {
	if n > budget.remaining {
		budget.remaining = 0
		return false
	}
	budget.remaining -= n
	return true
}

func (budget *nativeDetailBudget) takeString(value string) bool {
	if len(value) > budget.remaining || !budget.take(2) {
		budget.remaining = 0
		return false
	}
	for i := 0; i < len(value); {
		size := 1
		switch value[i] {
		case '"', '\\', '\n', '\r', '\t', '\b', '\f':
			size = 2
		case '<', '>', '&':
			size = 6
		default:
			if value[i] < 0x20 {
				size = 6
			} else if value[i] >= utf8.RuneSelf {
				r, width := utf8.DecodeRuneInString(value[i:])
				if r == utf8.RuneError && width == 1 {
					size = 6
				} else {
					size = width
					if r == '\u2028' || r == '\u2029' {
						size = 6
					}
					if !budget.take(size) {
						return false
					}
					i += width
					continue
				}
			}
		}
		if !budget.take(size) {
			return false
		}
		i++
	}
	return true
}

func (budget *nativeDetailBudget) takeRow(row CorrelationCandidate) bool {
	if !budget.take(512) {
		return false
	}
	for _, value := range []string{row.Name, row.Source, row.Expression, row.RuleID, row.ReviewAction} {
		if !budget.takeString(value) {
			return false
		}
	}
	for _, reason := range row.Reasons {
		if !budget.take(1) || !budget.takeString(reason) {
			return false
		}
	}
	return true
}

func (budget *nativeDetailBudget) takeReference(ref NativeRequestReference, sequence bool) bool {
	if !budget.take(160) {
		return false
	}
	if sequence && !budget.take(24) {
		return false
	}
	for _, value := range []string{ref.OriginalActionPath, ref.SourceMappingID, ref.SourceFile, ref.NativeActionPath} {
		if !budget.takeString(value) {
			return false
		}
	}
	return true
}

// The HAR/sample importer uses the generic request action with an explicit
// method. Its protocol comes from the enclosing script, not the action catalog.
func nativeReferenceHTTPProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "http":
		return true
	}
	return false
}
