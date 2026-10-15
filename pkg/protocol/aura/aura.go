// Package aura validates bounded Aura action envelopes without disclosing source
// identities, descriptors, payloads, tokens, or server error text in reports.
package aura

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	MaxEnvelopeBytes = 8 << 20
	MaxDepth         = 128
	MaxActions       = 256
	MaxTokens        = 131072
	MaxIDBytes       = 1024
	MaxFormFields    = 4096
)

type State string

const (
	Success    State = "SUCCESS"
	Error      State = "ERROR"
	Incomplete State = "INCOMPLETE"
	Aborted    State = "ABORTED"
	Unknown    State = "UNKNOWN"
)

type Code string

const (
	InvalidJSON        Code = "invalid_json"
	DuplicateMember    Code = "duplicate_member"
	ByteLimit          Code = "byte_limit"
	DepthLimit         Code = "depth_limit"
	WorkLimit          Code = "work_limit"
	ActionLimit        Code = "action_limit"
	InvalidEnvelope    Code = "invalid_envelope"
	EmptyActions       Code = "empty_actions"
	InvalidAction      Code = "invalid_action"
	InvalidID          Code = "invalid_id"
	DuplicateID        Code = "duplicate_id"
	MissingDescriptor  Code = "missing_descriptor"
	MissingState       Code = "missing_state"
	UnknownState       Code = "unknown_state"
	NonSuccessState    Code = "non_success_state"
	MissingResponse    Code = "missing_response"
	UnexpectedResponse Code = "unexpected_response"
	InvalidForm        Code = "invalid_form"
	MissingMessage     Code = "missing_message"
	DuplicateMessage   Code = "duplicate_message"
	// Framework errors answer the whole batch instead of its actions: the
	// session is invalid, the client is out of sync with the server, or
	// another Aura exception event.
	InvalidSession  Code = "invalid_session"
	ClientOutOfSync Code = "client_out_of_sync"
	FrameworkError  Code = "framework_error"
)

// IsFrameworkError reports a response-level Aura framework error, a known
// failure rather than an ambiguous envelope.
func IsFrameworkError(code Code) bool {
	return code == InvalidSession || code == ClientOutOfSync || code == FrameworkError
}

type Outcome struct {
	Ordinal int   `json:"ordinal"`
	State   State `json:"state"`
	// Recorded is the action's unsuccessful state in the recording, when the
	// import captured one.
	Recorded State `json:"recorded,omitempty"`
}

type Issue struct {
	Code    Code `json:"code"`
	Ordinal int  `json:"ordinal,omitempty"`
	// RecordedFailure marks a non_success_state on an action that also failed
	// in the recording, so it is not mistaken for a correlation problem.
	RecordedFailure bool `json:"recorded_failure,omitempty"`
}

// Report is deliberately value-free. Request-only Success means structural
// request validity, never a successful response; consult HasResponse explicitly.
// Complete remains true for known unsuccessful states, but is false when the
// envelope, identity coverage, limits, or state semantics are ambiguous.
type Report struct {
	Success         bool `json:"success"`
	Complete        bool `json:"complete"`
	HasResponse     bool `json:"has_response"`
	RequestActions  int  `json:"request_actions"`
	ResponseActions int  `json:"response_actions"`
	MatchedActions  int  `json:"matched_actions"`
	// Nonzero omission counts describe retained runtime diagnostics only.
	// Validation still evaluates every action permitted by the parser bounds.
	OutcomesOmitted int `json:"outcomes_omitted,omitempty"`
	IssuesOmitted   int `json:"issues_omitted,omitempty"`
	// AcceptedRecordedFailures counts actions that ended in the same
	// unsuccessful state as the recording and were accepted by policy.
	AcceptedRecordedFailures int       `json:"accepted_recorded_failures,omitempty"`
	Outcomes                 []Outcome `json:"outcomes,omitempty"`
	Issues                   []Issue   `json:"issues,omitempty"`
}

