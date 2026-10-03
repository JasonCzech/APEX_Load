package correlate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestReplayRelationshipCoverageSeparatesStaleAndNoStaleEvidence(t *testing.T) {
	recorded := append(sampleRecording(), Observation{ID: 3, RequestBody: sampleRecording()[1].RequestBody})
	replay := []Observation{{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-NEW-123456-replay"}`}, {ID: 2, RequestBody: recorded[1].RequestBody}, {ID: 3, RequestBody: "csrf=TOKEN-NEW-123456-replay", ResponseStatus: 204}}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, replay)
	if err != nil {
		t.Fatal(err)
	}
	coverage := diagnosis.RelationshipCoverage
	if !coverage.Complete || coverage.ProducerCount != 1 || coverage.ConsumerCount != 2 || coverage.StateCounts[RelationshipStaleLiteral] != 1 || coverage.StateCounts[RelationshipNoStaleLiteral] != 1 || len(diagnosis.Suggestions) != 1 {
		t.Fatal("rotated producer/consumer evidence was not accounted for independently")
	}
	for _, row := range coverage.Relationships {
		if row.ProducerStepID != 1 || !row.ProducerObserved || !row.ExtractorAvailable || !row.ConsumerObserved {
			t.Fatal("relationship lost its observed producer/extractor/consumer proof")
		}
	}
	wire, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"TOKEN-AAA", "TOKEN-NEW", "$.csrf", "corr_csrf"} {
		if strings.Contains(string(wire), raw) {
			t.Fatal("relationship coverage disclosed source-derived content")
		}
	}
}

func TestReplayRelationshipCoverageMissingEvidenceAndUnchangedProducer(t *testing.T) {
	recorded := append(sampleRecording(), Observation{ID: 3, RequestBody: sampleRecording()[1].RequestBody})
	tests := []struct {
		name                                       string
		replay                                     []Observation
		producers, extractors, consumers, warnings int
		state                                      RelationshipState
	}{
		{"missing producer", []Observation{{ID: 2}, {ID: 3}}, 1, 0, 0, 1, RelationshipProducerMissing},
		{"extractor absent", []Observation{{ID: 1, ResponseBody: `{"ordinary":"safe"}`}, {ID: 2}, {ID: 3}}, 0, 1, 0, 1, RelationshipExtractorUnavailable},
		{"consumer missing with unchanged producer", []Observation{recorded[0], recorded[1]}, 0, 0, 1, 1, RelationshipConsumerMissing},
		{"both producer and consumers missing", nil, 1, 0, 2, 3, RelationshipConsumerMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnosis, err := DiagnoseReplayReviewChecked(recorded, test.replay)
			if err != nil {
				t.Fatal(err)
			}
			coverage := diagnosis.RelationshipCoverage
			if coverage.Complete || coverage.ProducerCount != 1 || coverage.ConsumerCount != 2 || coverage.MissingProducers != test.producers || coverage.UnavailableExtractors != test.extractors || coverage.MissingConsumers != test.consumers || len(diagnosis.Suggestions) != test.warnings {
				t.Fatal("missing observation/extractor coverage was hidden or warning duplicated")
			}
			if coverage.StateCounts[test.state] == 0 {
				t.Fatal("missing relationship state was not retained")
			}
			for _, suggestion := range diagnosis.Suggestions {
				if suggestion.Severity != SeverityWarning {
					t.Fatal("missing evidence invented an automatic repair")
				}
			}
		})
	}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, recorded)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosis.RelationshipCoverage.Complete || diagnosis.RelationshipCoverage.StateCounts[RelationshipProducerUnchanged] != 2 || len(diagnosis.Suggestions) != 0 {
		t.Fatal("unchanged producer was treated as proven dynamic correlation")
	}
}

