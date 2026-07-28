// Package credentialpolicy defines source-neutral credential projection and
// binding requirements for importers. It intentionally does not parse any
// source format and does not depend on the DSL or API layers.
package credentialpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// ProjectionMode controls what replaces a detected literal in the projected
// import draft.
type ProjectionMode string

const (
	// Preserve retains the imported value byte-for-byte in the executable
	// destination script. Import API projections may mask the value for display,
	// but persistence and replay keep the operator-supplied credential.
	Preserve ProjectionMode = "preserve"
	// Audit preserves the source value. It is intended only for compatibility
	// migrations whose existing outer disclosure boundary still performs the
	// projection.
	Audit ProjectionMode = "audit"
	// Redact replaces the source value with the repository disclosure sentinel.
	Redact ProjectionMode = "redact"
	// SecretReference replaces the value with a portable {{secret.NAME}}
	// reference.
	SecretReference ProjectionMode = "secret_reference"
)

// Valid reports whether mode is a supported explicit projection policy.
func (mode ProjectionMode) Valid() bool {
	switch mode {
	case Preserve, Audit, Redact, SecretReference:
		return true
	default:
		return false
	}
}

// SourceRef identifies the source construct that contributed a credential.
// It contains location/provenance only; source values never belong here.
type SourceRef struct {
	Format  string `json:"format,omitempty"`
	Path    string `json:"path,omitempty"`
	Pointer string `json:"pointer,omitempty"`
	ItemID  string `json:"item_id,omitempty"`
}

// SourceVariable is trusted, importer-internal provenance for a source-defined
// value that was translated to a {{var.*}} or {{param.*}} runtime reference.
// It is never serialized. Projectors consult it only when that reference
// occurs in a credential-bearing scalar; nonsensitive variable references
// remain ordinary runtime parameters.
type SourceVariable struct {
	Value  string
	Source SourceRef
}

// TargetRef identifies one projected field containing a logical binding. A
// requirement can have multiple targets when HeaderList and a compatibility
// map intentionally mirror the same wire value.
type TargetRef struct {
	RequestKey string `json:"request_key,omitempty"`
	Field      string `json:"field"`
	Index      int    `json:"index,omitempty"`
	Pointer    string `json:"pointer,omitempty"`
	Occurrence int    `json:"occurrence,omitempty"`
}

// Requirement is an importer-internal, disclosure-safe binding contract.
// Value is retained only for an authorized short-lived migration session and
// is never serialized.
type Requirement struct {
	ID                   string      `json:"id"`
	Key                  string      `json:"key,omitempty"`
	Label                string      `json:"label"`
	Context              string      `json:"context"`
	SuggestedName        string      `json:"suggested_name"`
	Replacement          string      `json:"replacement,omitempty"`
	Source               SourceRef   `json:"source,omitempty"`
	Targets              []TargetRef `json:"targets"`
	SourceValueAvailable bool        `json:"source_value_available,omitempty"`
	ExistingReference    bool        `json:"existing_reference,omitempty"`
	Value                string      `json:"-"`
	// DeferredSourceExpression retains only source template syntax for a
	// credential whose raw value was not available during projection. It is
	// used by protected-session reconciliation when an adapter attaches source
	// parameters after request conversion. It is never serialized and must
	// never be treated as a migratable secret value.
	DeferredSourceExpression string `json:"-"`
	// SourceVariableNames records source-defined variables consumed while
	// materializing this credential value. Import adapters use it to remove the
	// corresponding raw parameter/data-set values from public drafts.
	SourceVariableNames []string `json:"-"`
}

// RequirementSpec is the trusted input to NewRequirement.
type RequirementSpec struct {
	Key                 string
	Label               string
	Context             string
	NameHint            string
	Source              SourceRef
	Targets             []TargetRef
	Value               string
	ExistingReference   bool
	SourceVariableNames []string
	Mode                ProjectionMode
	Registry            *NameRegistry
}

