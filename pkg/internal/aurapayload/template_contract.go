package aurapayload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"strconv"
)

const InvalidTemplateContract Code = "invalid_template_contract"

const (
	TemplateValueEncodingJSONStrings    = "aura_json_strings"
	TemplateValueEncodingQueryComponent = "url_query_component"
)

// TemplateContract retains the input representation of existing occurrences
// when a plain form body is promoted to structural substitution. Hashes bind
// typed consumer locations and authored tokens without disclosing either.
type TemplateContract struct {
	Version             int                  `json:"version"`
	FormWireOccurrences []TemplateOccurrence `json:"form_wire_occurrences"`
	LiteralOccurrences  []TemplateOccurrence `json:"literal_occurrences"`
}

type TemplateOccurrence struct {
	ContextID        string `json:"context_id"`
	OccurrenceIndex  int    `json:"occurrence_index"`
	ExpressionSHA256 string `json:"expression_sha256"`
}

type TemplateContext struct{ formWireTokens, literalTokens map[int]bool }

func (c TemplateContext) IsFormWireToken(index int) bool { return c.formWireTokens[index] }
func (c TemplateContext) IsLiteralToken(index int) bool  { return c.literalTokens[index] }

var authoredTemplatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// ReadTemplateContract admits only the bounded, value-free versioned envelope.
// It does not marshal arbitrary metadata before applying the cardinality cap.
func ReadTemplateContract(raw any) (*TemplateContract, error) {
	if raw == nil {
		return nil, nil
	}
	var contract TemplateContract
	switch value := raw.(type) {
	case *TemplateContract:
		if value == nil {
			return nil, fail(InvalidTemplateContract)
		}
		contract = *value
	case TemplateContract:
		contract = value
	case map[string]any:
		if len(value) != 3 {
			return nil, fail(InvalidTemplateContract)
		}
		version, ok := contractInteger(value["version"])
		if !ok {
			return nil, fail(InvalidTemplateContract)
		}
		contract.Version = version
		remaining := MaxTemplateOccurrences
		for _, field := range []struct {
			key  string
			rows *[]TemplateOccurrence
		}{{"form_wire_occurrences", &contract.FormWireOccurrences}, {"literal_occurrences", &contract.LiteralOccurrences}} {
			rows := reflect.ValueOf(value[field.key])
			if !rows.IsValid() || rows.Kind() != reflect.Slice || rows.Len() > remaining {
				return nil, fail(InvalidTemplateContract)
			}
			remaining -= rows.Len()
			*field.rows = make([]TemplateOccurrence, rows.Len())
			for i := 0; i < rows.Len(); i++ {
				switch row := rows.Index(i).Interface().(type) {
				case TemplateOccurrence:
					(*field.rows)[i] = row
				case map[string]any:
					if len(row) != 3 {
						return nil, fail(InvalidTemplateContract)
					}
					context, contextOK := row["context_id"].(string)
					hash, hashOK := row["expression_sha256"].(string)
					index, indexOK := contractInteger(row["occurrence_index"])
					if !contextOK || !hashOK || !indexOK {
						return nil, fail(InvalidTemplateContract)
					}
					(*field.rows)[i] = TemplateOccurrence{ContextID: context, OccurrenceIndex: index, ExpressionSHA256: hash}
				default:
					return nil, fail(InvalidTemplateContract)
				}
			}
		}
	default:
		return nil, fail(InvalidTemplateContract)
	}
	if contract.Version != 1 || len(contract.FormWireOccurrences)+len(contract.LiteralOccurrences) > MaxTemplateOccurrences {
		return nil, fail(InvalidTemplateContract)
	}
	seen := make(map[string]bool, len(contract.FormWireOccurrences)+len(contract.LiteralOccurrences))
	for _, rows := range [][]TemplateOccurrence{contract.FormWireOccurrences, contract.LiteralOccurrences} {
		for _, row := range rows {
			if !contractHash(row.ContextID) || !contractHash(row.ExpressionSHA256) || row.OccurrenceIndex < 0 || row.OccurrenceIndex >= MaxTemplateOccurrences {
				return nil, fail(InvalidTemplateContract)
			}
			key := row.ContextID + ":" + strconv.Itoa(row.OccurrenceIndex)
			if seen[key] {
				return nil, fail(InvalidTemplateContract)
			}
			seen[key] = true
		}
	}
	contract.FormWireOccurrences = append([]TemplateOccurrence{}, contract.FormWireOccurrences...)
	contract.LiteralOccurrences = append([]TemplateOccurrence{}, contract.LiteralOccurrences...)
	return &contract, nil
}

