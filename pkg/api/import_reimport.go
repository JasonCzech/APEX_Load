package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Re-import as a new version, for every importer but Bruno (which has its
// own candidate matching): an import records where each script came from
// (dsl.SetImportSourceIdentity), the import review lists the earlier script
// each new one would update, and a save can update that script in place as a
// new version instead of creating a duplicate.

// importExistingScript is an earlier imported script a new one matches.
type importExistingScript struct {
	ScriptID  string    `json:"script_id"`
	Name      string    `json:"name"`
	RefKey    string    `json:"ref_key,omitempty"`
	FolderID  string    `json:"folder_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Matches counts the earlier scripts from the same source item. Each is
	// offered to one new script, the most recently updated first.
	Matches int `json:"matches"`
	// SameSource is true when the source declares its identity (a collection
	// ID, a specification title, a test plan or LoadRunner project name). A
	// match on a file name alone may be an unrelated upload, so the review
	// defaults it to a copy.
	SameSource bool `json:"same_source"`
	// EditedSinceImport is true when the script changed in APEX after the
	// import saved it (or when that cannot be told): updating replaces those
	// changes.
	EditedSinceImport bool `json:"edited_since_import"`
	// ContentDigest fingerprints the script's content as reviewed; a save
	// sends it back as expected_content_digest.
	ContentDigest string `json:"content_digest,omitempty"`
	// Inputs is how the script supplied each input its import recorded, by
	// input name, so a re-import can offer the same choice again.
	Inputs map[string]importPreviousInput `json:"inputs,omitempty"`
}

// importedScriptIdentityLister is the storage re-import matching reads.
type importedScriptIdentityLister interface {
	ListImportedScriptIdentities(projectID, source string) ([]storage.ImportedScriptIdentity, error)
}

// stampFileImportSourceIdentity records a file name as the source of each
// script whose importer recorded no identity of its own. A file name is not a
// declared identity. Scripts converted from one file together are told apart
// by name.
func stampFileImportSourceIdentity(fileName string, scripts []*dsl.Script) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return
	}
	for _, script := range scripts {
		if script == nil {
			continue
		}
		var metadata *dsl.ImportMetadata
		if script.Metadata != nil {
			metadata = script.Metadata.Import
		}
		if _, _, _, ok := dsl.ImportSourceIdentityOf(metadata); ok {
			continue
		}
		item := ""
		if len(scripts) > 1 {
			item = script.Name
		}
		dsl.SetImportSourceIdentity(script, fileName, false, item)
	}
}

// importExistingScripts returns, for each script, the earlier imported script
// in projectID from the same source document and item, or nil. Matching is a
// convenience: when it cannot run, the review simply offers no update.
func (s *Server) importExistingScripts(projectID string, scripts []*dsl.Script) []*importExistingScript {
	matches := make([]*importExistingScript, len(scripts))
	if s == nil || len(scripts) == 0 {
		return matches
	}
	lister, ok := s.store.(importedScriptIdentityLister)
	if !ok {
		return matches
	}
	type key struct{ source, document, item string }
	type candidate struct {
		identity storage.ImportedScriptIdentity
		declared bool
	}
	bySource := make(map[string]map[key][]candidate)
	// An earlier script is offered to one new script only: two new scripts
	// with the same identity take the newest and the next newest.
	used := make(map[string]bool)
	found := false
	for index, script := range scripts {
		source, document, item, _, ok := importScriptSourceIdentity(script)
		if !ok {
			continue
		}
		identities, loaded := bySource[source]
		if !loaded {
			list, err := lister.ListImportedScriptIdentities(projectID, source)
			if err != nil {
				slog.Warn("match previously imported scripts", "project_id", projectID, "source", source, "error", err)
				list = nil
			}
			identities = make(map[key][]candidate)
			for _, existing := range list {
				if existing.ArchivedAt != nil {
					continue
				}
				existingDocument, existingItem, existingDeclared, has := dsl.ImportSourceIdentityOf(existing.Import)
				if !has {
					continue
				}
				k := key{source, strings.ToLower(existingDocument), strings.ToLower(existingItem)}
				identities[k] = append(identities[k], candidate{identity: existing, declared: existingDeclared})
			}
			bySource[source] = identities
		}
		candidates := identities[key{source, strings.ToLower(document), strings.ToLower(item)}]
		if len(candidates) == 0 {
			continue
		}
		bestIndex := -1
		for candidateIndex, next := range candidates {
			if used[next.identity.ID] {
				continue
			}
			if bestIndex < 0 || next.identity.UpdatedAt.After(candidates[bestIndex].identity.UpdatedAt) {
				bestIndex = candidateIndex
			}
		}
		if bestIndex < 0 {
			continue
		}
		best := candidates[bestIndex]
		used[best.identity.ID] = true
		_, _, _, declared, _ := importScriptSourceIdentity(script)
		folderID := ""
		if best.identity.FolderID != nil {
			folderID = strings.TrimSpace(*best.identity.FolderID)
		}
		matches[index] = &importExistingScript{
			ScriptID:          best.identity.ID,
			Name:              best.identity.Name,
			RefKey:            best.identity.RefKey,
			FolderID:          folderID,
			UpdatedAt:         best.identity.UpdatedAt.UTC(),
			Matches:           len(candidates),
			SameSource:        declared && best.declared,
			EditedSinceImport: true,
		}
		found = true
	}
	if !found {
		return matches
	}
	for _, match := range matches {
		if match == nil {
			continue
		}
		existing, err := s.store.GetScript(match.ScriptID)
		if err != nil || existing == nil || existing.Metadata == nil || existing.Metadata.Import == nil {
			continue
		}
		recorded, _ := existing.Metadata.Import.Preserved[dsl.ImportedActionDigestKey].(string)
		current, digestErr := importedScriptContentDigest(existing)
		match.EditedSinceImport = digestErr != nil || recorded == "" || recorded != current
		if digestErr == nil {
			match.ContentDigest = current
		}
		for name := range existing.Metadata.Import.Inputs {
			// A credential-shaped input is never a value for every user, so a
			// value under its name was put there by hand and stays out.
			if sharedvalue.CredentialShaped(name) {
				continue
			}
			if input, ok := previousImportInputFrom(existing, name); ok {
				if match.Inputs == nil {
					match.Inputs = make(map[string]importPreviousInput)
				}
				match.Inputs[name] = input
			}
		}
	}
	return matches
}

// importScriptSourceIdentity returns a script's import source profile and the
// identity its importer recorded.
func importScriptSourceIdentity(script *dsl.Script) (source, document, item string, declared, ok bool) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return "", "", "", false, false
	}
	source = strings.TrimSpace(script.Metadata.Import.Source)
	if source == "" || strings.EqualFold(source, "bruno") {
		return "", "", "", false, false
	}
	document, item, declared, ok = dsl.ImportSourceIdentityOf(script.Metadata.Import)
	return source, document, item, declared, ok
}

// importedScriptContentDigest fingerprints what an import writes and an
// operator edits: protocol, runtime settings, data, parameters and steps. Its
// name, folder and metadata are left out.
//
// The fields are read back through JSON, as storage reads them, so a script
// saved and loaded again digests the same (an empty list and none match).
func importedScriptContentDigest(script *dsl.Script) (string, error) {
	if script == nil {
		return "", errors.New("script is required")
	}
	content, err := json.Marshal(struct {
		Protocol    string           `json:"protocol"`
		Config      dsl.ScriptConfig `json:"config"`
		DataSources []dsl.DataSource `json:"data_sources,omitempty"`
		Parameters  []dsl.Parameter  `json:"parameters,omitempty"`
		Init        []dsl.Action     `json:"init,omitempty"`
		Actions     []dsl.Action     `json:"actions,omitempty"`
		Teardown    []dsl.Action     `json:"teardown,omitempty"`
	}{script.Protocol, script.Config, script.DataSources, script.Parameters, script.Init, script.Actions, script.Teardown})
	if err != nil {
		return "", err
	}
	var canonical any
	if err := json.Unmarshal(content, &canonical); err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// stampImportedContentDigest records the content an import is about to save,
// so a later import can tell whether the script was edited since.
func stampImportedContentDigest(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	digest, err := importedScriptContentDigest(script)
	if err != nil {
		return
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	script.Metadata.Import.Preserved[dsl.ImportedActionDigestKey] = digest
}

// importUpdateTarget names the earlier imported script a save updates as a
// new version, and the updated_at the operator reviewed.
type importUpdateTarget struct {
	ScriptID          string    `json:"script_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	// ExpectedContentDigest is the content_digest the review showed. A script
	// back at that content (a failed save put it back as a further version)
	// is still the version reviewed.
	ExpectedContentDigest string `json:"expected_content_digest,omitempty"`
}

// importUpdateTargetStaleCode is the error code of a save whose script to
// update changed, moved or was archived after review.
const importUpdateTargetStaleCode = "import_update_target_stale"

var errImportUpdateTargetStale = errors.New("the script to update changed after review")

// staleImportUpdate is a stale update target, saying why in words an
// operator can act on.
type staleImportUpdate string

func (reason staleImportUpdate) Error() string {
	return string(reason) + "; inspect the source again"
}

func (staleImportUpdate) Is(target error) bool { return target == errImportUpdateTargetStale }

// normalizeImportUpdateTargets checks a save's update targets: none, or one
// entry (null to create) per script, each naming a different script.
func normalizeImportUpdateTargets(targets []*importUpdateTarget, scripts int) ([]*importUpdateTarget, error) {
	normalized := make([]*importUpdateTarget, scripts)
	if len(targets) == 0 {
		return normalized, nil
	}
	if len(targets) != scripts {
		return nil, errors.New("update_targets must be omitted or contain one entry (null to create) per script")
	}
	seen := make(map[string]struct{}, len(targets))
	for index, target := range targets {
		if target == nil {
			continue
		}
		scriptID := strings.TrimSpace(target.ScriptID)
		if scriptID == "" || target.ExpectedUpdatedAt.IsZero() {
			return nil, fmt.Errorf("update target %d needs a script_id and the expected_updated_at the review showed", index+1)
		}
		if _, duplicate := seen[scriptID]; duplicate {
			return nil, fmt.Errorf("update target %d names script %q, which another script already updates", index+1, scriptID)
		}
		seen[scriptID] = struct{}{}
		normalized[index] = &importUpdateTarget{
			ScriptID:              scriptID,
			ExpectedUpdatedAt:     target.ExpectedUpdatedAt.UTC(),
			ExpectedContentDigest: strings.TrimSpace(target.ExpectedContentDigest),
		}
	}
	return normalized, nil
}

// importUpdateTargetsDigestValue is the update targets as the request digest
// records them, so a retry that updates different scripts is a new request.
func importUpdateTargetsDigestValue(targets []*importUpdateTarget) []any {
	values := make([]any, len(targets))
	for index, target := range targets {
		if target != nil {
			values[index] = map[string]string{
				"script_id":               target.ScriptID,
				"expected_updated_at":     target.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano),
				"expected_content_digest": target.ExpectedContentDigest,
			}
		}
	}
	return values
}

