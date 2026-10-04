package salesforce

import (
	"encoding/base64"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	"golang.org/x/text/encoding/charmap"
)

const (
	maxAuraReviewBatches  = 512
	maxAuraReviewBytes    = 64 << 20
	maxAuraOutcomeDetails = 2048
	maxAuraIssueDetails   = 2048
)

// AuraOutcomeReview contains only structural evidence. Complete describes
// retained capture coverage, not a successful baseline or live replay.
// Action totals and state counts cover inspected batches only; omitted batches
// are counted explicitly when the review budget is exhausted.
type AuraOutcomeReview struct {
	Complete             bool               `json:"complete"`
	DetailsComplete      bool               `json:"details_complete"`
	OutcomesOmitted      int                `json:"outcomes_omitted"`
	IssuesOmitted        int                `json:"issues_omitted"`
	RequestIssuesOmitted int                `json:"request_issues_omitted"`
	OutcomeDetailLimit   int                `json:"outcome_detail_limit"`
	IssueDetailLimit     int                `json:"issue_detail_limit"`
	TotalBatches         int                `json:"total_batches"`
	InspectedBatches     int                `json:"inspected_batches"`
	CompleteBatches      int                `json:"complete_batches"`
	IncompleteBatches    int                `json:"incomplete_batches"`
	OmittedBatches       int                `json:"omitted_batches"`
	MissingResponses     int                `json:"missing_responses"`
	InvalidRequests      int                `json:"invalid_requests"`
	RequestActions       int                `json:"request_actions"`
	ResponseActions      int                `json:"response_actions"`
	MatchedActions       int                `json:"matched_actions"`
	BatchLimit           int                `json:"batch_limit"`
	AnalysisByteLimit    int                `json:"analysis_byte_limit"`
	StateCounts          map[aura.State]int `json:"state_counts,omitempty"`
	IssueCounts          map[aura.Code]int  `json:"issue_counts,omitempty"`
	Batches              []AuraOutcomeBatch `json:"batches"`
}

// RequestValid describes the imported executable request. Analysis compares the
// matched HAR's captured request and response, before any native correlation
// rewrites. Without a HAR match it describes request-only evidence. Its Success
// field must always be interpreted together with HasResponse.
type AuraOutcomeBatch struct {
	ActionSequence       int          `json:"action_sequence"`
	ActionPath           string       `json:"action_path,omitempty"`
	HAREntryID           int          `json:"har_entry_id,omitempty"`
	RequestValid         bool         `json:"request_valid"`
	RequestIssues        []aura.Issue `json:"request_issues,omitempty"`
	RequestIssuesOmitted int          `json:"request_issues_omitted,omitempty"`
	Analysis             aura.Report  `json:"analysis"`
}

func isAuraBatch(ref actionRef) bool {
	return ref.Kind == "aura" && ref.Method == "POST" && ref.Action != nil
}

func validateAuraRequest(body string) aura.Report {
	if len(body) > aura.MaxEnvelopeBytes {
		return aura.Report{Issues: []aura.Issue{{Code: aura.ByteLimit}}}
	}
	return aura.ValidateRequest([]byte(body))
}

