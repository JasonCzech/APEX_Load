package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type authoringCorrelationPreviewRequest struct {
	Name                    string                               `json:"name"`
	ExpectedUpdatedAt       time.Time                            `json:"expected_updated_at"`
	Source                  string                               `json:"source,omitempty"`
	Type                    string                               `json:"type,omitempty"`
	Expression              string                               `json:"expression"`
	SampleValue             string                               `json:"sample_value,omitempty"`
	FoundAction             string                               `json:"found_action,omitempty"`
	FoundActionIndex        *int                                 `json:"found_action_index,omitempty"`
	UsedActionIndexes       *[]int                               `json:"used_action_indexes,omitempty"`
	ReplaceSampleValue      *bool                                `json:"replace_sample_value,omitempty"`
	Apply                   bool                                 `json:"apply,omitempty"`
	Confidence              float64                              `json:"confidence,omitempty"`
	Severity                string                               `json:"severity,omitempty"`
	Repairs                 []authoringCorrelationPreviewRequest `json:"repairs,omitempty"`
	AutoApplyHighConfidence bool                                 `json:"auto_apply_high_confidence,omitempty"`
}

type authoringCorrelationPreviewResponse struct {
	SessionID       string                            `json:"session_id"`
	Applied         bool                              `json:"applied"`
	Extractor       dsl.Extractor                     `json:"extractor"`
	Draft           *dsl.Script                       `json:"draft"`
	Changes         []authoringCorrelationDiff        `json:"changes"`
	AuthoringHealth AuthoringHealth                   `json:"authoring_health"`
	Items           []authoringCorrelationPreviewItem `json:"items,omitempty"`
	ReviewRequired  []string                          `json:"review_required,omitempty"`
	Blocked         bool                              `json:"blocked,omitempty"`
	AppliedCount    int                               `json:"applied_count,omitempty"`
}

type authoringCorrelationDiff struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type authoringCorrelationPreviewItem struct {
	Name         string                     `json:"name"`
	Applied      bool                       `json:"applied"`
	ReviewStatus string                     `json:"review_status"`
	Confidence   float64                    `json:"confidence,omitempty"`
	Severity     string                     `json:"severity,omitempty"`
	Extractor    dsl.Extractor              `json:"extractor"`
	Changes      []authoringCorrelationDiff `json:"changes,omitempty"`
	Error        string                     `json:"error,omitempty"`
}

func (s *Server) handlePreviewAuthoringCorrelation(w http.ResponseWriter, r *http.Request) {
	s.authoringMutationMu.Lock()
	defer s.authoringMutationMu.Unlock()
	session, ok := s.getAuthoringSession(w, r.PathValue("id"))
	if !ok {
		return
	}
	if session.Draft == nil {
		writeError(w, http.StatusBadRequest, "authoring session draft is required")
		return
	}

	var req authoringCorrelationPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ExpectedUpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "expected_updated_at is required")
		return
	}
	if !session.UpdatedAt.Equal(req.ExpectedUpdatedAt) {
		writeError(w, http.StatusConflict, "authoring session changed since correlation repair was prepared")
		return
	}
	if req.Apply {
		switch session.Status {
		case "approving":
			writeError(w, http.StatusConflict, "authoring session approval is in progress")
			return
		case "approved":
			writeError(w, http.StatusConflict, "approved authoring sessions are immutable")
			return
		}
	}
	if len(req.Repairs) > 0 {
		s.handleBatchAuthoringCorrelationPreview(w, r, session, req)
		return
	}
	extractor, err := buildGuidedCorrelationExtractor(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	draft := cloneScriptDefinition(session.Draft)
	changes, err := applyGuidedCorrelation(draft, req, extractor)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	validation := buildScriptValidationResponse("", draft)

	if req.Apply {
		expectedUpdatedAt := session.UpdatedAt
		expectedStatus := session.Status
		session.Draft = draft
		session.Status = "editing"
		session.UpdatedAt = time.Now()
		session.LastAccessedAt = session.UpdatedAt
		session.ReviewState = cloneReviewState(session.ReviewState)
		if session.ReviewState == nil {
			session.ReviewState = map[string]any{}
		}
		session.ReviewState["last_correlation_preview"] = map[string]any{
			"name":         extractor.Name,
			"changes":      len(changes),
			"applied_at":   session.UpdatedAt.Format(time.RFC3339Nano),
			"health_state": validation.AuthoringHealth.Status,
		}
		saved, err := s.compareAndSwapAuthoringSession(session, expectedUpdatedAt, expectedStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !saved {
			writeError(w, http.StatusConflict, "authoring session changed while correlation repairs were applied")
			return
		}
		s.recordAuditEvent(r, "script.authoring_session.correlation.apply", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"name": extractor.Name, "changes": len(changes)})
	} else {
		s.recordAuditEvent(r, "script.authoring_session.correlation.preview", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"name": extractor.Name, "changes": len(changes)})
	}

	writeJSON(w, http.StatusOK, authoringCorrelationPreviewResponse{
		SessionID:       session.ID,
		Applied:         req.Apply,
		Extractor:       extractor,
		Draft:           maskScriptSecrets(draft),
		Changes:         sanitizeAuthoringCorrelationDiffs(changes),
		AuthoringHealth: validation.AuthoringHealth,
	})
}

