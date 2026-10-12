package api

import (
	"context"
	"net/http"
	"regexp"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

var replaySandboxSecretReference = regexp.MustCompile(`\{\{\s*secret\.`)

// Sandbox data is supplied explicitly by the operator. This path never reads a
// pool or run, and cannot acquire a production table or finalization capability.
func (s *Server) prepareReplayLiveTables(ctx context.Context, w http.ResponseWriter, script *dsl.Script, seeds []engine.LiveTableSandboxSeed) bool {
	aliases, err := dsl.ScriptLiveTableAliases(script)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid live table actions")
		return false
	}
	if len(aliases) == 0 && len(seeds) == 0 {
		return true
	}
	if !s.requireDomainFeatureForProject(w, script.ProjectID, "data.live_tables") {
		return false
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if !requireAuthorizedProjectMatch(w, r, script.ProjectID) {
		return false
	}
	if s.authSettingsForRequest().Mode != authModeDev {
		user, _ := AuthenticatedUserFromContext(ctx)
		account, _ := AuthenticatedServiceAccountFromContext(ctx)
		requirement := withServiceAccountScope(viewerProjectAccess(func(*http.Request, storage.Store) (string, int, string) {
			return script.ProjectID, 0, ""
		}), "read")
		if !authorizeAccessRequirement(w, r, s.store, authResolution{User: user, ServiceAccount: account}, requirement) {
			return false
		}
	}
	if len(aliases) > 0 && len(seeds) == 0 {
		writeError(w, http.StatusBadRequest, "configure isolated live table seeds in Debug options before replaying")
		return false
	}
	for _, seed := range seeds {
		for _, row := range seed.Rows {
			if row.ID != "" || row.Version != 0 || row.State != "" || row.DeliveryAttempts != 0 {
				writeError(w, http.StatusBadRequest, "sandbox seed rows accept values only; record identities are created per replay")
				return false
			}
			for _, value := range row.Values {
				if replaySandboxSecretReference.MatchString(value) {
					writeError(w, http.StatusBadRequest, "managed secrets cannot seed isolated live tables")
					return false
				}
			}
		}
	}
	if err := engine.ValidateLiveTableSandboxSeeds(script, seeds); err != nil {
		// Validation errors describe structure and limits; never include rows.
		writeError(w, http.StatusBadRequest, "invalid isolated live table seeds: "+err.Error())
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}