// importUpdateTargetScript loads the script a re-import updates and checks
// it is still the active script an earlier import of the same source made in
// projectID. A failed check wraps errImportUpdateTargetStale and says why.
// Whether it is still the version reviewed is checked once a retry has had
// the chance to adopt what an earlier attempt saved
// (requireReviewedImportUpdates).
func (s *Server) importUpdateTargetScript(projectID string, draft *dsl.Script, target importUpdateTarget) (*StoredScript, error) {
	stale := func(reason string) error { return staleImportUpdate(reason) }
	existing, ok := s.runner.GetScript(target.ScriptID)
	if !ok || existing == nil || existing.Script == nil || existing.ID != target.ScriptID ||
		normalizeProjectID(existing.ProjectID) != normalizeProjectID(projectID) {
		return nil, stale("the script to update no longer exists")
	}
	if existing.Script.ArchivedAt != nil {
		return nil, stale("the script to update is archived")
	}
	source, document, item, _, ok := importScriptSourceIdentity(draft)
	existingSource, existingDocument, existingItem, _, existingOK := importScriptSourceIdentity(existing.Script)
	if !ok || !existingOK || !sameImportSource(source, existingSource) ||
		!strings.EqualFold(document, existingDocument) || !strings.EqualFold(item, existingItem) {
		return nil, stale("the script to update was not imported from this source")
	}
	return existing, nil
}