// Err provides only an enum and ordinal, never parser errors or source values.
func (r Report) Err() error {
	if r.Success {
		return nil
	}
	if len(r.Issues) == 0 {
		return fmt.Errorf("Aura validation failed")
	}
	issue := r.Issues[0]
	recorded := ""
	if issue.RecordedFailure {
		recorded = " (also failed in the recording)"
	}
	if issue.Ordinal > 0 {
		return fmt.Errorf("Aura validation failed: %s at action %d%s", issue.Code, issue.Ordinal, recorded)
	}
	return fmt.Errorf("Aura validation failed: %s%s", issue.Code, recorded)
}

// RecordedOutcome is an action's unsuccessful state in the recording, by its
// position in the request envelope.
type RecordedOutcome struct {
	Ordinal int   `json:"ordinal"`
	State   State `json:"state"`
}

// Baseline is the recorded outcome of a batch's actions that did not succeed.
type Baseline struct {
	Recorded []RecordedOutcome `json:"recorded"`
}

// ParseBaseline reads a baseline from an assertion's expected value (nil means
// none). It runs for every Aura request at replay, so the forms it meets
// there, a Baseline or its decoded JSON, are read directly; anything else
// goes through JSON.
func ParseBaseline(expected any) (map[int]State, error) {
	var outcomes []RecordedOutcome
	switch typed := expected.(type) {
	case nil:
		return nil, nil
	case Baseline:
		outcomes = typed.Recorded
	case *Baseline:
		if typed == nil {
			return nil, nil
		}
		outcomes = typed.Recorded
	case map[string]any:
		decoded, err := decodedBaseline(typed)
		if err != nil {
			return nil, err
		}
		outcomes = decoded
	default:
		encoded, err := json.Marshal(expected)
		if err != nil {
			return nil, errInvalidBaseline
		}
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			return nil, errInvalidBaseline
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errInvalidBaseline
		}
		decoded, err := decodedBaseline(object)
		if err != nil {
			return nil, err
		}
		outcomes = decoded
	}
	if len(outcomes) == 0 || len(outcomes) > MaxActions {
		return nil, fmt.Errorf("recorded baseline must list 1 to %d actions", MaxActions)
	}
	recorded := make(map[int]State, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Ordinal < 1 || outcome.Ordinal > MaxActions {
			return nil, fmt.Errorf("recorded baseline ordinal must be between 1 and %d", MaxActions)
		}
		switch outcome.State {
		case Error, Incomplete, Aborted:
		default:
			return nil, fmt.Errorf("recorded baseline state must be ERROR, INCOMPLETE or ABORTED")
		}
		if _, duplicate := recorded[outcome.Ordinal]; duplicate {
			return nil, fmt.Errorf("recorded baseline repeats action %d", outcome.Ordinal)
		}
		recorded[outcome.Ordinal] = outcome.State
	}
	return recorded, nil
}

var errInvalidBaseline = fmt.Errorf("recorded baseline must be {\"recorded\": [{\"ordinal\", \"state\"}]}")

// decodedBaseline reads the decoded JSON form, rejecting unknown members and
// non-integer ordinals.
func decodedBaseline(object map[string]any) ([]RecordedOutcome, error) {
	if len(object) != 1 {
		return nil, errInvalidBaseline
	}
	items, ok := object["recorded"].([]any)
	if !ok {
		return nil, errInvalidBaseline
	}
	outcomes := make([]RecordedOutcome, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok || len(entry) != 2 {
			return nil, errInvalidBaseline
		}
		ordinal, ok := baselineOrdinal(entry["ordinal"])
		state, isString := entry["state"].(string)
		if !ok || !isString {
			return nil, errInvalidBaseline
		}
		outcomes = append(outcomes, RecordedOutcome{Ordinal: ordinal, State: State(state)})
	}
	return outcomes, nil
}

