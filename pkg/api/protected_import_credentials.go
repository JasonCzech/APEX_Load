package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"net/http"

	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const optionalCredentialMigrationUnavailableWarning = "Optional Project Secret migration is unavailable; the imported destination retains its credential values and remains runnable."

const protectedImportCredentialMetadataKey = importreview.ProtectedImportCredentialMetadataKey

type protectedImportCredentialLocation = importreview.ProtectedImportCredentialLocation

// protectedImportCredentialDescriptor is the disclosure-safe portion of a
// shared credential requirement. It is allowed in the editable inspection
// script so the existing SecretBindingReview apply route can validate stable
// requirement IDs without receiving a source value.
type protectedImportCredentialDescriptor = importreview.ProtectedImportCredentialDescriptor

// attachImportCredentialReview maps converter-authored requirements back to
// the generated scripts and records the same tamper-evident binding metadata
// used by protected value handoffs. Multi-script converters such as JMeter get
// an explicit ScriptIndex, rather than silently presenting every requirement
// as though it belonged to the first script.
var attachImportCredentialReview = importreview.AttachImportCredentialReview

type importCredentialReviewWork = importreview.ImportCredentialReviewWork

var attachImportCredentialReviewWithWork = importreview.AttachImportCredentialReviewWithWork

var narrowImportCredentialScriptMatches = importreview.NarrowImportCredentialScriptMatches

var importCredentialScriptSourcePaths = importreview.ImportCredentialScriptSourcePaths

var normalizeImportCredentialSourcePath = importreview.NormalizeImportCredentialSourcePath

var importCredentialScriptRequestIdentities = importreview.ImportCredentialScriptRequestIdentities

var importCredentialRequestKeyMatchesIdentities = importreview.ImportCredentialRequestKeyMatchesIdentities

func attachProtectedImportCredentialReview(
	script *dsl.Script,
	requirements []credentialpolicy.Requirement,
) ([]SecretBindingRequirement, error) {
	if script == nil || len(requirements) == 0 {
		return nil, nil
	}
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		return nil, err
	}
	locations := make(map[string][]protectedImportCredentialLocation, len(requirements))
	for _, requirement := range requirements {
		replacement := strings.TrimSpace(requirement.Replacement)
		if replacement != "" {
			locations[replacement] = findProtectedImportCredentialLocations(raw, replacement)
		}
	}
	return attachProtectedImportCredentialReviewWithLocations(script, requirements, locations)
}

var attachProtectedImportCredentialReviewWithLocations = importreview.AttachProtectedImportCredentialReviewWithLocations

var mergeProtectedImportCredentialReview = importreview.MergeProtectedImportCredentialReview

func (s *Server) attachProtectedImportCredentialMigration(
	r *http.Request,
	response map[string]any,
	projectID string,
	protected []credentialpolicy.Requirement,
	public []SecretBindingRequirement,
) []SecretBindingRequirement {
	if s == nil || response == nil || len(public) == 0 ||
		s.protectedSourceSessions == nil || !s.protectedSourceSessions.Available() {
		return public
	}
	if sessionID, _ := response["source_secret_session_id"].(string); strings.TrimSpace(sessionID) != "" {
		return public
	}
	allowed := make(map[string]struct{}, len(public))
	for _, requirement := range public {
		allowed[requirement.ID] = struct{}{}
	}
	values := make(map[string]string, len(public))
	for _, requirement := range protected {
		if _, ok := allowed[requirement.ID]; ok {
			value, recoverable := credentialpolicy.RecoverableSourceCredentialValue(requirement.Value)
			if recoverable {
				values[requirement.ID] = value
			}
		}
	}
	if len(values) == 0 {
		return public
	}
	sessionID, err := s.registerSourceSecretSession(r, projectID, values)
	if err != nil {
		warnings, _ := response["warnings"].([]string)
		response["warnings"] = append(warnings, optionalCredentialMigrationUnavailableWarning)
		return public
	}
	if strings.TrimSpace(sessionID) == "" {
		return public
	}
	for index := range public {
		_, public[index].SourceValueAvailable = values[public[index].ID]
	}
	response["source_secret_session_id"] = sessionID
	response["source_secret_session_expires_at"] = time.Now().UTC().Add(sourceSecretSessionTTL)
	response["source_secret_migration_allowed"] = true
	return public
}

var loadProtectedImportCredentialDescriptors = importreview.LoadProtectedImportCredentialDescriptors

var applyProtectedImportCredentialBindings = importreview.ApplyProtectedImportCredentialBindings

func stripProtectedImportCredentialMetadata(script *dsl.Script) {
	setProtectedImportCredentialDescriptors(script, nil)
}

var setProtectedImportCredentialDescriptors = importreview.SetProtectedImportCredentialDescriptors

var publicProtectedImportCredentialDescriptors = importreview.PublicProtectedImportCredentialDescriptors

type protectedReferenceReplacement = importreview.ProtectedReferenceReplacement

var applyProtectedReferenceReplacements = importreview.ApplyProtectedReferenceReplacements

var replaceProtectedReferenceOccurrence = importreview.ReplaceProtectedReferenceOccurrence

var protectedReferenceReplacementCount = importreview.ProtectedReferenceReplacementCount

var findProtectedImportCredentialLocations = importreview.FindProtectedImportCredentialLocations

var collectProtectedImportCredentialLocations = importreview.CollectProtectedImportCredentialLocations

var collectProtectedImportCredentialStringLocations = importreview.CollectProtectedImportCredentialStringLocations
