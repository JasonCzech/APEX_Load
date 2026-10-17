package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

type JMeterBatchOperation struct {
	Type               string         `json:"type"`
	SourceGroupID      string         `json:"source_group_id,omitempty"`
	SourceNodeID       string         `json:"source_node_id,omitempty"`
	SecretName         string         `json:"secret_name,omitempty"`
	ClientIDSecret     string         `json:"client_id_secret,omitempty"`
	PayloadPointer     string         `json:"payload_pointer,omitempty"`
	MessageIndex       int            `json:"message_index,omitempty"`
	Parameter          *dsl.Parameter `json:"parameter,omitempty"`
	PropertyName       string         `json:"property_name,omitempty"`
	PropertyValue      string         `json:"property_value,omitempty"`
	UUID               bool           `json:"uuid,omitempty"`
	Timezone           string         `json:"timezone,omitempty"`
	ConfirmNoOverrides bool           `json:"confirm_no_overrides,omitempty"`
}

func (s *Server) processJMeterBatchRepair(w http.ResponseWriter, r *http.Request, apply bool, req ImportRepairRequest) {
	if project, ok := authorizedProjectFromRequest(r); ok && project != normalizeProjectID(req.ProjectID) {
		writeError(w, 404, "Batch not found")
		return
	}
	id := jmeterBatchIdentity{ID: req.BatchID, Revision: req.Revision}
	if len(req.Operations) == 0 || len(req.Operations) > 32 {
		writeError(w, 400, "Supply 1–32 reviewed batch operations")
		return
	}
	requestDigest := batchDigest(struct {
		ID         jmeterBatchIdentity
		Operations []JMeterBatchOperation
	}{id, req.Operations})
	if receipt, err := s.openBatchReceipt(r, id.Revision, req.ProjectID); err == nil {
		if apply && receipt.Kind == "repair" && receipt.RequestDigest == requestDigest && req.PreviewDigest == receipt.Digest {
			writeJSON(w, 200, receipt.Response)
			return
		}
		writeError(w, 409, "Batch revision has already been replaced or saved")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, 409, "Batch receipt is unavailable")
		return
	}
	// Preview never replaces the batch, so it reads without the consume lease
	// and cannot block a concurrent save or apply of the same batch.
	var b *jmeterBatchCandidate
	var claim *importsession.Claim
	var err error
	if apply {
		b, claim, err = s.claimJMeterBatch(r, id, req.ProjectID)
	} else {
		b, err = s.openJMeterBatch(r, id, req.ProjectID)
	}
	if err != nil {
		writeError(w, 409, "Batch is stale, expired, or outside this project")
		return
	}
	if claim != nil {
		defer s.releaseImportCandidateClaim(claim)
	}
	for _, doc := range b.Source {
		defer clear(doc.Data)
	}
	var lease *importCandidateClaimLease
	if claim != nil {
		lease = s.startImportCandidateClaimLease(claim)
		defer lease.stop()
	}
	if _, err = s.openBatchReceipt(r, id.Revision, req.ProjectID); !errors.Is(err, os.ErrNotExist) {
		writeError(w, 409, "Batch revision changed")
		return
	}
	before := make([]*dsl.Script, len(b.Scripts))
	for i, script := range b.Scripts {
		before[i] = batchDisclosure(script)
		if before[i] == nil {
			writeError(w, 500, "Cannot project protected batch")
			return
		}
	}
	for _, op := range req.Operations {
		if err = applyJMeterBatchOperation(b, op); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if _, err := buildJMeterScenarioRequest(b.Scripts, b.Scenario); err != nil {
		writeError(w, 400, "Repair invalidates the protected scenario")
		return
	}
	findings := map[string][]importreview.Finding{}
	readiness := map[string]importreview.Readiness{}
	for scriptIndex, script := range b.Scripts {
		f, err := importreview.RefreshDependencies(script)
		if err != nil {
			writeError(w, 400, "Cannot evaluate repaired source dependencies")
			return
		}
		issues := dsl.ValidateScriptIssuesWithPreservedImportCredentials(script)
		if len(issues) > 0 {
			writeError(w, 400, "Repair creates an invalid script")
			return
		}
		missing := len(discoverSecretBindingRequirements(script, 0))
		if s.runner != nil {
			_, refs := replaySecretRefs(script, s.runner.secretResolverForProject(script.ProjectID))
			missing += len(refs)
		}
		ready, err := importreview.EvaluateScript(script, importreview.EvaluationInput{ValidationIssues: issues, UnresolvedBindings: missing})
		if err != nil {
			writeError(w, 400, "Cannot evaluate repaired readiness")
			return
		}
		key := fmt.Sprintf("script-%d", scriptIndex)
		if script.Config.JMeter != nil {
			key = script.Config.JMeter.SourceGroupID
		}
		findings[key] = f
		readiness[key] = ready
	}
	scripts := make([]*dsl.Script, len(b.Scripts))
	for i, script := range b.Scripts {
		scripts[i] = batchDisclosure(script)
		if scripts[i] == nil {
			writeError(w, 500, "Cannot project repaired batch")
			return
		}
	}
	digest := batchDigest(struct {
		Request  string
		Scripts  []*dsl.Script
		Scenario *jmeter.ScenarioDraft
	}{requestDigest, b.Scripts, b.Scenario})
	evidence := &jmeter.ConvertResult{Scripts: b.Scripts, SourceReview: b.SourceReview, Unsupported: b.Advisories}
	evidence.NormalizeReviewEvidence()
	draft := importDraft{Source: "jmeter", ProjectID: b.ProjectID, Scripts: b.Scripts, SourceReview: b.SourceReview, Unsupported: evidence.Diagnostics, Warnings: evidence.Warnings}
	augmented := draft.Augment(r)
	applyJMeterReviewPreflight(augmented.Preflight, evidence)
	response := map[string]any{"batch": b.Identity, "preview_digest": digest, "before": before, "scripts": scripts, "scenario_draft": b.Scenario, "findings": findings, "readiness": readiness, "expires_at": b.ExpiresAt, "diagnostics": evidence.Diagnostics, "review_summary": evidence.ReviewSummary, "warnings": augmented.Warnings, "unsupported_elements": evidence.UnsupportedElements, "preflight": augmented.Preflight}
	if !apply {
		writeJSON(w, 200, response)
		return
	}
	if req.PreviewDigest != digest {
		writeError(w, 409, "Apply requires the matching repair preview")
		return
	}
	if err = lease.assertOwned(); err != nil {
		writeError(w, 409, "Batch lease was lost")
		return
	}
	old := *b
	b.Identity.Revision = uuid.NewString()
	b.History = append(b.History, req.Operations...)
	stampBatch(b)
	stampJMeterDirectImportOperation(&jmeter.ConvertResult{Scripts: b.Scripts}, uuid.NewString())
	secured := s.newScriptImportResponseWithSourceSecrets(withImportBatchExpiry(r, b.ExpiresAt), scriptImportResponseOptions{Scripts: b.Scripts, ScenarioDraft: b.Scenario})
	if secured[importDraftResponseFailureKey] != nil {
		writeScriptImportResponse(w, 503, secured)
		return
	}
	safeScripts := make([]*dsl.Script, len(b.Scripts))
	for i, script := range b.Scripts {
		safeScripts[i] = batchDisclosure(script)
		if safeScripts[i] == nil {
			writeError(w, 500, "Cannot project repaired batch")
			return
		}
	}
	response["scripts"] = safeScripts
	response["secret_binding_requirements"] = secured["secret_binding_requirements"]
	response["batch"] = b.Identity
	if err = s.saveJMeterBatch(r, b); err != nil {
		writeError(w, 503, "Cannot secure replacement batch")
		return
	}
	if err = s.publishBatchReceipt(r, &old, claim, jmeterBatchReceipt{Kind: "repair", RequestDigest: requestDigest, Digest: digest, Response: response}); err != nil {
		writeError(w, 409, "Batch publication failed; retry the same operation")
		return
	}
	// Receipt is authoritative even if old claim cleanup fails.
	_ = s.completeImportCandidateClaim(r.Context(), claim)
	writeJSON(w, 200, response)
}

