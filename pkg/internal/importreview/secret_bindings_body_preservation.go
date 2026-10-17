package importreview

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"io"
	"mime"
	"strings"
)

func StructuredImportBodyCanRemainExact(root any, path, sourceValue string) bool {
	node, ok := root.(map[string]any)
	if !ok || !CredentialPreservingImportJSONNode(node) {
		return false
	}
	tokens, err := SplitJSONPointer(path)
	if err != nil || len(tokens) < 2 || tokens[len(tokens)-1] != "body" {
		return false
	}
	current := root
	for _, token := range tokens[:len(tokens)-1] {
		switch value := current.(type) {
		case map[string]any:
			current, ok = value[token]
		case []any:
			index, valid := DecimalTokenIndex(token, len(value))
			if !valid {
				return false
			}
			current, ok = value[index], true
		default:
			return false
		}
		if !ok {
			return false
		}
	}
	owner, ok := current.(map[string]any)
	if !ok {
		return false
	}
	stored, ok := owner["body"].(string)
	if !ok {
		return false
	}
	ref := dslwalk.SurfaceRef{Request: owner}
	body, exists, err := DecodeImportCommitProjectionBody(ref)
	if err != nil || !exists {
		return false
	}
	contentType := ImportCommitSurfaceContentType(ref)
	limits := credentialpolicy.DefaultLimits()
	proof := &StructuredImportPayloadProof{remainingBytes: limits.MaxBytes, remainingValues: limits.MaxValues}
	if stored == sourceValue {
		return ExactStructuredImportPayload(body.wire, contentType, 0, proof)
	}
	// Disclosure may mask one complete JSON document carried in an encoded
	// form field. Prove the parsed component instead of applying a credential-
	// scalar decoding budget to the complete serialized document.
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		return false
	}
	fields, err := credentialpolicy.ParseFormURLEncoded(body.Text(), credentialpolicy.Options{Mode: credentialpolicy.Preserve})
	if err != nil {
		return false
	}
	for _, field := range fields {
		if field.RawValue == sourceValue || field.Value == sourceValue {
			return ExactStructuredImportPayload([]byte(field.Value), "", 0, proof)
		}
	}
	return false
}

type StructuredImportPayloadProof struct {
	remainingBytes  int
	remainingValues int
}

func ExactStructuredImportPayload(wire []byte, contentType string, depth int, proof *StructuredImportPayloadProof) bool {
	limits := credentialpolicy.DefaultLimits()
	if depth > limits.MaxDepth || len(wire) > proof.remainingBytes {
		return false
	}
	proof.remainingBytes -= len(wire)
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		// The form codec establishes the transport boundary. Inspect decoded
		// field leaves, while retaining RawName/RawValue in the destination.
		// Treating a percent-encoded JSON document as one credential scalar
		// can exhaust the scalar candidate guard even when every decoded leaf
		// is available. No form bytes are rewritten by this proof.
		fields, err := credentialpolicy.ParseFormURLEncoded(string(wire), credentialpolicy.Options{Mode: credentialpolicy.Preserve})
		if err != nil {
			return false
		}
		for _, field := range fields {
			if proof.remainingValues < 2 {
				return false
			}
			proof.remainingValues -= 2
			if !ExactStructuredImportScalar(field.Name, depth+1, proof) ||
				!ExactStructuredImportScalar(field.Value, depth+1, proof) {
				return false
			}
		}
		return len(fields) > 0
	}
	projected, result, err := credentialpolicy.ProjectRequestBody(
		wire,
		contentType,
		credentialpolicy.Options{
			Mode:        credentialpolicy.Preserve,
			Source:      credentialpolicy.SourceRef{Format: "import_destination"},
			RequestKey:  "body",
			TargetField: "body",
		},
	)
	if err != nil || result.Disposition != credentialpolicy.ExecutableExact ||
		len(result.Requirements) != 0 || !bytes.Equal(projected, wire) {
		return false
	}
	trimmed := bytes.TrimSpace(wire)
	if len(trimmed) == 0 {
		return false
	}
	// This recovery proof supports JSON and encoded forms only. Do not treat
	// an opaque string or a codec's incomplete-preservation fallback as a
	// structured document whose credential leaves have been inspected.
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return ExactStructuredImportJSONLeaves(trimmed, depth, proof)
	}
	return false
}

