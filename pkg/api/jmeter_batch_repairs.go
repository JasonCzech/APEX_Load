package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	explainJMeterReviewDiagnostics(evidence.Diagnostics)
	// The batch keeps the converter's warnings, which the review evidence
	// rebuilt from the stored scripts cannot recover; without them every
	// script's replay preflight would be rebuilt with none.
	warnings := append(append([]string(nil), b.Warnings...), evidence.Warnings...)
	draft := importDraft{Source: "jmeter", ProjectID: b.ProjectID, Scripts: b.Scripts, SourceReview: b.SourceReview, Unsupported: evidence.Diagnostics, Warnings: warnings, ScriptWarnings: jmeterBatchScriptWarnings(b)}
	augmented := draft.Augment(r)
	applyJMeterReviewPreflight(augmented.Preflight, evidence)
	response := map[string]any{"batch": b.Identity, "preview_digest": digest, "before": before, "scripts": scripts, "scenario_draft": b.Scenario, "findings": findings, "readiness": readiness, "expires_at": b.ExpiresAt, "diagnostics": evidence.Diagnostics, "review_summary": evidence.ReviewSummary, "warnings": augmented.Warnings, "unsupported_elements": evidence.UnsupportedElements, "preflight": augmented.Preflight, "repair_suggestions": jmeterReviewRepairSuggestions(b.Scripts)}
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
	requirements, _ := secured["secret_binding_requirements"].([]SecretBindingRequirement)
	// consumed is the batch's previous session. Until the replacement batch
	// and its receipt are both stored, the stored revision still names it, so
	// a failure puts it back for the retry the error asks for.
	var consumed *sourceSecretSession
	defer func() { clearSourceSecretSession(consumed) }()
	rollbackSession := func() {
		if b.SourceSecretSessionID != "" {
			_, _ = s.discardSourceSecretSession(r, b.ProjectID, b.SourceSecretSessionID)
		}
		if consumed != nil {
			_ = s.restoreSourceSecretSession(r, consumed)
		}
	}
	if previousSession := b.SourceSecretSessionID; strings.TrimSpace(previousSession) != "" {
		var sessionID string
		var expiresAt time.Time
		sessionID, expiresAt, consumed = s.rekeyJMeterBatchSourceSecretSession(r, b.ProjectID, previousSession, b.SourceSecretRequirements, requirements)
		recordJMeterBatchSourceSecretSession(b, sessionID, requirements)
		if b.SourceSecretSessionID != "" {
			response["source_secret_session_id"] = b.SourceSecretSessionID
			response["source_secret_session_expires_at"] = expiresAt
			response["source_secret_migration_allowed"] = true
		} else {
			// The offer is gone (migrated, discarded, expired or no longer
			// allowed); tell the client to drop the old ID.
			response["source_secret_session_id"] = ""
		}
	}
	response["secret_binding_requirements"] = secured["secret_binding_requirements"]
	response["batch"] = b.Identity
	if err = s.saveJMeterBatch(r, b); err != nil {
		rollbackSession()
		writeError(w, 503, "Cannot secure replacement batch")
		return
	}
	if err = s.publishBatchReceipt(r, &old, claim, jmeterBatchReceipt{Kind: "repair", RequestDigest: requestDigest, Digest: digest, Response: response}); err != nil {
		// Roll back only when the receipt is known to be absent: a published
		// receipt hands the retry the replacement session ID.
		if _, rerr := s.openBatchReceipt(r, id.Revision, req.ProjectID); errors.Is(rerr, os.ErrNotExist) {
			rollbackSession()
		}
		writeError(w, 409, "Batch publication failed; retry the same operation")
		return
	}
	// Receipt is authoritative even if old claim cleanup fails.
	_ = s.completeImportCandidateClaim(r.Context(), claim)
	writeJSON(w, 200, response)
}