// NewRequirement returns the safe requirement and the value that should be
// placed in the projected draft. IDs and suggested names never hash or embed
// the credential value.
func NewRequirement(spec RequirementSpec) (Requirement, string, error) {
	if !spec.Mode.Valid() {
		return Requirement{}, "", fmt.Errorf("unsupported credential projection mode %q", spec.Mode)
	}
	targets, err := normalizeTargets(spec.Targets)
	if err != nil {
		return Requirement{}, "", err
	}
	identity := stableIdentity(spec.Source, targets, spec.Key)
	nameHint := firstNonEmpty(spec.NameHint, spec.Label, spec.Key, "IMPORTED_SECRET")
	var suggested string
	var existingReference string
	if spec.ExistingReference {
		if parsed, ok := credential.ParseReference(spec.Value); ok {
			suggested = parsed
			if spec.Registry != nil {
				spec.Registry.Reserve(suggested)
			}
			existingReference, err = credential.Reference(parsed)
			if err != nil {
				return Requirement{}, "", err
			}
		} else {
			return Requirement{}, "", errors.New("existing credential reference must be a whole {{secret.NAME}} template")
		}
	}
	if suggested == "" {
		if spec.Registry != nil {
			suggested = spec.Registry.Allocate(nameHint, identity)
		} else {
			suggested = canonicalSuggestion(nameHint)
		}
	}
	if !credential.ValidLogicalName(suggested) {
		return Requirement{}, "", fmt.Errorf("invalid suggested credential name %q", suggested)
	}

	replacement := ""
	projected := spec.Value
	if spec.ExistingReference {
		replacement = existingReference
		projected = existingReference
	} else {
		switch spec.Mode {
		case Preserve, Audit:
			// The requirement is still returned, but no source value is repeated
			// in its serializable fields.
		case Redact:
			replacement = credential.Redacted
			projected = replacement
		case SecretReference:
			replacement, err = credential.Reference(suggested)
			if err != nil {
				return Requirement{}, "", err
			}
			projected = replacement
		}
	}

	requirement := Requirement{
		ID:                   StableID(spec.Source, targets, spec.Key),
		Key:                  strings.TrimSpace(spec.Key),
		Label:                firstNonEmpty(spec.Label, spec.Key, "Imported credential"),
		Context:              strings.TrimSpace(spec.Context),
		SuggestedName:        suggested,
		Replacement:          replacement,
		Source:               normalizeSource(spec.Source),
		Targets:              targets,
		SourceValueAvailable: !spec.ExistingReference && spec.Value != "",
		ExistingReference:    spec.ExistingReference,
		Value:                spec.Value,
		SourceVariableNames:  normalizeSourceVariableNames(spec.SourceVariableNames),
	}
	if requirement.ExistingReference {
		requirement.Value = ""
		requirement.SourceValueAvailable = false
	}
	return requirement, projected, nil
}