func (s *Server) handleBatchAuthoringCorrelationPreview(w http.ResponseWriter, r *http.Request, session *storage.AuthoringSession, req authoringCorrelationPreviewRequest) {
	draft := cloneScriptDefinition(session.Draft)
	items := make([]authoringCorrelationPreviewItem, 0, len(req.Repairs))
	allChanges := make([]authoringCorrelationDiff, 0)
	reviewRequired := make([]string, 0)
	appliedCount := 0
	blocked := false

	for _, repair := range req.Repairs {
		extractor, err := buildGuidedCorrelationExtractor(repair)
		item := authoringCorrelationPreviewItem{
			Name:       strings.TrimSpace(repair.Name),
			Confidence: repair.Confidence,
			Severity:   strings.TrimSpace(repair.Severity),
			Extractor:  extractor,
		}
		if err != nil {
			item.Error = err.Error()
			item.ReviewStatus = "invalid"
			reviewRequired = append(reviewRequired, item.Name)
			items = append(items, item)
			continue
		}

		highConfidence := repair.Confidence >= 0.8
		highRisk := strings.EqualFold(strings.TrimSpace(repair.Severity), "error")
		shouldApply := !req.Apply || !req.AutoApplyHighConfidence || highConfidence
		if req.Apply && req.AutoApplyHighConfidence && !highConfidence {
			item.ReviewStatus = "review_required"
			reviewRequired = append(reviewRequired, extractor.Name)
			if highRisk {
				blocked = true
			}
			items = append(items, item)
			continue
		}

		changes, err := applyGuidedCorrelation(draft, repair, extractor)
		if err != nil {
			item.Error = err.Error()
			item.ReviewStatus = "invalid"
			reviewRequired = append(reviewRequired, extractor.Name)
			if highRisk {
				blocked = true
			}
			items = append(items, item)
			continue
		}
		item.Changes = changes
		item.Applied = req.Apply && shouldApply
		if item.Applied {
			appliedCount++
			item.ReviewStatus = "applied"
		} else if highConfidence {
			item.ReviewStatus = "preview_high_confidence"
		} else {
			item.ReviewStatus = "review_required"
			reviewRequired = append(reviewRequired, extractor.Name)
			if highRisk {
				blocked = true
			}
		}
		allChanges = append(allChanges, changes...)
		items = append(items, item)
	}

	validation := buildScriptValidationResponse("", draft)
	if req.Apply && appliedCount > 0 {
		expectedUpdatedAt := session.UpdatedAt
		expectedStatus := session.Status
		session.Draft = draft
		session.Status = "editing"
		session.UpdatedAt = time.Now()
		session.LastAccessedAt = session.UpdatedAt
		session.ReviewState = cloneReviewState(session.ReviewState)
		if session.ReviewState == nil {
			session.ReviewState = map[string]any{}
		}
		session.ReviewState["last_correlation_batch_preview"] = map[string]any{
			"total":           len(req.Repairs),
			"applied":         appliedCount,
			"review_required": reviewRequired,
			"blocked":         blocked,
			"applied_at":      session.UpdatedAt.Format(time.RFC3339Nano),
			"health_state":    validation.AuthoringHealth.Status,
		}
		saved, err := s.compareAndSwapAuthoringSession(session, expectedUpdatedAt, expectedStatus)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !saved {
			writeError(w, http.StatusConflict, "authoring session changed while correlation repairs were applied")
			return
		}
		s.recordAuditEvent(r, "script.authoring_session.correlation.batch_apply", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"applied": appliedCount, "review_required": len(reviewRequired), "blocked": blocked})
	} else {
		s.recordAuditEvent(r, "script.authoring_session.correlation.batch_preview", "success", "authoring_session", session.ID, session.ProjectID, map[string]any{"repairs": len(req.Repairs), "review_required": len(reviewRequired), "blocked": blocked})
	}

	writeJSON(w, http.StatusOK, authoringCorrelationPreviewResponse{
		SessionID:       session.ID,
		Applied:         req.Apply && appliedCount > 0,
		Draft:           maskScriptSecrets(draft),
		Changes:         sanitizeAuthoringCorrelationDiffs(allChanges),
		AuthoringHealth: validation.AuthoringHealth,
		Items:           sanitizeAuthoringCorrelationPreviewItems(items),
		ReviewRequired:  uniqueStringsPreserveOrder(reviewRequired),
		Blocked:         blocked,
		AppliedCount:    appliedCount,
	})
}