// rekeyJMeterBatchSourceSecretSession consumes the batch's source-secret
// migration session and registers a replacement holding the values whose
// requirements the repaired batch still has, marking those requirements
// source_value_available. Requirement IDs are not stable across responses, so
// each value follows its recorded requirement to the one current requirement
// at the same script and path; batch operations never move scripts or
// fields. A value whose requirement a repair resolved (a bind operation that
// put a secret reference in its place), or that matches no single
// requirement, is dropped. It returns the replacement ID and expiry, or ""
// when no value survives or the session is gone, expired, or no longer
// allowed, plus the consumed session (nil when nothing was consumed) so the
// caller can restore it if storing the replacement batch fails; the caller
// clears it.
func (s *Server) rekeyJMeterBatchSourceSecretSession(r *http.Request, batchProjectID, sessionID string, recorded []jmeterBatchSourceSecretRequirement, current []SecretBindingRequirement) (string, time.Time, *sourceSecretSession) {
	projectID := normalizeProjectID(batchProjectID)
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if projectID != "" && projectID != authorizedProjectID {
			return "", time.Time{}, nil
		}
		projectID = authorizedProjectID
	}
	session, err := s.consumeSourceSecretSession(r, projectID, sessionID)
	if err != nil || session == nil {
		return "", time.Time{}, nil
	}
	type location struct {
		script int
		path   string
	}
	matches := make(map[location][]string, len(current))
	for _, requirement := range current {
		at := location{requirement.ScriptIndex, requirement.Path}
		matches[at] = append(matches[at], requirement.ID)
	}
	values := make(map[string]string, len(session.Values))
	defer clear(values)
	for _, previous := range recorded {
		value, ok := session.Values[previous.ID]
		if !ok {
			continue
		}
		if ids := matches[location{previous.ScriptIndex, previous.Path}]; len(ids) == 1 {
			values[ids[0]] = value
		}
	}
	if len(values) == 0 {
		return "", time.Time{}, session
	}
	newSessionID, err := s.registerSourceSecretSession(r, session.ProjectID, values, session.FeatureIDs...)
	if err != nil || newSessionID == "" {
		return "", time.Time{}, session
	}
	for index := range current {
		if _, available := values[current[index].ID]; available {
			current[index].SourceValueAvailable = true
		}
	}
	return newSessionID, time.Now().UTC().Add(sourceSecretSessionTTL), session
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
		// JMeter formats __time in the JVM's default zone. UTC is the usual
		// answer, but a plan that ran in a local zone (often the one picked as
		// the source timezone at import) is reproduced only by confirming that
		// zone instead.
		zone, ok := jmeterReferenceTimezone(strings.TrimSpace(op.Timezone))
		if !op.ConfirmNoOverrides || !ok {
			return errors.New("Confirm the JMeter reference timezone (UTC, or an IANA zone such as America/Chicago) and that no JMeter property overrides apply")
		}
		// time_mode=utc built the clock parameters to send true UTC, so a
		// local zone here would silently undo that import choice.
		if zone != "UTC" {
			for _, s := range selected {
				if jmeterPreservedTimeMode(s) == "utc" {
					return fmt.Errorf("This import chose time mode utc, so its timestamps are sent in UTC; confirm UTC, or re-import with the source time mode to reproduce %s", zone)
				}
			}
		}
		for _, s := range selected {
			s.Config.JMeter.ContextConfirmed = true
			s.Config.JMeter.Timezone = zone
			for i := range s.Parameters {
				if s.Parameters[i].Type == "timestamp" || s.Parameters[i].Type == "date_time" {
					s.Parameters[i].Timezone = zone
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
		s.Config.Kafka.OAuth.ClientSecret = ""
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

// jmeterPreservedTimeMode returns the time mode the import recorded in
// preserved time_resolution, typed or decoded from a stored batch.
func jmeterPreservedTimeMode(s *dsl.Script) string {
	if s == nil || s.Metadata == nil || s.Metadata.Import == nil {
		return ""
	}
	switch resolution := s.Metadata.Import.Preserved["time_resolution"].(type) {
	case map[string]string:
		return resolution["mode"]
	case map[string]any:
		mode, _ := resolution["mode"].(string)
		return mode
	}
	return ""
}

// jmeterReferenceTimezone returns the canonical spelling of a named zone the
// runtime can load, which is what a confirmation stores. "Local" is refused: it
// is the generator's zone, which is what confirmation pins down. A UTC alias
// (Etc/UTC, Zulu, utc) is stored as UTC.
func jmeterReferenceTimezone(zone string) (string, bool) {
	if zone == "" || strings.EqualFold(zone, "local") {
		return "", false
	}
	for _, alias := range []string{"UTC", "Etc/UTC", "UCT", "Etc/UCT", "Universal", "Etc/Universal", "Zulu", "Etc/Zulu"} {
		if strings.EqualFold(zone, alias) {
			return "UTC", true
		}
	}
	if _, err := time.LoadLocation(zone); err != nil || !jmeterZoneSpelledExactly(zone) {
		return "", false
	}
	return zone, true
}

// jmeterZoneSpelledExactly reports whether zone is spelled as the zoneinfo
// file it loads from. On a case-insensitive filesystem (macOS)
// time.LoadLocation also loads "america/chicago", which a Linux generator
// cannot. The directories are the ones time.LoadLocation searches. Each is
// walked by its own directory entries, so no path is built from zone: a
// component found only under another case is misspelled, and a zone found in
// none of them came from a zip, whose lookup is case-sensitive.
func jmeterZoneSpelledExactly(zone string) bool {
	parts := strings.Split(zone, "/")
	if slices.ContainsFunc(parts, func(part string) bool { return part == "" || part == "." || part == ".." }) {
		return false
	}
	roots := []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ", "/etc/zoneinfo"}
	if env := os.Getenv("ZONEINFO"); env != "" {
		roots = append([]string{env}, roots...)
	}
	for _, root := range roots {
		if spelled, found := jmeterZoneInRoot(root, parts); found {
			return spelled
		}
	}
	return true
}

// jmeterZoneInRoot walks root one zone component at a time. found reports
// whether every component exists under some case; spelled whether each one
// matched exactly.
func jmeterZoneInRoot(root string, parts []string) (spelled, found bool) {
	dir, spelled := root, true
	for _, part := range parts {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false, false
		}
		at := slices.IndexFunc(entries, func(e os.DirEntry) bool { return e.Name() == part })
		if at < 0 {
			at = slices.IndexFunc(entries, func(e os.DirEntry) bool { return strings.EqualFold(e.Name(), part) })
			spelled = false
		}
		if at < 0 {
			return false, false
		}
		dir = filepath.Join(dir, entries[at].Name())
	}
	return spelled, true
}

func removeJMeterRepairCode(s *dsl.Script, code string) {
	_ = dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			importreview.RemoveActionCodes(ref.Action, func(c string) bool { return c == code })
		}
		return nil
	})
}