// importUpdateTargetReviewed reports whether script is still the version the
// review showed: the same update time, or the same content.
func importUpdateTargetReviewed(script *dsl.Script, target importUpdateTarget) bool {
	if script == nil {
		return false
	}
	if sameImportUpdateTime(script.UpdatedAt, target.ExpectedUpdatedAt) {
		return true
	}
	if target.ExpectedContentDigest == "" {
		return false
	}
	current, err := importedScriptContentDigest(script)
	return err == nil && current == target.ExpectedContentDigest
}

// requireReviewedImportUpdates writes a 409 and returns false when a script a
// save updates is no longer the version the operator reviewed. A save calls
// it after recovery, so a retry first adopts an update its earlier attempt
// applied.
func requireReviewedImportUpdates(w http.ResponseWriter, targets []*importUpdateTarget, updates map[string]*StoredScript) bool {
	for index, target := range targets {
		if target == nil {
			continue
		}
		existing := updates[target.ScriptID]
		if existing == nil || !importUpdateTargetReviewed(existing.Script, *target) {
			writeImportUpdateTargetStale(w, index, staleImportUpdate("the script to update changed after review"))
			return false
		}
	}
	return true
}

// sameImportUpdateTime compares a stored script's update time with the one a
// review read, to the microsecond: Postgres keeps no finer time, so a value
// read back may differ from the one written in its nanoseconds.
func sameImportUpdateTime(stored, reviewed time.Time) bool {
	return stored.Truncate(time.Microsecond).Equal(reviewed.Truncate(time.Microsecond))
}

