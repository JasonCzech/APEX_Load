// Package srcparse hardens the parsing of untrusted import source documents.
//
// It owns exactly one idea: reject a source document that can mean two
// different things to two different readers. YAML merge keys, non-scalar
// mapping keys, and keys that collide only under Unicode normalization plus
// case folding all let a converter and a reviewer disagree about what the
// document said — and a credential can hide in the half nobody looked at. The
// JSON side is the same problem: duplicate object keys, unbounded nesting, and
// trailing values are all places where "what the parser used" and "what the
// author wrote" diverge.
//
// The package deliberately has no apexload dependencies and no opinion about
// severity. Callers decide whether a finding blocks an import or becomes a
// review warning, because that answer differs per format: a Bruno collection
// with a merge key is a hard error, while real-world OpenAPI specs legitimately
// use anchors and merge.
package srcparse

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

// DefaultMaxDepth bounds nesting for both YAML and JSON source documents.
const DefaultMaxDepth = 128

// Limits bounds the structural work a single document may require.
type Limits struct {
	MaxDepth int
}

// DefaultLimits returns the limits used when a caller passes a zero value.
func DefaultLimits() Limits { return Limits{MaxDepth: DefaultMaxDepth} }

func (l Limits) normalized() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	return l
}

// FindingKind classifies a structural ambiguity.
type FindingKind string

const (
	// MergeKey is a YAML "<<" merge key. What the document contains depends on
	// which anchor the reader resolves and in what order.
	MergeKey FindingKind = "merge_key"
	// NonScalarKey is a mapping key that is itself a collection. Readers differ
	// on how (and whether) such a key is addressable.
	NonScalarKey FindingKind = "non_scalar_key"
	// DuplicateKey is a mapping key that repeats under NFC normalization plus
	// Unicode case folding. Readers silently pick one; which one varies.
	DuplicateKey FindingKind = "duplicate_key"
)

// Finding is one structural ambiguity, located by a dotted path.
type Finding struct {
	Kind     FindingKind
	Location string
	Detail   string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s at %s: %s", f.Kind, f.Location, f.Detail)
}

// YAMLPolicy selects which findings are errors for a given format.
type YAMLPolicy struct {
	RejectMergeKeys     bool
	RejectNonScalarKeys bool
	RejectDuplicateKeys bool
}

// StrictYAMLPolicy rejects every structural ambiguity. It suits formats whose
// documents are tool-generated and have no legitimate reason to use anchors.
func StrictYAMLPolicy() YAMLPolicy {
	return YAMLPolicy{
		RejectMergeKeys:     true,
		RejectNonScalarKeys: true,
		RejectDuplicateKeys: true,
	}
}

// SpecYAMLPolicy rejects the ambiguities that make a document mean two things
// while tolerating merge keys, which hand-authored API specs use legitimately.
// Callers should still surface tolerated merge keys as review findings.
func SpecYAMLPolicy() YAMLPolicy {
	return YAMLPolicy{
		RejectNonScalarKeys: true,
		RejectDuplicateKeys: true,
	}
}

// InspectYAMLMappingKeys walks a YAML document and reports every structural
// ambiguity it finds. It returns an error only when the document cannot be
// parsed at all or exceeds the depth limit.
func InspectYAMLMappingKeys(data []byte, limits Limits) ([]Finding, error) {
	limits = limits.normalized()
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	var findings []Finding
	if err := inspectYAMLNode(&document, "$", 0, limits, &findings); err != nil {
		return nil, err
	}
	return findings, nil
}

// ValidateYAMLMappingKeys returns the first finding selected by policy as an
// error, and reports every finding so a caller can surface the tolerated ones.
func ValidateYAMLMappingKeys(data []byte, policy YAMLPolicy, limits Limits) ([]Finding, error) {
	findings, err := InspectYAMLMappingKeys(data, limits)
	if err != nil {
		return nil, err
	}
	for _, finding := range findings {
		reject := false
		switch finding.Kind {
		case MergeKey:
			reject = policy.RejectMergeKeys
		case NonScalarKey:
			reject = policy.RejectNonScalarKeys
		case DuplicateKey:
			reject = policy.RejectDuplicateKeys
		}
		if reject {
			return findings, fmt.Errorf("%s", finding.String())
		}
	}
	return findings, nil
}

func inspectYAMLNode(node *yaml.Node, location string, depth int, limits Limits, findings *[]Finding) error {
	if node == nil {
		return nil
	}
	if depth > limits.MaxDepth {
		return fmt.Errorf("YAML nesting at %s exceeds %d levels", location, limits.MaxDepth)
	}
	switch node.Kind {
	case yaml.MappingNode:
		seen := make(map[string]string, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode := node.Content[index]
			if keyNode.Kind != yaml.ScalarNode {
				*findings = append(*findings, Finding{
					Kind:     NonScalarKey,
					Location: location,
					Detail:   "mapping contains a non-scalar key",
				})
				continue
			}
			key := strings.TrimSpace(keyNode.Value)
			if key == "<<" {
				*findings = append(*findings, Finding{
					Kind:     MergeKey,
					Location: location,
					Detail:   "mapping uses a YAML merge key; the resolved content depends on anchor order",
				})
			}
			folded := cases.Fold().String(norm.NFC.String(key))
			if prior, duplicate := seen[folded]; duplicate {
				*findings = append(*findings, Finding{
					Kind:     DuplicateKey,
					Location: location,
					Detail:   fmt.Sprintf("keys %q and %q collide under case folding", prior, keyNode.Value),
				})
			}
			seen[folded] = keyNode.Value
			if err := inspectYAMLNode(node.Content[index+1], location+"."+key, depth+1, limits, findings); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for index, child := range node.Content {
			if err := inspectYAMLNode(child, location+"["+strconv.Itoa(index)+"]", depth+1, limits, findings); err != nil {
				return err
			}
		}
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if err := inspectYAMLNode(child, location, depth, limits, findings); err != nil {
				return err
			}
		}
	}
	return nil
}

// DecodeJSON parses data with duplicate-key rejection, a depth cap,
// json.Number for every number so large integers and numeric spelling survive,
// and rejection of trailing or multiple top-level values.
func DecodeJSON(data []byte, limits Limits) (any, error) {
	if err := ValidateJSON(data, limits); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not supported")
		}
		return nil, err
	}
	return value, nil
}

// ValidateJSON reports whether data is unambiguous JSON without decoding it
// into a value.
func ValidateJSON(data []byte, limits Limits) error {
	limits = limits.normalized()
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := validateJSONValue(decoder, "$", 0, limits); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return err
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, location string, depth int, limits Limits) error {
	if depth > limits.MaxDepth {
		return fmt.Errorf("JSON nesting at %s exceeds %d levels", location, limits.MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object %s contains a non-string key", location)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("JSON object %s contains duplicate key %q", location, key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, location+"/"+key, depth+1, limits); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("JSON object %s has an invalid closing token", location)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := validateJSONValue(decoder, location+"/"+strconv.Itoa(index), depth+1, limits); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("JSON array %s has an invalid closing token", location)
		}
	}
	return nil
}