func TestReplayRelationshipCoverageAmbiguityAndManualWirePolicies(t *testing.T) {
	recorded := []Observation{{ID: 1, ResponseBody: "aura.context=" + url.QueryEscape(`{"fwuid":"FWUID-RECORDED-123456"}`)}, {ID: 2, RequestBody: "fwuid=FWUID-RECORDED-123456"}}
	replay := []Observation{{ID: 1, ResponseBody: "aura.context=" + url.QueryEscape(`{"fwuid":"FIRST-REPLAY-123456","fwuid":"SECOND-REPLAY-654321"}`)}, {ID: 2, RequestBody: recorded[1].RequestBody}}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, replay)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RelationshipCoverage.Complete || diagnosis.RelationshipCoverage.UnavailableExtractors != 1 || diagnosis.RelationshipCoverage.StateCounts[RelationshipExtractorUnavailable] != 1 || len(diagnosis.Suggestions) != 1 {
		t.Fatal("ambiguous extractor identity was presented as available")
	}
	old, fresh := "token /+ value", "rotated /+ value"
	for _, test := range []struct {
		name        string
		body        string
		transformed bool
		state       RelationshipState
	}{
		{"form spelling", "token=" + strings.ReplaceAll(url.QueryEscape(old), "+", "%20"), false, RelationshipManualEncoding},
		{"transformed body", "token=" + old, true, RelationshipManualSurface},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorded := []Observation{{ID: 1, ResponseBody: `{"csrf_token":"` + old + `"}`}, {ID: 2, RequestBody: test.body}}
			replay := []Observation{{ID: 1, ResponseBody: `{"csrf_token":"` + fresh + `"}`}, {ID: 2, RequestBody: test.body, RequestBodyTransformed: test.transformed}}
			diagnosis, err := DiagnoseReplayReviewChecked(recorded, replay)
			if err != nil {
				t.Fatal(err)
			}
			if !diagnosis.RelationshipCoverage.Complete || diagnosis.RelationshipCoverage.StateCounts[test.state] != 1 || len(diagnosis.Suggestions) != 1 || diagnosis.Suggestions[0].ReviewAction != "manual_value_replacement_required" {
				t.Fatal("unsupported wire repair was not explicitly manual")
			}
		})
	}
}

func TestReplayRelationshipCoverageCapsDetailsWithoutLosingCounts(t *testing.T) {
	recorded := []Observation{sampleRecording()[0]}
	replay := []Observation{sampleRecording()[0]}
	for index := 0; index < maxRelationshipDetails+2; index++ {
		id := index + 2
		recorded = append(recorded, Observation{ID: id, RequestBody: sampleRecording()[1].RequestBody})
		replay = append(replay, Observation{ID: id, RequestBody: sampleRecording()[1].RequestBody})
	}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, replay)
	if err != nil {
		t.Fatal(err)
	}
	coverage := diagnosis.RelationshipCoverage
	if !coverage.Complete || coverage.DetailsComplete || coverage.DetailLimit != maxRelationshipDetails || len(coverage.Relationships) != maxRelationshipDetails || coverage.DetailsOmitted != 2 || coverage.ConsumerCount != maxRelationshipDetails+2 || coverage.StateCounts[RelationshipProducerUnchanged] != maxRelationshipDetails+2 {
		t.Fatal("relationship retention cap lost full counts or hid omitted details")
	}
	for _, inputs := range [][]Observation{{{ID: 1}}, nil} {
		diagnosis, err := DiagnoseReplayReviewChecked([]Observation{{ID: 1}, {ID: 2}}, inputs)
		if err != nil {
			t.Fatal(err)
		}
		if diagnosis.RelationshipCoverage.Complete || diagnosis.RelationshipCoverage.ProducerCount != 0 || len(diagnosis.Suggestions) != 0 {
			t.Fatal("zero detected advice was presented as complete relationship proof")
		}
	}
}

func TestReplayRelationshipCoveragePreservesCheckedExhaustion(t *testing.T) {
	var body strings.Builder
	body.WriteByte('{')
	for index := 0; index <= maxCorrelationCandidatesPerObservation; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `"token_%d":"synthetic-value-%06d"`, index, index)
	}
	body.WriteByte('}')
	diagnosis, err := DiagnoseReplayReviewChecked([]Observation{{ID: 1, ResponseBody: body.String()}}, nil)
	if !errors.Is(err, ErrCorrelationWorkLimit) || diagnosis.Suggestions != nil || diagnosis.RelationshipCoverage.StateCounts != nil || diagnosis.RelationshipCoverage.Complete {
		t.Fatal("checked exhaustion returned partial advice or clean relationship coverage")
	}
}