func contractInteger(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case float64:
		if value >= 0 && value <= MaxTemplateOccurrences && math.Trunc(value) == value {
			return int(value), true
		}
	case json.Number:
		number, err := strconv.Atoi(string(value))
		return number, err == nil
	}
	return 0, false
}

func contractHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type templateSurface struct {
	id, value string
	tokens    []string
	matches   [][]int
	context   TemplateContext
}

type preparedTemplates struct {
	surfaces   []*templateSurface
	message    map[*node]*templateSurface
	fields     map[int]*templateSurface
	jsonFields map[int]*payload
	jsonLeaves map[int]map[*node]*templateSurface
}

func prepareTemplates(p *payload) (*preparedTemplates, error) {
	result := &preparedTemplates{message: map[*node]*templateSurface{}, fields: map[int]*templateSurface{}, jsonFields: map[int]*payload{}, jsonLeaves: map[int]map[*node]*templateSurface{}}
	remaining := MaxTemplateOccurrences
	add := func(kind string, fieldIndex int, route []Segment, value, fieldKey string) (*templateSurface, error) {
		routeID := ""
		if len(route) > 0 {
			var err error
			routeID, err = routeKey(route)
			if err != nil {
				return nil, err
			}
		}
		if err := p.budget.addWork(len(routeID) + len(value) + len(fieldKey) + sha256.Size*2); err != nil {
			return nil, err
		}
		id := sha256.Sum256([]byte("aura-template-context-v1\x00" + kind + "\x00" + strconv.Itoa(fieldIndex) + "\x00" + fieldKey + "\x00" + routeID))
		surface := &templateSurface{id: hex.EncodeToString(id[:]), value: value}
		matches := authoredTemplatePattern.FindAllStringIndex(value, remaining+1)
		if len(matches) > remaining {
			return nil, fail(TokenLimit)
		}
		remaining -= len(matches)
		surface.matches = matches
		for _, match := range matches {
			if err := p.budget.addWork(sha256.Size * 2); err != nil {
				return nil, err
			}
			hash := sha256.Sum256([]byte(value[match[0]:match[1]]))
			surface.tokens = append(surface.tokens, hex.EncodeToString(hash[:]))
		}
		result.surfaces = append(result.surfaces, surface)
		return surface, nil
	}
	for _, leaf := range p.inspection.Leaves {
		if leaf.ValueType != "string" {
			continue
		}
		key, _ := routeKey(leaf.Route)
		surface, err := add("message", -1, leaf.Route, leaf.Value, "message")
		if err != nil {
			return nil, err
		}
		result.message[p.nodes[key]] = surface
	}
	if p.codec == FormMessage {
		for i, field := range p.fields {
			if i == p.messageIndex {
				continue
			}
			if field.key == "aura.context" && looksJSONContainer(field.value) {
				q, err := readJSONPayload(field.value, p.budget)
				if err != nil {
					return nil, err
				}
				result.jsonFields[i] = q
				result.jsonLeaves[i] = map[*node]*templateSurface{}
				for _, leaf := range q.inspection.Leaves {
					if leaf.ValueType != "string" {
						continue
					}
					key, _ := routeKey(leaf.Route)
					surface, err := add("context", i, leaf.Route, leaf.Value, field.key)
					if err != nil {
						return nil, err
					}
					result.jsonLeaves[i][q.nodes[key]] = surface
				}
			} else {
				surface, err := add("form", i, nil, field.value, field.key)
				if err != nil {
					return nil, err
				}
				result.fields[i] = surface
			}
		}
	}
	return result, nil
}