func applyJMeterBatchOperation(b *jmeterBatchCandidate, op JMeterBatchOperation) error {
	var selected []*dsl.Script
	for _, s := range b.Scripts {
		if s.Config.JMeter != nil && (op.SourceGroupID == "" || op.SourceGroupID == s.Config.JMeter.SourceGroupID) {
			selected = append(selected, s)
		}
	}
	if len(selected) == 0 {
		return errors.New("Source group is no longer present")
	}
	if op.Type == "confirm_reference_context" {
		if op.Timezone != "UTC" || !op.ConfirmNoOverrides {
			return errors.New("Explicit UTC and no-property-overrides confirmation is required")
		}
		for _, s := range selected {
			s.Config.JMeter.ContextConfirmed = true
			s.Config.JMeter.Timezone = "UTC"
			for i := range s.Parameters {
				if s.Parameters[i].Type == "timestamp" || s.Parameters[i].Type == "date_time" {
					s.Parameters[i].Timezone = "UTC"
				}
			}
			removeJMeterRepairCode(s, "jmeter_reference_context_confirmation_required")
		}
		return nil
	}
	if len(selected) != 1 {
		return errors.New("Select exactly one source group")
	}
	s := selected[0]
	switch op.Type {
	case "normalize_oauth_options":
		if s.Config.Kafka.OAuth == nil {
			return errors.New("Selected group has no supported OAuth configuration")
		}
		var checked bool
		for _, doc := range b.Source {
			if err := jmeter.ValidateOAuthNormalization(doc.Data, s.Config.JMeter.ConfigurationID); err == nil {
				checked = true
				break
			}
		}
		if !checked {
			return errors.New("OAuth options are missing, unsupported, or conflicting; normalization is blocked")
		}
		removeJMeterRepairCode(s, "jmeter_kafka_nonstandard_oauth_options")
	case "bind_kafka_credentials":
		if s.Config.Kafka.OAuth == nil || !logicalSecretNamePattern.MatchString(op.SecretName) || !logicalSecretNamePattern.MatchString(op.ClientIDSecret) {
			return errors.New("Select valid Kafka client ID and client-secret references")
		}
		s.Config.Kafka.OAuth.ClientID = "{{secret." + op.ClientIDSecret + "}}"
		s.Config.Kafka.OAuth.ClientSecretRef = "{{secret." + op.SecretName + "}}"
		// Runtime secret discovery retains missing-secret blockers independently.
		removeJMeterRepairCode(s, "jmeter_kafka_oauth_credentials_required")
		refreshBatchCredentialConfig(s)
	case "bind_eventhubs_credentials":
		if s.Protocol != "eventhubs" || !logicalSecretNamePattern.MatchString(op.SecretName) {
			return errors.New("Select a valid Event Hubs credential reference")
		}
		s.Config.EventHubs.ConnectionString = ""
		s.Config.EventHubs.ConnectionStringSecret = "{{secret." + op.SecretName + "}}"
		removeJMeterRepairCode(s, "jmeter_eventhubs_credentials_required")
		refreshBatchCredentialConfig(s)
	case "utc_timestamp", "parameterize_payload", "eventhubs_property":
		var action *dsl.Action
		_ = dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
			if ref.Action != nil && dslwalk.MetadataString(ref.Metadata(), "source_node_id") == op.SourceNodeID {
				action = ref.Action
			}
			return nil
		})
		if action == nil {
			return errors.New("Select an existing source sampler")
		}
		if op.Type == "eventhubs_property" {
			if action.Type != "eventhubs_produce" || op.PropertyName == "" || len(op.PropertyName) > 256 {
				return errors.New("Select an Event Hubs sampler and property name")
			}
			value := op.PropertyValue
			if op.UUID {
				value = "{{fn.uuid()}}"
			}
			if value == "" || len(value) > 4096 {
				return errors.New("Supply the intended property value or select UUID")
			}
			if action.Headers == nil {
				action.Headers = map[string]string{}
			}
			if _, exists := action.Headers[op.PropertyName]; exists {
				return errors.New("Property already exists")
			}
			action.Headers[op.PropertyName] = value
			return nil
		}
		replacement := "{{fn.timestamp.iso8601()}}"
		if op.Type == "parameterize_payload" {
			if op.Parameter == nil || !repairVariableName.MatchString(op.Parameter.Name) {
				return errors.New("Supply a valid parameter")
			}
			for _, p := range s.Parameters {
				if p.Name == op.Parameter.Name {
					return errors.New("Parameter already exists")
				}
			}
			s.Parameters = append(s.Parameters, *op.Parameter)
			replacement = "{{param." + op.Parameter.Name + "}}"
		}
		body := action.Body
		var messages []string
		if raw, ok := action.Params["messages"]; ok {
			data, _ := json.Marshal(raw)
			if json.Unmarshal(data, &messages) != nil || op.MessageIndex < 0 || op.MessageIndex >= len(messages) {
				return errors.New("Select an existing message")
			}
			body = messages[op.MessageIndex]
		} else if op.MessageIndex != 0 {
			return errors.New("Select message index zero for a single payload")
		}
		updated, err := replacePayloadPointer(body, op.PayloadPointer, replacement)
		if err != nil {
			return err
		}
		if messages != nil {
			messages[op.MessageIndex] = updated
			action.Params["messages"] = messages
		} else {
			action.Body = updated
		}
	default:
		return errors.New("Unsupported reviewed batch operation")
	}
	return nil
}
func removeJMeterRepairCode(s *dsl.Script, code string) {
	_ = dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			importreview.RemoveActionCodes(ref.Action, func(c string) bool { return c == code })
		}
		return nil
	})
}
func replacePayloadPointer(body, pointer, value string) (string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return "", errors.New("Select a JSON payload field using a JSON pointer")
	}
	var root any
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil {
		return "", errors.New("Selected payload is not JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("Selected payload must contain exactly one JSON value")
	}
	parts := strings.Split(pointer[1:], "/")
	current := root
	for i, key := range parts {
		for j := 0; j < len(key); j++ {
			if key[j] == '~' {
				if j+1 == len(key) || (key[j+1] != '0' && key[j+1] != '1') {
					return "", errors.New("Invalid JSON pointer escape")
				}
				j++
			}
		}
		key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
		last := i == len(parts)-1
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[key]
			if !ok {
				return "", errors.New("Payload field does not exist")
			}
			if last {
				node[key] = value
			} else {
				current = next
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || strconv.Itoa(index) != key || index < 0 || index >= len(node) {
				return "", errors.New("Payload array index does not exist")
			}
			if last {
				node[index] = value
			} else {
				current = node[index]
			}
		default:
			return "", fmt.Errorf("Payload pointer traverses a scalar")
		}
	}
	out, err := json.Marshal(root)
	return string(out), err
}