func TestReplayRelationshipCoverageHeaderProducerAndBodylessConsumer(t *testing.T) {
	recorded := []Observation{{ID: 1, RequestURL: "/head", ResponseHeaders: map[string]string{"X-CSRF-Token": "RECORDED-HEADER-123456"}}, {ID: 2, RequestBody: "csrf=RECORDED-HEADER-123456"}}
	replay := []Observation{{ID: 1, RequestURL: "/head", ResponseHeaders: map[string]string{"X-CSRF-Token": "REPLAYED-HEADER-654321"}, ResponseStatus: 200}, {ID: 2, RequestBody: "csrf=REPLAYED-HEADER-654321", ResponseStatus: 204}}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, replay)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosis.RelationshipCoverage.Complete || diagnosis.RelationshipCoverage.MissingProducers != 0 || diagnosis.RelationshipCoverage.UnavailableExtractors != 0 || diagnosis.RelationshipCoverage.MissingConsumers != 0 || len(diagnosis.Suggestions) != 0 {
		t.Fatal("HEAD/header evidence or a bodyless 204 consumer was described as missing")
	}
}

func TestReplayAssertionFailureOnHTTP200IsManualAndUnexplained(t *testing.T) {
	success, failed := true, false
	for _, test := range []struct {
		name    string
		status  int
		success *bool
		want    int
	}{
		{"unknown assertion", 200, nil, 0}, {"asserted success", 200, &success, 0}, {"asserted failure", 200, &failed, 1}, {"bodyless success", 204, nil, 0}, {"unknown transport", 0, &failed, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnosis, err := DiagnoseReplayReviewChecked([]Observation{{ID: 1}}, []Observation{{ID: 1, ResponseStatus: test.status, ResponseSuccess: test.success}})
			if err != nil {
				t.Fatal(err)
			}
			if len(diagnosis.Suggestions) != test.want {
				t.Fatal("assertion-aware HTTP failure count is incorrect")
			}
			if test.want > 0 {
				suggestion := diagnosis.Suggestions[0]
				if suggestion.RuleID != "replay_assertion_failed_unexplained" || suggestion.Extractor != nil || suggestion.ReviewAction != "manual_review_required" || suggestion.Severity != SeverityWarning || HasBlockingRepairs(diagnosis.Suggestions) {
					t.Fatal("HTTP200 assertion failure invented a correlation repair")
				}
				for _, word := range []string{"auth", "csrf", "nonce", "token"} {
					if strings.Contains(strings.ToLower(suggestion.Detail+suggestion.Suggestion), word) {
						t.Fatal("HTTP200 assertion failure inferred an unsupported cause")
					}
				}
			}
		})
	}
	diagnosis, err := DiagnoseReplayReviewChecked(sampleRecording(), []Observation{{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-NEW-654321-replayed"}`}, {ID: 2, RequestBody: sampleRecording()[1].RequestBody, ResponseStatus: 200, ResponseSuccess: &failed}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnosis.Suggestions) != 2 || diagnosis.Suggestions[0].Severity != SeverityError || diagnosis.Suggestions[1].RuleID != "replay_assertion_failed_unexplained" {
		t.Fatal("correlation advice hid independent assertion-failure evidence")
	}
	warnings := diagnoseUnexplainedRejections([]Observation{{ID: 2, ResponseStatus: 200, ResponseSuccess: &failed}, {ID: 2, ResponseStatus: 200, ResponseSuccess: &failed}}, nil)
	if len(warnings) != 1 {
		t.Fatal("the same assertion failure was duplicated")
	}
}

func TestMissingConsumerWarningIsUniqueWhileBindingCountsRemainFull(t *testing.T) {
	recorded := []Observation{{ID: 1, ResponseBody: `{"csrf_token":"SYNTHETIC-CSRF-123456","access_token":"SYNTHETIC-ACCESS-654321"}`}, {ID: 2, RequestBody: "csrf=SYNTHETIC-CSRF-123456&access=SYNTHETIC-ACCESS-654321"}}
	diagnosis, err := DiagnoseReplayReviewChecked(recorded, recorded[:1])
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.RelationshipCoverage.ProducerCount != 2 || diagnosis.RelationshipCoverage.ConsumerCount != 2 || diagnosis.RelationshipCoverage.MissingConsumers != 2 || len(diagnosis.Suggestions) != 1 || diagnosis.Suggestions[0].RuleID != "replay_consumer_missing" {
		t.Fatal("missing consumer warning duplicated or full binding counts were lost")
	}
	coverage := newRelationshipCoverage(nil, nil)
	coverage.record(Correlation{RuleID: "synthetic-private-rule-value", FoundInID: 1, Value: "synthetic-private-value", Expression: "synthetic-private-expression"}, 2, RelationshipProducerMissing, false, false, true)
	wire, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "synthetic-private") {
		t.Fatal("unknown catalog ID or private fields entered relationship details")
	}
}
