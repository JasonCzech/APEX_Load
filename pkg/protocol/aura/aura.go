// Package aura validates bounded Aura action envelopes without disclosing source
// identities, descriptors, payloads, tokens, or server error text in reports.
package aura

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
)

type Outcome struct {
	Ordinal int   `json:"ordinal"`
	State   State `json:"state"`
}

type Issue struct {
	Code    Code `json:"code"`
	Ordinal int  `json:"ordinal,omitempty"`
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
	OutcomesOmitted int       `json:"outcomes_omitted,omitempty"`
	IssuesOmitted   int       `json:"issues_omitted,omitempty"`
	Outcomes        []Outcome `json:"outcomes,omitempty"`
	Issues          []Issue   `json:"issues,omitempty"`
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
	if issue.Ordinal > 0 {
		return fmt.Errorf("Aura validation failed: %s at action %d", issue.Code, issue.Ordinal)
	}
	return fmt.Errorf("Aura validation failed: %s", issue.Code)
}

func ValidateRequest(body []byte) Report {
	actions, issues := parseRequest(body)
	return Report{Success: len(issues) == 0, Complete: len(issues) == 0, RequestActions: len(actions), Issues: issues}
}

func InspectResponse(body []byte) Report {
	actions, issues := parseEnvelope(body, false)
	report := Report{Complete: len(issues) == 0, HasResponse: true, ResponseActions: len(actions), Issues: issues}
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
	report := Report{Complete: len(requestIssues) == 0 && len(responseIssues) == 0, HasResponse: true,
		RequestActions: len(requests), ResponseActions: len(responses)}
	report.Issues = append(requestIssues, responseIssues...)
	if !report.Complete {
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
}

func parseRequest(body []byte) ([]action, []Issue) {
	if len(body) > MaxEnvelopeBytes {
		return nil, []Issue{{Code: ByteLimit}}
	}
	if !utf8.Valid(body) {
		return nil, []Issue{{Code: InvalidJSON}}
	}
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		return parseEnvelope(trimmed, true)
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
	return parseEnvelope([]byte(messages[0]), true)
}

func parseEnvelope(body []byte, request bool) ([]action, []Issue) {
	fail := func(code Code, ordinal int) ([]action, []Issue) { return nil, []Issue{{Code: code, Ordinal: ordinal}} }
	if len(body) > MaxEnvelopeBytes {
		return fail(ByteLimit, 0)
	}
	if !utf8.Valid(body) || !validStringEscapes(body) {
		return fail(InvalidJSON, 0)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	scanner := scanner{decoder: decoder}
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
)

type scanner struct {
	decoder *json.Decoder
	tokens  int
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
// envelope/actions/id/descriptor/state structure needed for matching. Object
// key sets detect duplicates, including escaped aliases, without retaining
// payload values. Both token count and nesting are bounded before recursion.
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
		seen := make(map[string]bool)
		var object map[string]any
		if retain == envelopeMode || retain == actionMode {
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
			if seen[key] {
				return nil, DuplicateMember
			}
			seen[key] = true
			childMode := discardMode
			if retain == envelopeMode && key == "actions" {
				childMode = actionsMode
			}
			if retain == actionMode && (key == "id" || key == "descriptor" || key == "state") {
				childMode = scalarMode
			}
			value, code := s.read(depth+1, childMode)
			if code != "" {
				return nil, code
			}
			if childMode != discardMode && object != nil {
				object[key] = value
			}
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