// importUpdatesPending reports whether a script an update target names does
// not hold what this save writes (prepared, by position), so an earlier
// attempt of the save has not updated it, or a failure put it back.
func importUpdatesPending(stored []*StoredScript, prepared []*dsl.Script, targets []*importUpdateTarget) bool {
	for index, target := range targets {
		if target == nil || index >= len(stored) || index >= len(prepared) {
			continue
		}
		if stored[index] == nil || stored[index].Script == nil || prepared[index] == nil {
			return true
		}
		current, currentErr := importedScriptContentDigest(stored[index].Script)
		want, wantErr := importedScriptContentDigest(prepared[index])
		if currentErr != nil || wantErr != nil || current != want {
			return true
		}
	}
	return false
}

// sameImportSource reports whether two import source profiles name the same
// importer, allowing the aliases the generic batch save accepts.
func sameImportSource(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	normalizedA, normalizedB := normalizeGenericImportSourceProfile(a), normalizeGenericImportSourceProfile(b)
	return normalizedA != "" && normalizedB != "" && genericImportSourceProfilesCompatible(normalizedA, normalizedB)
}

// hasImportUpdateTargets reports whether a save updates any earlier script.
func hasImportUpdateTargets(targets []*importUpdateTarget) bool {
	for _, target := range targets {
		if target != nil {
			return true
		}
	}
	return false
}