func (p *preparedTemplates) bind(contract *TemplateContract, codec string) error {
	if contract == nil {
		return nil
	}
	if codec != FormMessage && len(contract.FormWireOccurrences) != 0 {
		return fail(InvalidTemplateContract)
	}
	byID := make(map[string]*templateSurface, len(p.surfaces))
	for _, surface := range p.surfaces {
		byID[surface.id] = surface
	}
	for policy, rows := range [][]TemplateOccurrence{contract.FormWireOccurrences, contract.LiteralOccurrences} {
		for _, row := range rows {
			surface := byID[row.ContextID]
			if surface == nil || row.OccurrenceIndex >= len(surface.tokens) || surface.tokens[row.OccurrenceIndex] != row.ExpressionSHA256 {
				return fail(InvalidTemplateContract)
			}
			if policy == 0 {
				if surface.context.formWireTokens == nil {
					surface.context.formWireTokens = map[int]bool{}
				}
				surface.context.formWireTokens[row.OccurrenceIndex] = true
			} else {
				if surface.context.literalTokens == nil {
					surface.context.literalTokens = map[int]bool{}
				}
				surface.context.literalTokens[row.OccurrenceIndex] = true
			}
		}
	}
	return nil
}

// VisitTemplatesContextualWithWorkAccounting validates all retained occurrences
// before visiting decoded scalar values. The shared codec budget admits each
// two-pass template scan before its callback, without rendering another body.
func VisitTemplatesContextualWithWorkAccounting(body, encoding string, rawContract any, visit func(string, TemplateContext) error, maxWork int) (int, error) {
	contract, err := ReadTemplateContract(rawContract)
	if err != nil {
		return 0, err
	}
	p, err := readPayloadLimit(body, encoding, maxWork)
	if err != nil {
		return errorWork(err), err
	}
	prepared, err := prepareTemplates(p)
	if err != nil {
		return p.budget.work, err
	}
	if err := prepared.bind(contract, p.codec); err != nil {
		return p.budget.work, err
	}
	if visit == nil {
		return p.budget.work, fail(InvalidRoute)
	}
	for _, surface := range prepared.surfaces {
		if err := p.budget.addWork(2 * len(surface.value)); err != nil {
			return p.budget.work, err
		}
		if err := visit(surface.value, surface.context); err != nil {
			return p.budget.work, err
		}
	}
	return p.budget.work, nil
}

// PromoteTemplateContractWithWorkAccounting snapshots plain-form occurrences
// before an edit. A previously structural body retains its existing contract;
// query_component and direct JSON occurrences already produce logical values.
func PromoteTemplateContractWithWorkAccounting(body, encoding, policy string, raw any, maxWork int) (*TemplateContract, int, error) {
	contract, err := ReadTemplateContract(raw)
	if err != nil {
		return nil, 0, err
	}
	p, err := readPayloadLimit(body, encoding, maxWork)
	if err != nil {
		return nil, errorWork(err), err
	}
	prepared, err := prepareTemplates(p)
	if err != nil {
		return nil, p.budget.work, err
	}
	if err := prepared.bind(contract, p.codec); err != nil {
		return nil, p.budget.work, err
	}
	if policy == TemplateValueEncodingJSONStrings {
		if contract == nil {
			contract = &TemplateContract{Version: 1, FormWireOccurrences: []TemplateOccurrence{}, LiteralOccurrences: []TemplateOccurrence{}}
		}
		return contract, p.budget.work, nil
	}
	if contract != nil {
		return nil, p.budget.work, fail(InvalidTemplateContract)
	}
	if p.codec == DirectJSON && policy == TemplateValueEncodingQueryComponent {
		for _, surface := range prepared.surfaces {
			if len(surface.tokens) > 0 {
				return nil, p.budget.work, fail(InvalidTemplateContract)
			}
		}
	}
	executable, err := originalExecutableOccurrences(p, prepared)
	if err != nil {
		return nil, p.budget.work, err
	}
	contract = &TemplateContract{Version: 1, FormWireOccurrences: []TemplateOccurrence{}, LiteralOccurrences: []TemplateOccurrence{}}
	for _, surface := range prepared.surfaces {
		for index, hash := range surface.tokens {
			row := TemplateOccurrence{ContextID: surface.id, OccurrenceIndex: index, ExpressionSHA256: hash}
			if !executable[surface][index] {
				contract.LiteralOccurrences = append(contract.LiteralOccurrences, row)
			} else if p.codec == FormMessage && policy != TemplateValueEncodingQueryComponent {
				contract.FormWireOccurrences = append(contract.FormWireOccurrences, row)
			}
		}
	}
	return contract, p.budget.work, nil
}

func errorWork(err error) int {
	if failure, ok := err.(*Error); ok {
		return failure.WorkBytes
	}
	return 0
}