func baselineOrdinal(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != math.Trunc(typed) || typed < math.MinInt32 || typed > math.MaxInt32 {
			return 0, false
		}
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed < math.MinInt32 || parsed > math.MaxInt32 {
			return 0, false
		}
		return int(parsed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

// ApplyRecordedBaseline relates a validated replay to the recording: actions
// that failed again are marked as recorded failures, and with accept an
// action that ended in the same unsuccessful state as the recording is
// accepted rather than reported. Structural issues are never affected.
func (r *Report) ApplyRecordedBaseline(recorded map[int]State, accept bool) {
	if len(recorded) == 0 {
		return
	}
	replayed := make(map[int]State, len(r.Outcomes))
	for index := range r.Outcomes {
		outcome := &r.Outcomes[index]
		replayed[outcome.Ordinal] = outcome.State
		outcome.Recorded = recorded[outcome.Ordinal]
	}
	issues := make([]Issue, 0, len(r.Issues))
	for _, issue := range r.Issues {
		if state, ok := recorded[issue.Ordinal]; ok && issue.Code == NonSuccessState {
			if accept && replayed[issue.Ordinal] == state {
				r.AcceptedRecordedFailures++
				continue
			}
			issue.RecordedFailure = true
		}
		issues = append(issues, issue)
	}
	r.Issues = issues
	r.Success = r.Complete && len(r.Issues) == 0
}

func ValidateRequest(body []byte) Report {
	actions, issues := parseRequest(body)
	return Report{Success: len(issues) == 0, Complete: len(issues) == 0, RequestActions: len(actions), Issues: issues}
}

func InspectResponse(body []byte) Report {
	actions, issues := parseEnvelope(body, false)
	report := Report{Complete: len(issues) == 0 || IsFrameworkError(issues[0].Code), HasResponse: true, ResponseActions: len(actions), Issues: issues}
	for i, action := range actions {
		report.addOutcome(i+1, action.state)
	}
	report.Success = report.Complete && len(report.Issues) == 0
	return report
}

// Validate matches responses against the actual outgoing resolved request
// envelope, independently of response order or captured action-ID constants.
func Validate(requestBody, responseBody []byte) Report {
	requests, requestIssues := parseRequest(requestBody)
	responses, responseIssues := parseEnvelope(responseBody, false)
	// A framework error is a known failure of the whole batch, so the report is
	// complete although no action has an outcome.
	frameworkError := len(responseIssues) > 0 && IsFrameworkError(responseIssues[0].Code)
	report := Report{Complete: len(requestIssues) == 0 && (len(responseIssues) == 0 || frameworkError), HasResponse: true,
		RequestActions: len(requests), ResponseActions: len(responses)}
	report.Issues = append(requestIssues, responseIssues...)
	if !report.Complete || frameworkError {
		return report
	}
	byID := make(map[string]action, len(responses))
	for _, response := range responses {
		byID[response.id] = response
	}
	requested := make(map[string]bool, len(requests))
	for i, request := range requests {
		requested[request.id] = true
		response, exists := byID[request.id]
		if !exists {
			report.Complete = false
			report.Issues = append(report.Issues, Issue{Code: MissingResponse, Ordinal: i + 1})
			continue
		}
		report.MatchedActions++
		report.addOutcome(i+1, response.state)
	}
	for i, response := range responses {
		if !requested[response.id] {
			report.Complete = false
			report.Issues = append(report.Issues, Issue{Code: UnexpectedResponse, Ordinal: i + 1})
		}
	}
	report.Success = report.Complete && len(report.Issues) == 0
	return report
}

func (r *Report) addOutcome(ordinal int, state State) {
	r.Outcomes = append(r.Outcomes, Outcome{Ordinal: ordinal, State: state})
	if state == Unknown {
		r.Complete = false
		r.Issues = append(r.Issues, Issue{Code: UnknownState, Ordinal: ordinal})
	} else if state != Success {
		r.Issues = append(r.Issues, Issue{Code: NonSuccessState, Ordinal: ordinal})
	}
}

type action struct {
	id    string
	state State
	// descriptor and apex name a request action when names are read.
	descriptor string
	apex       string
}

func parseRequest(body []byte) ([]action, []Issue) {
	return parseRequestEnvelope(body, false)
}

func parseRequestEnvelope(body []byte, names bool) ([]action, []Issue) {
	if len(body) > MaxEnvelopeBytes {
		return nil, []Issue{{Code: ByteLimit}}
	}
	if !utf8.Valid(body) {
		return nil, []Issue{{Code: InvalidJSON}}
	}
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		return parseEnvelopeWith(trimmed, true, names)
	}
	if bytes.Count(trimmed, []byte("&"))+1 > MaxFormFields {
		return nil, []Issue{{Code: WorkLimit}}
	}
	values, err := url.ParseQuery(string(trimmed))
	if err != nil {
		return nil, []Issue{{Code: InvalidForm}}
	}
	messages := values["message"]
	if len(messages) == 0 {
		return nil, []Issue{{Code: MissingMessage}}
	}
	if len(messages) != 1 {
		return nil, []Issue{{Code: DuplicateMessage}}
	}
	return parseEnvelopeWith([]byte(messages[0]), true, names)
}

func parseEnvelope(body []byte, request bool) ([]action, []Issue) {
	return parseEnvelopeWith(body, request, false)
}

// parseEnvelopeWith also reads each request action's descriptor and, for an
// Apex call, its namespace, class and method when names is set; nothing else
// in params is ever kept.
func parseEnvelopeWith(body []byte, request bool, names bool) ([]action, []Issue) {
	fail := func(code Code, ordinal int) ([]action, []Issue) { return nil, []Issue{{Code: code, Ordinal: ordinal}} }
	if len(body) > MaxEnvelopeBytes {
		return fail(ByteLimit, 0)
	}
	if !request {
		if inner, wrapped := unwrapFrameworkError(body); wrapped {
			return fail(frameworkErrorCode(inner), 0)
		}
	}
	if !utf8.Valid(body) || !validStringEscapes(body) {
		return fail(InvalidJSON, 0)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	scanner := scanner{decoder: decoder, names: names}
	value, code := scanner.read(0, envelopeMode)
	if code != "" {
		return fail(code, 0)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fail(InvalidJSON, 0)
	}
	envelope, ok := value.(map[string]any)
	if !ok {
		return fail(InvalidEnvelope, 0)
	}
	items, ok := envelope["actions"].([]any)
	if !ok {
		// The client decodes a non-200 body directly and treats an exception
		// event there as a framework error too.
		if !request && envelope["exceptionEvent"] == true {
			return fail(frameworkErrorCode(body), 0)
		}
		return fail(InvalidEnvelope, 0)
	}
	if len(items) == 0 {
		return fail(EmptyActions, 0)
	}
	actions := make([]action, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return fail(InvalidAction, i+1)
		}
		id, ok := object["id"].(string)
		if !ok || strings.TrimSpace(id) == "" || len(id) > MaxIDBytes {
			return fail(InvalidID, i+1)
		}
		if seen[id] {
			return fail(DuplicateID, i+1)
		}
		seen[id] = true
		current := action{id: id}
		if request {
			descriptor, ok := object["descriptor"].(string)
			if !ok || strings.TrimSpace(descriptor) == "" {
				return fail(MissingDescriptor, i+1)
			}
			if names {
				current.descriptor = descriptor
				current.apex = apexName(descriptor, object["params"])
			}
		} else {
			state, ok := object["state"].(string)
			if !ok || state == "" {
				return fail(MissingState, i+1)
			}
			switch State(state) {
			case Success, Error, Incomplete, Aborted:
				current.state = State(state)
			default:
				current.state = Unknown
			}
		}
		actions = append(actions, current)
	}
	return actions, nil
}

type mode int

const (
	discardMode mode = iota
	envelopeMode
	actionsMode
	actionMode
	scalarMode
	frameworkErrorMode
	eventMode
	paramsMode
)

type scanner struct {
	decoder *json.Decoder
	tokens  int
	// names also keeps an action's params namespace, classname and method.
	names bool
}

func (s *scanner) token() (json.Token, Code) {
	s.tokens++
	if s.tokens > MaxTokens {
		return nil, WorkLimit
	}
	token, err := s.decoder.Token()
	if err != nil {
		return nil, InvalidJSON
	}
	return token, ""
}

// read validates all JSON (including ignored payloads), but retains only the
// envelope/actions/id/descriptor/state structure needed for matching. A member
// the validator reads may appear once per object, escaped aliases included, so
// its value cannot depend on which duplicate a decoder keeps; duplicates
// elsewhere (payloads, unread fields) do not change the result and are
// accepted. Both token count and nesting are bounded before recursion.
func (s *scanner) read(depth int, retain mode) (any, Code) {
	token, code := s.token()
	if code != "" {
		return nil, code
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		if retain == scalarMode {
			return token, ""
		}
		return nil, ""
	}
	if depth >= MaxDepth {
		return nil, DepthLimit
	}
	switch delimiter {
	case '{':
		var object map[string]any
		if retain == envelopeMode || retain == actionMode || retain == frameworkErrorMode || retain == eventMode || retain == paramsMode {
			object = make(map[string]any)
		}
		for s.decoder.More() {
			token, code := s.token()
			if code != "" {
				return nil, code
			}
			key, ok := token.(string)
			if !ok {
				return nil, InvalidJSON
			}
			childMode := discardMode
			switch {
			case retain == envelopeMode && key == "actions":
				childMode = actionsMode
			case retain == actionMode && (key == "id" || key == "descriptor" || key == "state"),
				(retain == envelopeMode || retain == frameworkErrorMode) && key == "exceptionEvent",
				retain == eventMode && key == "descriptor":
				childMode = scalarMode
			case retain == frameworkErrorMode && key == "event":
				childMode = eventMode
			case s.names && retain == actionMode && key == "params":
				childMode = paramsMode
			case retain == paramsMode && (key == "namespace" || key == "classname" || key == "method"):
				childMode = scalarMode
			}
			if childMode == discardMode {
				if _, code := s.read(depth+1, discardMode); code != "" {
					return nil, code
				}
				continue
			}
			if _, repeated := object[key]; repeated {
				return nil, DuplicateMember
			}
			value, code := s.read(depth+1, childMode)
			if code != "" {
				return nil, code
			}
			object[key] = value
		}
		close, code := s.token()
		if code != "" {
			return nil, code
		}
		if close != json.Delim('}') {
			return nil, InvalidJSON
		}
		if object != nil {
			return object, ""
		}
		return nil, ""
	case '[':
		var items []any
		if retain == actionsMode {
			items = make([]any, 0)
		}
		for s.decoder.More() {
			childMode := discardMode
			if retain == actionsMode {
				if len(items) >= MaxActions {
					return nil, ActionLimit
				}
				childMode = actionMode
			}
			value, code := s.read(depth+1, childMode)
			if code != "" {
				return nil, code
			}
			if retain == actionsMode {
				items = append(items, value)
			}
		}
		close, code := s.token()
		if code != "" {
			return nil, code
		}
		if close != json.Delim(']') {
			return nil, InvalidJSON
		}
		if retain == actionsMode {
			return items, ""
		}
		return nil, ""
	default:
		return nil, InvalidJSON
	}
}

// unwrapFrameworkError mirrors the Aura client's decode of a response that
// ends in "/*ERROR*/": the JSON lies between the first "*/" and the last "/*".
func unwrapFrameworkError(body []byte) ([]byte, bool) {
	trimmed := bytes.TrimRight(body, " \t\r\n")
	if !bytes.HasSuffix(trimmed, []byte("/*ERROR*/")) {
		return nil, false
	}
	start, end := bytes.Index(trimmed, []byte("*/")), bytes.LastIndex(trimmed, []byte("/*"))
	if start < 0 || end < start+2 {
		return nil, true
	}
	return trimmed[start+2 : end], true
}

// frameworkErrorCode classifies a framework error body the way the Aura client
// does: an exception event for aura:invalidSession or aura:clientOutOfSync, or
// any other error. Only the enum is reported, never the event or its message.
func frameworkErrorCode(body []byte) Code {
	if len(body) == 0 || !utf8.Valid(body) || !validStringEscapes(body) {
		return FrameworkError
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	scanner := scanner{decoder: decoder}
	value, code := scanner.read(0, frameworkErrorMode)
	if code != "" {
		return FrameworkError
	}
	if _, err := decoder.Token(); err != io.EOF {
		return FrameworkError
	}
	object, _ := value.(map[string]any)
	if object["exceptionEvent"] != true {
		return FrameworkError
	}
	event, _ := object["event"].(map[string]any)
	descriptor, _ := event["descriptor"].(string)
	switch strings.TrimPrefix(descriptor, "markup://") {
	case "aura:invalidSession":
		return InvalidSession
	case "aura:clientOutOfSync":
		return ClientOutOfSync
	default:
		return FrameworkError
	}
}

// encoding/json replaces unpaired UTF-16 surrogates with U+FFFD. Reject them
// before decoding, so malformed keys/identities cannot acquire aliases.
func validStringEscapes(body []byte) bool {
	inString := false
	for i := 0; i < len(body); i++ {
		if !inString {
			if body[i] == '"' {
				inString = true
			}
			continue
		}
		if body[i] == '"' {
			inString = false
			continue
		}
		if body[i] < 0x20 {
			return false
		}
		if body[i] != '\\' {
			continue
		}
		if i+1 >= len(body) {
			return false
		}
		if body[i+1] != 'u' {
			i++
			continue
		}
		code, ok := unicodeEscape(body, i)
		if !ok {
			return false
		}
		if code >= 0xd800 && code <= 0xdbff {
			low, ok := unicodeEscape(body, i+6)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 11
		} else {
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			i += 5
		}
	}
	return !inString
}
func unicodeEscape(body []byte, offset int) (uint16, bool) {
	if offset+6 > len(body) || body[offset] != '\\' || body[offset+1] != 'u' {
		return 0, false
	}
	var value uint16
	for _, digit := range body[offset+2 : offset+6] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}

// apexDescriptor is the descriptor of a call to an Apex method.
const apexDescriptor = "aura://ApexActionController/ACTION$execute"

// MaxNameBytes bounds an action name.
const MaxNameBytes = 512

// RequestAction names one action of an Aura request: its descriptor and, for
// an Apex call, the Apex class and method (namespace.Class.method). Nothing
// the action sends is kept.
type RequestAction struct {
	Descriptor string
	Apex       string
}

// Name is the Apex method of an Apex call, else the descriptor.
func (a RequestAction) Name() string {
	if a.Apex != "" {
		return a.Apex
	}
	return a.Descriptor
}

// RequestActions names the actions of an Aura request (the form Aura posts,
// or a JSON envelope), in order.
func RequestActions(body []byte) ([]RequestAction, error) {
	actions, issues := parseRequestEnvelope(body, true)
	if len(issues) > 0 {
		return nil, Report{Issues: issues}.Err()
	}
	out := make([]RequestAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, RequestAction{Descriptor: boundName(action.descriptor), Apex: boundName(action.apex)})
	}
	return out, nil
}

// apexName is namespace.Class.method for an Apex call, or "".
func apexName(descriptor string, params any) string {
	if descriptor != apexDescriptor {
		return ""
	}
	fields, _ := params.(map[string]any)
	class, _ := fields["classname"].(string)
	method, _ := fields["method"].(string)
	if strings.TrimSpace(class) == "" || strings.TrimSpace(method) == "" {
		return ""
	}
	name := class + "." + method
	if namespace, _ := fields["namespace"].(string); strings.TrimSpace(namespace) != "" {
		name = namespace + "." + name
	}
	return name
}

func boundName(name string) string {
	if len(name) <= MaxNameBytes {
		return name
	}
	cut := MaxNameBytes
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}
