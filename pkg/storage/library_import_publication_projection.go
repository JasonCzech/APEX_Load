package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

var ErrLibraryImportPublicationUnavailable = errors.New("library import publication evidence unavailable")

// LibraryImportClaimFacts is a bounded publication projection. Recovery owner,
// lease, identity and before-snapshot validation remain the API's responsibility.
// These facts are never accepted as a user-supplied publication assertion.
type LibraryImportClaimFacts struct {
	Committed   bool
	Prepared    bool
	LocalItemID string
	RemoteID    string
	Kind        string
}

func NormalizeLibraryImportKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "script", "scripts":
		return "script"
	case "scenario", "scenarios":
		return "scenario"
	case "result", "results":
		return "result"
	default:
		return ""
	}
}

func LibraryImportAssetKey(kind, id string) string {
	return NormalizeLibraryImportKind(kind) + "\x00" + strings.TrimSpace(id)
}

func (f LibraryImportClaimFacts) PendingDirectImport() bool {
	return !f.Committed && f.Prepared && strings.TrimSpace(f.LocalItemID) != "" && strings.HasPrefix(strings.TrimSpace(f.RemoteID), "direct-import:")
}

// Match encoding/json's Unicode simple-fold equivalence rather than accepting
// last-value-wins aliases, including escaped or non-ASCII spellings of names.
func foldPublicationMember(name string) string {
	var folded strings.Builder
	for _, r := range name {
		smallest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < smallest {
				smallest = next
			}
		}
		folded.WriteRune(smallest)
	}
	return folded.String()
}

// ParseLibraryImportPublication never normalizes the authoritative raw bytes.
// Every entry is an object with unambiguous top-level keys and typed facts.
// Unknown payload fields are retained by the rich API decoder, not projected.
func ParseLibraryImportPublication(raw string) ([]LibraryImportClaimFacts, error) {
	if err := validateLibraryImportPublicationRaw(raw); err != nil {
		return nil, ErrLibraryImportPublicationUnavailable
	}
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, ErrLibraryImportPublicationUnavailable
	}
	facts := make([]LibraryImportClaimFacts, 0)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, ErrLibraryImportPublicationUnavailable
		}
		var f LibraryImportClaimFacts
		seen := make(map[string]bool)
		for decoder.More() {
			token, err = decoder.Token()
			name, ok := token.(string)
			if err != nil || !ok {
				return nil, ErrLibraryImportPublicationUnavailable
			}
			folded := foldPublicationMember(name)
			if seen[folded] {
				return nil, ErrLibraryImportPublicationUnavailable
			}
			seen[folded] = true
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return nil, ErrLibraryImportPublicationUnavailable
			}
			var target any
			switch folded {
			case "COMMITTED":
				target = &f.Committed
			case "PREPARED":
				target = &f.Prepared
			case "LOCAL_ITEM_ID":
				target = &f.LocalItemID
			case "REMOTE_ID":
				target = &f.RemoteID
			case "KIND":
				target = &f.Kind
			default:
				continue
			}
			if strings.TrimSpace(string(value)) == "null" || json.Unmarshal(value, target) != nil {
				return nil, ErrLibraryImportPublicationUnavailable
			}
		}
		if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
			return nil, ErrLibraryImportPublicationUnavailable
		}
		f.Kind = NormalizeLibraryImportKind(f.Kind)
		if f.Kind == "" || (f.Prepared && strings.TrimSpace(f.LocalItemID) == "") {
			return nil, ErrLibraryImportPublicationUnavailable
		}
		facts = append(facts, f)
	}
	return facts, nil
}