// reserveImportUpdate makes the prepared script at index update the earlier
// imported script target names, if it still may: the script takes that
// script's ID, folder and creation time, and updates records the version the
// update is fenced on. It writes a 409 and returns false when the target is
// gone, archived or from another source; a nil target creates and returns
// true.
func (s *Server) reserveImportUpdate(
	w http.ResponseWriter,
	projectID string,
	index int,
	script *dsl.Script,
	target *importUpdateTarget,
	updates map[string]*StoredScript,
) bool {
	if target == nil {
		return true
	}
	existing, err := s.importUpdateTargetScript(projectID, script, *target)
	if err != nil {
		writeImportUpdateTargetStale(w, index, err)
		return false
	}
	script.ID = existing.ID
	script.FolderID = cloneStringPtr(existing.FolderID)
	script.CreatedAt = existing.Script.CreatedAt
	keepAPEXScriptSettings(script, existing.Script)
	updates[existing.ID] = existing
	return true
}

// keepAPEXScriptSettings carries over what APEX sets on a script and an
// import does not write (its goals and its runtime policy) when the imported
// script sets none, so updating it does not drop them.
func keepAPEXScriptSettings(next, existing *dsl.Script) {
	if next == nil || existing == nil {
		return
	}
	if next.GoalConfig == nil {
		next.GoalConfig = cloneDSLGoalConfig(existing.GoalConfig)
	}
	if existing.Metadata != nil && existing.Metadata.RuntimePolicy != nil &&
		(next.Metadata == nil || next.Metadata.RuntimePolicy == nil) {
		if next.Metadata == nil {
			next.Metadata = &dsl.ScriptMetadata{}
		}
		next.Metadata.RuntimePolicy = &dsl.ScriptRuntimePolicy{
			AllowedTargetHosts: append([]string(nil), existing.Metadata.RuntimePolicy.AllowedTargetHosts...),
		}
	}
}

// writeImportUpdateTargetStale answers a save whose script to update changed,
// moved or was archived after review.
func writeImportUpdateTargetStale(w http.ResponseWriter, index int, err error) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":        fmt.Sprintf("script %d: %v", index+1, err),
		"code":         importUpdateTargetStaleCode,
		"script_index": index,
	})
}

// saveImportedScript updates the earlier script the transaction reserved for
// script.ID as a new version, or creates script.
func (tx *createImportTransaction) saveImportedScript(script *dsl.Script, sourceProfile string) (*StoredScript, error) {
	if tx != nil && script != nil && tx.updates[strings.TrimSpace(script.ID)] != nil {
		return tx.updateImportedScript(script, sourceProfile)
	}
	return tx.createImportedScript(script, sourceProfile)
}

// withoutImportedContentDigest returns metadata without the content digest an
// import save stamps, copying only what it changes.
func withoutImportedContentDigest(metadata *dsl.ScriptMetadata) *dsl.ScriptMetadata {
	if metadata == nil || metadata.Import == nil || metadata.Import.Preserved == nil {
		return metadata
	}
	if _, stamped := metadata.Import.Preserved[dsl.ImportedActionDigestKey]; !stamped {
		return metadata
	}
	copied := *metadata
	importCopy := *metadata.Import
	importCopy.Preserved = make(map[string]any, len(metadata.Import.Preserved))
	for key, value := range metadata.Import.Preserved {
		if key != dsl.ImportedActionDigestKey {
			importCopy.Preserved[key] = value
		}
	}
	copied.Import = &importCopy
	return &copied
}

// keepUpdatedScriptNames gives each script that updates an earlier one that
// script's current name, which an operator may have changed since the import:
// an update replaces steps and settings, and keeps the name and folder.
func keepUpdatedScriptNames(scripts []*dsl.Script, updates map[string]*StoredScript) {
	for _, script := range scripts {
		if script == nil {
			continue
		}
		if existing := updates[strings.TrimSpace(script.ID)]; existing != nil && strings.TrimSpace(existing.Name) != "" {
			script.Name = existing.Name
		}
	}
}