func ExactStructuredImportJSONLeaves(wire []byte, depth int, proof *StructuredImportPayloadProof) bool {
	// Tokens retain duplicate object members. Decoding into a map would erase
	// an earlier unavailable leaf when a later duplicate key has a safe value.
	decoder := json.NewDecoder(bytes.NewReader(wire))
	nesting := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nesting == 0
		}
		if err != nil || proof.remainingValues <= 0 {
			return false
		}
		proof.remainingValues--
		switch token := token.(type) {
		case json.Delim:
			switch token {
			case '{', '[':
				nesting++
			case '}', ']':
				nesting--
			}
			if nesting < 0 || nesting+depth > credentialpolicy.DefaultLimits().MaxDepth {
				return false
			}
		case string:
			if !ExactStructuredImportScalar(token, depth+nesting, proof) {
				return false
			}
		}
	}
}

func ExactStructuredImportScalar(value string, depth int, proof *StructuredImportPayloadProof) bool {
	switch credentialpolicy.ClassifySourceCredentialValue(value) {
	case credentialpolicy.SourceCredentialValueAvailable, credentialpolicy.SourceCredentialValueMissing:
		return true
	case credentialpolicy.SourceCredentialValueUnsafeEncoding:
		return ExactStructuredImportPayload([]byte(value), "", depth+1, proof)
	default:
		return false
	}
}

func CredentialPreservingImportJSONNode(node map[string]any) bool {
	metadata, _ := node["metadata"].(map[string]any)
	imported, _ := metadata["import"].(map[string]any)
	source, _ := imported["source"].(string)
	preserved, _ := imported["preserved"].(map[string]any)
	policy, _ := preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.TrimSpace(source) != "" && strings.EqualFold(
		strings.TrimSpace(policy), dsl.ImportPreservedCredentialPolicyPreserve,
	)
}

func ReconcileWholeBodySecretBindingLocators(script *dsl.Script) error {
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		body, ok := ImportCommitSurfaceBody(ref)
		if !ok {
			return nil
		}
		if _, governed := credential.ParseReference(body); !governed {
			return nil
		}
		metadata := ref.Metadata()
		raw, exists := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]
		if !exists {
			return nil
		}
		payload, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		var targets []credentialpolicy.TargetRef
		if err := json.Unmarshal(payload, &targets); err != nil {
			return err
		}
		result := make([]credentialpolicy.TargetRef, 0, len(targets))
		seenBodies := make(map[string]bool)
		for _, target := range targets {
			field, owned := ReboundBodyTargetField(ref, target)
			if !owned {
				result = append(result, target)
				continue
			}
			identity := field + "\x00" + target.RequestKey
			if !seenBodies[identity] {
				result = append(result, credentialpolicy.TargetRef{RequestKey: target.RequestKey, Field: field})
				seenBodies[identity] = true
			}
		}
		metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = result
		ref.SetMetadata(metadata)
		return nil
	})
}

func ReboundBodyTargetField(ref dslwalk.SurfaceRef, target credentialpolicy.TargetRef) (string, bool) {
	// Fully qualified fields prove which request surface owns the locator even
	// when its source request key is an importer-specific identity. Historical
	// unqualified body fields are local only when their request key is absent or
	// identifies this exact surface. A header named "body", a sibling request,
	// or an unrelated dot segment cannot establish that ownership.
	prefixes := []string{ref.Path + ".body"}
	if target.RequestKey == "" || target.RequestKey == ref.Path ||
		target.RequestKey == DslWalkPathJSONPointer(ref.Path) {
		prefixes = append(prefixes, "body")
	}
	for _, prefix := range prefixes {
		if target.Field == prefix || strings.HasPrefix(target.Field, prefix+".") {
			return prefix + ".opaque", true
		}
	}
	return "", false
}