// auraActionRequestBody mirrors HTTP wire-body transformations without altering
// the authored body or its encoding. Decode errors never enter exported evidence.
func auraActionRequestBody(action *dsl.Action) (string, aura.Code) {
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && action.Body != "" {
		if len(action.Body) > base64.StdEncoding.EncodedLen(aura.MaxEnvelopeBytes) {
			return "", aura.ByteLimit
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
		if err != nil {
			return "", aura.InvalidEnvelope
		}
		if len(decoded) > aura.MaxEnvelopeBytes {
			return "", aura.ByteLimit
		}
		return string(decoded), ""
	}
	if len(action.Body) > aura.MaxEnvelopeBytes {
		return "", aura.ByteLimit
	}
	if strings.EqualFold(action.BodyEncoding, "windows-1252") {
		encoded, err := charmap.Windows1252.NewEncoder().String(action.Body)
		if err != nil {
			return "", aura.InvalidEnvelope
		}
		return encoded, ""
	}
	return action.Body, ""
}

func validateAuraActionRequest(action *dsl.Action) aura.Report {
	body, code := auraActionRequestBody(action)
	if code != "" {
		return aura.Report{Issues: []aura.Issue{{Code: code}}}
	}
	return validateAuraRequest(body)
}

// Encoded representation and bounded decode work are charged before decoding;
// the separate captured/request-only parser charge below remains explicit.
func boundedAuraActionWorkBytes(action *dsl.Action) int {
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && action.Body != "" {
		encodedBytes := len(action.Body)
		limit := base64.StdEncoding.EncodedLen(aura.MaxEnvelopeBytes)
		if encodedBytes > limit {
			return limit + 1
		}
		return encodedBytes + base64.StdEncoding.DecodedLen(encodedBytes)
	}
	if strings.EqualFold(action.BodyEncoding, "windows-1252") {
		return 2 * boundedAuraBodyBytes(action.Body)
	}
	return boundedAuraBodyBytes(action.Body)
}

func inspectAuraOutcomes(refs []actionRef, entries []harEntry) *AuraOutcomeReview {
	byID := make(map[int]harEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	review := &AuraOutcomeReview{
		Complete: true, DetailsComplete: true, BatchLimit: maxAuraReviewBatches, AnalysisByteLimit: maxAuraReviewBytes,
		OutcomeDetailLimit: maxAuraOutcomeDetails, IssueDetailLimit: maxAuraIssueDetails,
		StateCounts: make(map[aura.State]int), IssueCounts: make(map[aura.Code]int),
		Batches: make([]AuraOutcomeBatch, 0),
	}
	usedBytes := 0
	for _, ref := range refs {
		if !isAuraBatch(ref) {
			continue
		}
		review.TotalBatches++
		entry, matched := byID[ref.HARID]
		requestBody := ref.Action.Body
		responseBody := ""
		if matched {
			requestBody, responseBody = entry.RequestBody, entry.ResponseBody
		}
		// Every body passed to the parser is charged against an aggregate work
		// budget in addition to the parser's individual envelope bounds.
		workBytes := boundedAuraActionWorkBytes(ref.Action) + boundedAuraBodyBytes(requestBody) + boundedAuraBodyBytes(responseBody)
		if review.InspectedBatches >= maxAuraReviewBatches || workBytes > maxAuraReviewBytes-usedBytes {
			review.OmittedBatches++
			review.Complete = false
			continue
		}
		usedBytes += workBytes
		request := validateAuraActionRequest(ref.Action)
		var analysis aura.Report
		if matched && strings.TrimSpace(responseBody) != "" {
			if len(requestBody) > aura.MaxEnvelopeBytes || len(responseBody) > aura.MaxEnvelopeBytes {
				capturedRequest := validateAuraRequest(requestBody)
				analysis = aura.Report{HasResponse: true, RequestActions: capturedRequest.RequestActions, Issues: capturedRequest.Issues}
				if len(responseBody) > aura.MaxEnvelopeBytes {
					analysis.Issues = append(analysis.Issues, aura.Issue{Code: aura.ByteLimit})
				}
			} else {
				analysis = aura.Validate([]byte(requestBody), []byte(responseBody))
			}
		} else if matched {
			analysis = validateAuraRequest(requestBody)
		} else {
			analysis = request
		}
		review.InspectedBatches++
		if !request.Success {
			review.InvalidRequests++
		}
		if !analysis.HasResponse {
			review.MissingResponses++
		}
		complete := request.Complete && analysis.Complete && analysis.HasResponse
		if complete {
			review.CompleteBatches++
		} else {
			review.IncompleteBatches++
			review.Complete = false
		}
		review.RequestActions += analysis.RequestActions
		review.ResponseActions += analysis.ResponseActions
		review.MatchedActions += analysis.MatchedActions
		for _, outcome := range analysis.Outcomes {
			review.StateCounts[outcome.State]++
		}
		for _, issue := range analysis.Issues {
			review.IssueCounts[issue.Code]++
		}
		review.Batches = append(review.Batches, AuraOutcomeBatch{
			ActionSequence: ref.Sequence, ActionPath: ref.ActionPath, HAREntryID: ref.HARID,
			RequestValid: request.Success, RequestIssues: request.Issues, Analysis: analysis,
		})
	}
	if review.TotalBatches == 0 {
		return nil
	}
	retainAuraReviewDetails(review)
	return review
}

// Full validation and aggregate counts precede detail retention. Failed
// outcomes across every batch take priority over successful earlier batches.
// The issue budget covers both executable-request and captured-response issues.
func retainAuraReviewDetails(review *AuraOutcomeReview) {
	remainingOutcomes := maxAuraOutcomeDetails
	selected := make([][]aura.Outcome, len(review.Batches))
	for _, failed := range []bool{true, false} {
		for index := range review.Batches {
			for _, outcome := range review.Batches[index].Analysis.Outcomes {
				if (outcome.State != aura.Success) == failed && remainingOutcomes > 0 {
					selected[index] = append(selected[index], outcome)
					remainingOutcomes--
				}
			}
		}
	}
	for index := range review.Batches {
		batch := &review.Batches[index]
		sort.Slice(selected[index], func(i, j int) bool { return selected[index][i].Ordinal < selected[index][j].Ordinal })
		batch.Analysis.OutcomesOmitted += len(batch.Analysis.Outcomes) - len(selected[index])
		batch.Analysis.Outcomes = selected[index]
		review.OutcomesOmitted += batch.Analysis.OutcomesOmitted
	}
	remainingIssues := maxAuraIssueDetails
	for index := range review.Batches {
		batch := &review.Batches[index]
		retained := len(batch.RequestIssues)
		if retained > remainingIssues {
			retained = remainingIssues
		}
		batch.RequestIssuesOmitted = len(batch.RequestIssues) - retained
		batch.RequestIssues = append([]aura.Issue(nil), batch.RequestIssues[:retained]...)
		remainingIssues -= retained
		review.RequestIssuesOmitted += batch.RequestIssuesOmitted
	}
	for index := range review.Batches {
		batch := &review.Batches[index]
		retained := len(batch.Analysis.Issues)
		if retained > remainingIssues {
			retained = remainingIssues
		}
		batch.Analysis.IssuesOmitted += len(batch.Analysis.Issues) - retained
		batch.Analysis.Issues = append([]aura.Issue(nil), batch.Analysis.Issues[:retained]...)
		remainingIssues -= retained
		review.IssuesOmitted += batch.Analysis.IssuesOmitted
	}
	review.DetailsComplete = review.OmittedBatches == 0 && review.OutcomesOmitted+review.IssuesOmitted+review.RequestIssuesOmitted == 0
}

func boundedAuraBodyBytes(body string) int {
	if len(body) > aura.MaxEnvelopeBytes {
		return aura.MaxEnvelopeBytes + 1
	}
	return len(body)
}

func removeLegacyAuraAssertions(action *dsl.Action) {
	assertions := action.Assertions[:0]
	for _, assertion := range action.Assertions {
		if assertion.Type == "body_not_contains" && assertion.Expected == nil &&
			!assertion.LegacyHTMLCheck && assertion.Min == 0 && assertion.Max == 0 &&
			(assertion.Value == `"state":"ERROR"` || assertion.Value == `"state":"INCOMPLETE"`) {
			continue
		}
		assertions = append(assertions, assertion)
	}
	action.Assertions = assertions
}
