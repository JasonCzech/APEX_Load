package correlate

// RelationshipCoverage describes observation coverage for detected producer
// bindings and their recorded consumer links. Complete does not prove correct
// runtime extraction, successful correlation, or successful replay.
const maxRelationshipDetails = 256

type RelationshipState string

const (
	RelationshipProducerMissing      RelationshipState = "producer_missing"
	RelationshipExtractorUnavailable RelationshipState = "extractor_unavailable"
	RelationshipConsumerMissing      RelationshipState = "consumer_missing"
	RelationshipProducerUnchanged    RelationshipState = "producer_unchanged"
	RelationshipStaleLiteral         RelationshipState = "stale_literal"
	RelationshipNoStaleLiteral       RelationshipState = "no_stale_literal"
	RelationshipManualEncoding       RelationshipState = "manual_encoding"
	RelationshipManualSurface        RelationshipState = "manual_surface"
)

type RelationshipDetail struct {
	RuleID             string            `json:"rule_id,omitempty"`
	ProducerStepID     int               `json:"producer_step_id"`
	ConsumerStepID     int               `json:"consumer_step_id"`
	State              RelationshipState `json:"state"`
	ProducerObserved   bool              `json:"producer_observed"`
	ExtractorAvailable bool              `json:"extractor_available"`
	ConsumerObserved   bool              `json:"consumer_observed"`
}

type RelationshipCoverage struct {
	Complete              bool                      `json:"complete"`
	ProducerCount         int                       `json:"producer_count"`
	ConsumerCount         int                       `json:"consumer_count"`
	MissingProducers      int                       `json:"missing_producers"`
	UnavailableExtractors int                       `json:"unavailable_extractors"`
	MissingConsumers      int                       `json:"missing_consumers"`
	StateCounts           map[RelationshipState]int `json:"state_counts"`
	DetailsComplete       bool                      `json:"details_complete"`
	DetailsOmitted        int                       `json:"details_omitted"`
	DetailLimit           int                       `json:"detail_limit"`
	Relationships         []RelationshipDetail      `json:"relationships"`
}

type ReplayDiagnosis struct {
	Suggestions          []RepairSuggestion
	RelationshipCoverage RelationshipCoverage
}

var relationshipRuleIDs = func() map[string]bool {
	ids := make(map[string]bool)
	for _, rule := range RuleCatalog() {
		ids[rule.ID] = true
	}
	return ids
}()

func newRelationshipCoverage(recorded []Observation, replay map[int]Observation) RelationshipCoverage {
	coverage := RelationshipCoverage{
		Complete: true, DetailsComplete: true, DetailLimit: maxRelationshipDetails,
		StateCounts:   make(map[RelationshipState]int),
		Relationships: make([]RelationshipDetail, 0),
	}
	for _, observation := range recorded {
		if _, exists := replay[observation.ID]; !exists {
			coverage.Complete = false
		}
	}
	return coverage
}

func (coverage *RelationshipCoverage) record(c Correlation, usedID int, state RelationshipState, producer, extractor, consumer bool) {
	coverage.ConsumerCount++
	coverage.StateCounts[state]++
	if !consumer {
		coverage.MissingConsumers++
		coverage.Complete = false
	}
	if len(coverage.Relationships) >= coverage.DetailLimit {
		coverage.DetailsOmitted++
		coverage.DetailsComplete = false
		return
	}
	ruleID := c.RuleID
	if !relationshipRuleIDs[ruleID] {
		ruleID = ""
	}
	coverage.Relationships = append(coverage.Relationships, RelationshipDetail{
		RuleID: ruleID, ProducerStepID: c.FoundInID, ConsumerStepID: usedID,
		State: state, ProducerObserved: producer, ExtractorAvailable: extractor, ConsumerObserved: consumer,
	})
}

func missingCorrelationConsumerSuggestion(sourceID, usedID int) RepairSuggestion {
	return RepairSuggestion{
		RuleID: "replay_consumer_missing", Severity: SeverityWarning,
		Title:        "Recorded consumer was not observed during replay",
		Detail:       "A recorded consumer request was absent from the replay evidence, so its dynamic value use could not be inspected.",
		SourceStepID: sourceID, FailingStepID: usedID,
		Suggestion: "Capture the complete replay flow and inspect this consumer before applying a correlation repair.",
		Confidence: 0.4, ConfidenceBand: confidenceBand(0.4), ReviewAction: "manual_review_required",
	}
}