func sanitizeAuthoringCorrelationDiffs(changes []authoringCorrelationDiff) []authoringCorrelationDiff {
	if len(changes) == 0 {
		return changes
	}
	out := make([]authoringCorrelationDiff, len(changes))
	for index, change := range changes {
		out[index] = authoringCorrelationDiff{Kind: change.Kind, Path: change.Path}
	}
	return out
}

func sanitizeAuthoringCorrelationPreviewItems(items []authoringCorrelationPreviewItem) []authoringCorrelationPreviewItem {
	if len(items) == 0 {
		return items
	}
	out := make([]authoringCorrelationPreviewItem, len(items))
	for index, item := range items {
		out[index] = item
		out[index].Changes = sanitizeAuthoringCorrelationDiffs(item.Changes)
	}
	return out
}

func buildGuidedCorrelationExtractor(req authoringCorrelationPreviewRequest) (dsl.Extractor, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return dsl.Extractor{}, fmt.Errorf("name is required")
	}
	expression := strings.TrimSpace(req.Expression)
	if expression == "" {
		return dsl.Extractor{}, fmt.Errorf("expression is required")
	}
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if source == "" {
		source = "body"
	}
	extractorType := strings.ToLower(strings.TrimSpace(req.Type))
	if extractorType == "" {
		extractorType = "jsonpath"
		if strings.Contains(expression, "|||") {
			extractorType = "boundary"
		}
	}
	return dsl.Extractor{
		Name:       name,
		Source:     source,
		Type:       extractorType,
		Expression: expression,
	}, nil
}

func applyGuidedCorrelation(script *dsl.Script, req authoringCorrelationPreviewRequest, extractor dsl.Extractor) ([]authoringCorrelationDiff, error) {
	if script == nil {
		return nil, fmt.Errorf("draft is required")
	}
	if len(script.Actions) == 0 {
		return nil, fmt.Errorf("draft must have at least one action")
	}
	foundIndex, err := resolveCorrelationFoundActionIndex(script.Actions, req)
	if err != nil {
		return nil, err
	}
	var usedIndexes []int
	if shouldReplaceCorrelationSample(req) {
		sample := strings.TrimSpace(req.SampleValue)
		replacement := "{{var." + extractor.Name + "}}"
		usedIndexes = resolveCorrelationUsedActionIndexes(script.Actions, foundIndex, req)
		// Raw replacement cannot maintain contextual occurrence identities.
		// Reject the whole repair before adding an extractor or editing another
		// consumer; otherwise a rejected batch item could still mutate its draft.
		for _, index := range usedIndexes {
			action := &script.Actions[index]
			if _, hasContract := action.Metadata[dsl.ImportAuraTemplateContractMetadataKey]; hasContract && sample != replacement && strings.Contains(action.Body, sample) {
				return nil, fmt.Errorf("actions[%d].body requires manual structural review before correlation replacement because it has an Aura template occurrence contract", index+1)
			}
		}
	}

	changes := make([]authoringCorrelationDiff, 0, 4)
	extractorIndex := len(script.Actions[foundIndex].Extractors)
	script.Actions[foundIndex].Extractors = append(script.Actions[foundIndex].Extractors, extractor)
	changes = append(changes, authoringCorrelationDiff{
		Kind:  "extractor_added",
		Path:  fmt.Sprintf("actions[%d].extractors[%d]", foundIndex+1, extractorIndex+1),
		After: fmt.Sprintf("%s:%s:%s", extractor.Name, extractor.Type, extractor.Expression),
	})

	if shouldReplaceCorrelationSample(req) {
		replacement := "{{var." + extractor.Name + "}}"
		for _, index := range usedIndexes {
			changes = append(changes, replaceActionSampleValue(&script.Actions[index], index, strings.TrimSpace(req.SampleValue), replacement)...)
		}
	}
	return changes, nil
}