func batchDisclosure(s *dsl.Script) *dsl.Script {
	p, err := scriptdisclosure.Project(s)
	if err != nil {
		return nil
	}
	return p
}

func refreshBatchCredentialConfig(s *dsl.Script) {
	// The source remains immutable in the parent. These mirrors describe the
	// current native action configuration and must not retain obsolete bindings.
	for i := range s.Actions {
		a := &s.Actions[i]
		if m, ok := a.Metadata["jmeter_kafka"].(map[string]any); ok {
			m["native_config"] = s.Config.Kafka
		}
		if m, ok := a.Metadata["jmeter_eventhubs"].(map[string]any); ok {
			m["native_config"] = s.Config.EventHubs
		}
	}
	stripProtectedImportCredentialMetadata(s)
}

// JMeterBatchRepairResponse documents the batch variant of the existing repair
// endpoints. Its scripts are disclosure projections, never source escrow.
type JMeterBatchRepairResponse struct {
	Diagnostics               diag.Diagnostics                  `json:"diagnostics"`
	ReviewSummary             *jmeter.ReviewSummary             `json:"review_summary"`
	Warnings                  []string                          `json:"warnings"`
	UnsupportedElements       []string                          `json:"unsupported_elements"`
	Preflight                 *ReplayPreflight                  `json:"preflight"`
	Batch                     jmeterBatchIdentity               `json:"batch"`
	PreviewDigest             string                            `json:"preview_digest"`
	Before                    []*dsl.Script                     `json:"before"`
	Scripts                   []*dsl.Script                     `json:"scripts"`
	ScenarioDraft             *jmeter.ScenarioDraft             `json:"scenario_draft,omitempty"`
	Findings                  map[string][]importreview.Finding `json:"findings"`
	Readiness                 map[string]importreview.Readiness `json:"readiness"`
	SecretBindingRequirements []SecretBindingRequirement        `json:"secret_binding_requirements,omitempty"`
	ExpiresAt                 time.Time                         `json:"expires_at"`
}
