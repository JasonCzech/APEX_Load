package bruno

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Readable secret names. While a request converts, each generated
// requirement takes a hashed name that cannot collide with another's; the
// generator also records a readable base (readableName), such as
// PARTNER_PASSWORD for {{partnerPassword}} or X_CDF_AUTH_TOKEN for that
// header. Once the whole collection has converted, every generated
// requirement takes its base. A later requirement that wants a name already
// taken is numbered (_2, _3) in collection order, so the same collection
// always gets the same names. The hashed name stays on the requirement as
// LegacyName: an import into a project that already configured the secret
// under that name can keep using it.

var nativeSecretReferencePattern = regexp.MustCompile(`\{\{secret\.([A-Za-z0-9_.\-]+)\}\}`)

// readableNativeSecretBase is a logical secret name built from raw alone:
// upper snake case, no source path, no hash. Empty when raw has no letters
// or digits.
func readableNativeSecretBase(raw string) string {
	var out strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToUpper(nativeCamelCaseBoundary.ReplaceAllString(strings.TrimSpace(raw), `${1}_${2}`)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	name := strings.Trim(out.String(), "_")
	name = strings.ReplaceAll(name, "USER_NAME", "USERNAME")
	if name == "" {
		return ""
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "SECRET_" + name
	}
	if len(name) > credential.MaxLogicalNameBytes-4 {
		// Leave room for a collision number.
		name = strings.TrimRight(name[:credential.MaxLogicalNameBytes-4], "_")
	}
	if !credential.ValidLogicalName(name) {
		return ""
	}
	return name
}

// assignReadableNativeSecretNames gives every generated requirement its
// readable name and rewrites the candidates to use it. Requirements the
// scripts use under a name they keep (the source's own {{secret.X}}, a name
// source scrubbing gave) hold theirs. A source-scrubbed definition no request
// uses, such as a disabled header, holds none.
func assignReadableNativeSecretNames(candidates []NativeScriptCandidate, aggregate map[string]SecretRequirement) error {
	used := map[string]bool{}
	for _, requirement := range aggregate {
		if requirement.ExistingReference || requirement.readableName == "" {
			used[strings.ToUpper(requirement.SuggestedName)] = true
		}
	}
	// Collection order: the first request that needs a secret names it.
	var order []string
	seen := map[string]bool{}
	for _, candidate := range candidates {
		ids := make([]string, 0, len(candidate.SecretRequirements))
		for _, requirement := range candidate.SecretRequirements {
			ids = append(ids, requirement.ID)
		}
		sort.Slice(ids, func(i, j int) bool {
			left, right := aggregate[ids[i]], aggregate[ids[j]]
			if left.readableName != right.readableName {
				return left.readableName < right.readableName
			}
			return ids[i] < ids[j]
		})
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
	}
	renames := map[string]string{}
	for _, id := range order {
		requirement, ok := aggregate[id]
		if !ok || requirement.ExistingReference || requirement.readableName == "" {
			continue
		}
		name := requirement.readableName
		for number := 2; used[name]; number++ {
			name = requirement.readableName + "_" + strconv.Itoa(number)
		}
		used[name] = true
		if name != requirement.SuggestedName {
			renames[requirement.SuggestedName] = name
		}
	}
	if err := renameNativeSecretsInCandidates(candidates, renames); err != nil {
		return err
	}
	for id, requirement := range aggregate {
		requirement = renamedNativeSecretRequirement(requirement, renames)
		if requirement.readableName != "" && !requirement.ExistingReference {
			requirement.NameBase = requirement.readableName
		}
		aggregate[id] = requirement
	}
	return nil
}

func renamedNativeSecretRequirement(requirement SecretRequirement, renames map[string]string) SecretRequirement {
	next, renamed := renames[requirement.SuggestedName]
	if !renamed {
		return requirement
	}
	if requirement.LegacyName == "" {
		requirement.LegacyName = requirement.SuggestedName
	}
	requirement.SuggestedName = next
	requirement.Replacement = "{{secret." + next + "}}"
	return requirement
}

// RenameNativeSecrets renames generated secret requirements, by their
// current name, throughout a native inspection: each candidate's script,
// requirements and credential targets, and the aggregate requirements.
func RenameNativeSecrets(result *Result, renames map[string]string) error {
	if result == nil || len(renames) == 0 {
		return nil
	}
	if err := renameNativeSecretsInCandidates(result.Scripts, renames); err != nil {
		return err
	}
	for index := range result.SecretRequirements {
		result.SecretRequirements[index] = renamedNativeSecretRequirement(result.SecretRequirements[index], renames)
	}
	return nil
}

// renameNativeSecretsInCandidates renames requirements in each candidate's
// script, requirement list and credential targets. A renamed field gets a new
// digest, so its targets still match the script exactly.
func renameNativeSecretsInCandidates(candidates []NativeScriptCandidate, renames map[string]string) error {
	if len(renames) == 0 {
		return nil
	}
	for index := range candidates {
		candidate := &candidates[index]
		for requirementIndex := range candidate.SecretRequirements {
			candidate.SecretRequirements[requirementIndex] = renamedNativeSecretRequirement(candidate.SecretRequirements[requirementIndex], renames)
		}
		if candidate.Script == nil {
			continue
		}
		renamed, digests, err := renameNativeScriptSecretReferences(candidate.Script, renames)
		if err != nil {
			return fmt.Errorf("rename secrets in Bruno request %q: %w", candidate.SourcePath, err)
		}
		candidate.Script = renamed
		for targetIndex := range candidate.CredentialTargets {
			if digest, changed := digests[candidate.CredentialTargets[targetIndex].JSONPointer]; changed {
				candidate.CredentialTargets[targetIndex].SanitizedSHA256 = digest
			}
		}
	}
	return nil
}

// renameNativeScriptSecretReferences rewrites {{secret.OLD}} references in a
// single pass, so one rename never feeds another, and returns the new digest
// of each string it changed, by JSON pointer.
func renameNativeScriptSecretReferences(script *dsl.Script, renames map[string]string) (*dsl.Script, map[string]string, error) {
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, nil, err
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, nil, err
	}
	digests := map[string]string{}
	var walk func(any, string) any
	walk = func(value any, pointer string) any {
		switch typed := value.(type) {
		case string:
			next := nativeSecretReferencePattern.ReplaceAllStringFunc(typed, func(reference string) string {
				name := nativeSecretReferencePattern.FindStringSubmatch(reference)[1]
				if renamed, ok := renames[name]; ok {
					return "{{secret." + renamed + "}}"
				}
				return reference
			})
			if next != typed {
				sum := sha256.Sum256([]byte(next))
				digests[pointer] = hex.EncodeToString(sum[:])
			}
			return next
		case []any:
			for index, child := range typed {
				typed[index] = walk(child, pointer+"/"+strconv.Itoa(index))
			}
			return typed
		case map[string]any:
			for key, child := range typed {
				typed[key] = walk(child, pointer+"/"+nativeJSONPointerEscape(key))
			}
			return typed
		default:
			return value
		}
	}
	document = walk(document, "")
	if len(digests) == 0 {
		return script, digests, nil
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	var renamed dsl.Script
	if err := json.Unmarshal(encoded, &renamed); err != nil {
		return nil, nil, err
	}
	if err := restoreNativeScriptDynamicValues(&renamed, script); err != nil {
		return nil, nil, err
	}
	return &renamed, digests, nil
}