func resolveCorrelationFoundActionIndex(actions []dsl.Action, req authoringCorrelationPreviewRequest) (int, error) {
	if req.FoundActionIndex != nil {
		index := *req.FoundActionIndex
		if index < 1 || index > len(actions) {
			return 0, fmt.Errorf("found_action_index must be between 1 and %d", len(actions))
		}
		return index - 1, nil
	}
	if name := strings.TrimSpace(req.FoundAction); name != "" {
		for index, action := range actions {
			if strings.EqualFold(strings.TrimSpace(action.Name), name) {
				return index, nil
			}
		}
		return 0, fmt.Errorf("found_action %q was not found", name)
	}
	if sample := strings.TrimSpace(req.SampleValue); sample != "" {
		for index, action := range actions {
			if actionContainsSample(action, sample) {
				return index, nil
			}
		}
	}
	return 0, nil
}

func resolveCorrelationUsedActionIndexes(actions []dsl.Action, foundIndex int, req authoringCorrelationPreviewRequest) []int {
	if req.UsedActionIndexes != nil {
		indexes := make([]int, 0, len(*req.UsedActionIndexes))
		seen := map[int]struct{}{}
		for _, oneBased := range *req.UsedActionIndexes {
			index := oneBased - 1
			// Correlation values can only be consumed after their producing
			// action. Ignore stale/forged picker indexes at or before the producer
			// as well as indexes outside the current draft.
			if index <= foundIndex || index >= len(actions) {
				continue
			}
			if _, ok := seen[index]; ok {
				continue
			}
			seen[index] = struct{}{}
			indexes = append(indexes, index)
		}
		return indexes
	}
	indexes := make([]int, 0, len(actions)-foundIndex-1)
	for index := foundIndex + 1; index < len(actions); index++ {
		indexes = append(indexes, index)
	}
	return indexes
}

func replaceActionSampleValue(action *dsl.Action, actionIndex int, sample string, replacement string) []authoringCorrelationDiff {
	if action == nil || sample == "" {
		return nil
	}
	changes := make([]authoringCorrelationDiff, 0, 3)
	action.Target, changes = replaceCorrelationField(action.Target, sample, replacement, fmt.Sprintf("actions[%d].target", actionIndex+1), changes)
	action.Body, changes = replaceCorrelationField(action.Body, sample, replacement, fmt.Sprintf("actions[%d].body", actionIndex+1), changes)
	action.Value, changes = replaceCorrelationField(action.Value, sample, replacement, fmt.Sprintf("actions[%d].value", actionIndex+1), changes)
	action.Selector, changes = replaceCorrelationField(action.Selector, sample, replacement, fmt.Sprintf("actions[%d].selector", actionIndex+1), changes)
	for key, value := range action.Headers {
		next, updatedChanges := replaceCorrelationField(value, sample, replacement, fmt.Sprintf("actions[%d].headers.%s", actionIndex+1, key), changes)
		action.Headers[key] = next
		changes = updatedChanges
	}
	return changes
}

func replaceCorrelationField(value string, sample string, replacement string, path string, changes []authoringCorrelationDiff) (string, []authoringCorrelationDiff) {
	if value == "" || !strings.Contains(value, sample) {
		return value, changes
	}
	next := strings.ReplaceAll(value, sample, replacement)
	return next, append(changes, authoringCorrelationDiff{
		Kind:   "value_replaced",
		Path:   path,
		Before: value,
		After:  next,
	})
}

func actionContainsSample(action dsl.Action, sample string) bool {
	if strings.Contains(action.Target, sample) || strings.Contains(action.Body, sample) || strings.Contains(action.Value, sample) || strings.Contains(action.Selector, sample) {
		return true
	}
	for _, value := range action.Headers {
		if strings.Contains(value, sample) {
			return true
		}
	}
	return false
}

func shouldReplaceCorrelationSample(req authoringCorrelationPreviewRequest) bool {
	if strings.TrimSpace(req.SampleValue) == "" {
		return false
	}
	if req.ReplaceSampleValue == nil {
		return true
	}
	return *req.ReplaceSampleValue
}

var cloneScriptDefinition = importreview.CloneScriptDefinition