// replacePayloadPointer replaces the JSON value at pointer with value as a JSON
// string. Only that value's bytes change: the reviewed payload keeps its key
// order, whitespace and escapes everywhere else, because the source sampler
// sent those exact bytes.
func replacePayloadPointer(body, pointer, value string) (string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return "", errors.New("Select a JSON payload field using a JSON pointer")
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	if decoder.Decode(new(json.RawMessage)) != nil {
		return "", errors.New("Selected payload is not JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", errors.New("Selected payload must contain exactly one JSON value")
	}
	parts := strings.Split(pointer[1:], "/")
	for i, key := range parts {
		for j := 0; j < len(key); j++ {
			if key[j] == '~' {
				if j+1 == len(key) || (key[j+1] != '0' && key[j+1] != '1') {
					return "", errors.New("Invalid JSON pointer escape")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
	}
	start, end, err := jsonPointerValueSpan(body, parts)
	if err != nil {
		return "", err
	}
	var encoded strings.Builder
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return body[:start] + strings.TrimSuffix(encoded.String(), "\n") + body[end:], nil
}

// jsonPointerValueSpan returns the byte span of the value the unescaped
// pointer tokens select in data, which must be one valid JSON value. Like
// encoding/json, a duplicated object key selects its last occurrence.
func jsonPointerValueSpan(data string, tokens []string) (int, int, error) {
	start := skipJSONSpace(data, 0)
	end := scanJSONValue(data, start)
	for _, token := range tokens {
		switch data[start] {
		case '{':
			found := -1
			foundEnd := -1
			at := skipJSONSpace(data, start+1)
			for data[at] != '}' {
				keyEnd := scanJSONValue(data, at)
				var key string
				if json.Unmarshal([]byte(data[at:keyEnd]), &key) != nil {
					return 0, 0, errors.New("Selected payload is not JSON")
				}
				valueStart := skipJSONSpace(data, skipJSONSpace(data, keyEnd)+1)
				valueEnd := scanJSONValue(data, valueStart)
				if key == token {
					found, foundEnd = valueStart, valueEnd
				}
				at = skipJSONSpace(data, valueEnd)
				if data[at] == ',' {
					at = skipJSONSpace(data, at+1)
				}
			}
			if found < 0 {
				return 0, 0, errors.New("Payload field does not exist")
			}
			start, end = found, foundEnd
		case '[':
			index, err := strconv.Atoi(token)
			if err != nil || strconv.Itoa(index) != token || index < 0 {
				return 0, 0, errors.New("Payload array index does not exist")
			}
			at := skipJSONSpace(data, start+1)
			position := 0
			found := false
			for data[at] != ']' {
				valueEnd := scanJSONValue(data, at)
				if position == index {
					start, end, found = at, valueEnd, true
					break
				}
				position++
				at = skipJSONSpace(data, valueEnd)
				if data[at] == ',' {
					at = skipJSONSpace(data, at+1)
				}
			}
			if !found {
				return 0, 0, errors.New("Payload array index does not exist")
			}
		default:
			return 0, 0, fmt.Errorf("Payload pointer traverses a scalar")
		}
	}
	return start, end, nil
}

func skipJSONSpace(data string, at int) int {
	for at < len(data) && (data[at] == ' ' || data[at] == '\t' || data[at] == '\n' || data[at] == '\r') {
		at++
	}
	return at
}

// scanJSONValue returns the end of the JSON value that starts at at in data,
// which the caller has already validated.
func scanJSONValue(data string, at int) int {
	switch data[at] {
	case '"':
		for i := at + 1; i < len(data); i++ {
			switch data[i] {
			case '\\':
				i++
			case '"':
				return i + 1
			}
		}
		return len(data)
	case '{', '[':
		depth := 0
		for i := at; i < len(data); i++ {
			switch data[i] {
			case '"':
				i = scanJSONValue(data, i) - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		return len(data)
	default:
		i := at
		for i < len(data) && !strings.ContainsRune(",}] \t\n\r", rune(data[i])) {
			i++
		}
		return i
	}
}

func batchDisclosure(s *dsl.Script) *dsl.Script {
	return cloneScriptDefinition(s)
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
	// RepairSuggestions explains each review gate the repaired batch still
	// has, as the import response does.
	RepairSuggestions []scriptImportRepairSuggestion `json:"repair_suggestions"`
	// SourceSecretSessionID is set on an apply when the import offered
	// source-secret migration: the replacement session that now holds the
	// recorded values (the previous ID is consumed), or "" when the offer is
	// gone and the client must drop its ID.
	SourceSecretSessionID        *string    `json:"source_secret_session_id,omitempty"`
	SourceSecretSessionExpiresAt *time.Time `json:"source_secret_session_expires_at,omitempty"`
	SourceSecretMigrationAllowed bool       `json:"source_secret_migration_allowed,omitempty"`
	ExpiresAt                    time.Time  `json:"expires_at"`
}