func normalizeSourceVariableNames(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, name := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// StableID derives an opaque deterministic requirement ID from provenance and
// target identity only. Rotating a source credential therefore does not change
// the requirement ID, and the digest cannot serve as an oracle for its value.
func StableID(source SourceRef, targets []TargetRef, key string) string {
	normalized, _ := normalizeTargets(targets)
	identity := stableIdentity(source, normalized, key)
	sum := sha256.Sum256([]byte("apexload-import-credential-v1\x00" + identity))
	return hex.EncodeToString(sum[:12])
}

// CloneRequirements returns a detached copy, including detached target slices.
func CloneRequirements(input []Requirement) []Requirement {
	if len(input) == 0 {
		return nil
	}
	out := make([]Requirement, len(input))
	for index := range input {
		out[index] = input[index]
		out[index].Targets = append([]TargetRef(nil), input[index].Targets...)
		out[index].SourceVariableNames = append([]string(nil), input[index].SourceVariableNames...)
	}
	return out
}

// ProtectedRequirements is a one-shot, in-memory handoff for requirements
// whose Value fields must cross an adapter boundary without becoming part of
// the adapter's JSON result. Store and Take detach their inputs, and Take
// clears the retained values before releasing the internal slice.
//
// The zero value is ready to use. ProtectedRequirements must not be copied
// after Store has been called.
type ProtectedRequirements struct {
	requirements []Requirement
}

// Store replaces the protected handoff contents with a detached copy.
func (protected *ProtectedRequirements) Store(requirements []Requirement) {
	if protected == nil {
		return
	}
	protected.clear()
	protected.requirements = CloneRequirements(requirements)
}

// Take transfers a detached copy to the caller and clears the handoff.
// Calling Take more than once returns nil.
func (protected *ProtectedRequirements) Take() []Requirement {
	if protected == nil || len(protected.requirements) == 0 {
		return nil
	}
	requirements := CloneRequirements(protected.requirements)
	protected.clear()
	return requirements
}

func (protected *ProtectedRequirements) clear() {
	if protected == nil {
		return
	}
	for index := range protected.requirements {
		protected.requirements[index].Value = ""
		protected.requirements[index].DeferredSourceExpression = ""
	}
	protected.requirements = nil
}

// NameRegistry allocates deterministic logical names without colliding with
// names already present in the same import result.
type NameRegistry struct {
	used        map[string]struct{}
	allocations map[string]string
}

// NewNameRegistry reserves any existing valid logical names.
func NewNameRegistry(reserved ...string) *NameRegistry {
	registry := &NameRegistry{
		used:        make(map[string]struct{}, len(reserved)),
		allocations: make(map[string]string),
	}
	for _, name := range reserved {
		registry.Reserve(name)
	}
	return registry
}

// Reserve adds a valid logical name to the registry.
func (registry *NameRegistry) Reserve(raw string) bool {
	if registry == nil {
		return false
	}
	name, err := credential.NormalizeLogicalName(raw)
	if err != nil {
		return false
	}
	if registry.used == nil {
		registry.used = make(map[string]struct{})
	}
	if _, exists := registry.used[name]; exists {
		return false
	}
	registry.used[name] = struct{}{}
	return true
}

// Allocate returns a valid name based on hint. A provenance-derived suffix is
// used on collision; the credential value is never part of that suffix.
func (registry *NameRegistry) Allocate(hint, identity string) string {
	base := canonicalSuggestion(hint)
	if registry == nil {
		return base
	}
	if registry.used == nil {
		registry.used = make(map[string]struct{})
	}
	if registry.allocations == nil {
		registry.allocations = make(map[string]string)
	}
	allocationKey := base + "\x00" + identity
	if allocated, exists := registry.allocations[allocationKey]; exists {
		return allocated
	}
	if _, exists := registry.used[base]; !exists {
		registry.used[base] = struct{}{}
		registry.allocations[allocationKey] = base
		return base
	}
	sum := sha256.Sum256([]byte("apexload-import-secret-name-v1\x00" + identity))
	suffix := "_" + strings.ToUpper(hex.EncodeToString(sum[:6]))
	candidateBase := truncateName(base, credential.MaxLogicalNameBytes-len(suffix))
	candidate := candidateBase + suffix
	if _, exists := registry.used[candidate]; !exists {
		registry.used[candidate] = struct{}{}
		registry.allocations[allocationKey] = candidate
		return candidate
	}
	for counter := 2; ; counter++ {
		counterSuffix := suffix + "_" + strconv.Itoa(counter)
		candidate = truncateName(base, credential.MaxLogicalNameBytes-len(counterSuffix)) + counterSuffix
		if _, exists := registry.used[candidate]; exists {
			continue
		}
		registry.used[candidate] = struct{}{}
		registry.allocations[allocationKey] = candidate
		return candidate
	}
}

func canonicalSuggestion(hint string) string {
	name := credential.EnvKey(hint)
	if len(name) > credential.MaxLogicalNameBytes {
		name = name[:credential.MaxLogicalNameBytes]
	}
	name = strings.Trim(name, "_")
	if name == "" {
		return "IMPORTED_SECRET"
	}
	return name
}

func truncateName(value string, limit int) string {
	if limit <= 0 {
		return "S"
	}
	if len(value) > limit {
		value = value[:limit]
	}
	value = strings.Trim(value, "_")
	if value == "" {
		return "S"
	}
	return value
}

func normalizeSource(source SourceRef) SourceRef {
	source.Format = strings.TrimSpace(source.Format)
	source.Path = strings.TrimSpace(source.Path)
	source.Pointer = strings.TrimSpace(source.Pointer)
	source.ItemID = strings.TrimSpace(source.ItemID)
	return source
}

func normalizeTargets(targets []TargetRef) ([]TargetRef, error) {
	if len(targets) == 0 {
		return nil, errors.New("credential requirement must have at least one target")
	}
	out := make([]TargetRef, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		target.RequestKey = strings.TrimSpace(target.RequestKey)
		target.Field = strings.TrimSpace(target.Field)
		target.Pointer = strings.TrimSpace(target.Pointer)
		if target.Field == "" {
			return nil, errors.New("credential target field is required")
		}
		if target.Index < 0 || target.Occurrence < 0 {
			return nil, errors.New("credential target index and occurrence must be non-negative")
		}
		identity := targetIdentity(target)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return targetIdentity(out[i]) < targetIdentity(out[j])
	})
	return out, nil
}

func stableIdentity(source SourceRef, targets []TargetRef, key string) string {
	source = normalizeSource(source)
	parts := []string{source.Format, source.Path, source.Pointer, source.ItemID, strings.TrimSpace(key)}
	for _, target := range targets {
		parts = append(parts, targetIdentity(target))
	}
	return strings.Join(parts, "\x00")
}

func targetIdentity(target TargetRef) string {
	return strings.Join([]string{
		target.RequestKey,
		target.Field,
		strconv.Itoa(target.Index),
		target.Pointer,
		strconv.Itoa(target.Occurrence),
	}, "\x1f")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
