package api

import (
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) validateLiveTablePolicy(projectID string) error {
	if tr.projectStore == nil {
		return errors.New("live table project policy is unavailable")
	}
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil {
		return errors.New("live table project is unavailable")
	}
	policies, ok := tr.dataPoolStore.(storage.DomainFeaturePolicyStore)
	if !ok {
		return errors.New("live table domain policy is unavailable")
	}
	policy, err := policies.GetDomainFeaturePolicy(project.DomainID)
	if err != nil {
		return errors.New("live table domain policy is unavailable")
	}
	for _, state := range domainFeatureResponse(policy).Features {
		if state.ID == "data.live_tables" {
			if !state.EffectiveEnabled {
				return errors.New("live tables are disabled for this domain")
			}
			return nil
		}
	}
	return errors.New("live table domain policy is unavailable")
}

// Finalization is a separate pool mutation. Re-read the initiating principal's
// current grants; possession of an earlier run-start authorization is not a
// perpetual permission to change a reusable data pool.
func (tr *TestRunner) authorizeLiveTablePersistence(test *ManagedTest) error {
	tr.mu.RLock()
	subject := firstNonEmpty(test.StartedBySubject, test.CreatedBySubject)
	mode := normalizeAuthSettings(tr.settings.Auth).Mode
	projectID := test.ProjectID
	tr.mu.RUnlock()
	if isAuthBypassMode(mode) && (subject == "dev-mode" || subject == "") {
		return nil
	}
	store, ok := tr.dataPoolStore.(storage.Store)
	if !ok {
		return errors.New("live table output authorization is unavailable")
	}
	var user *AuthenticatedUser
	if strings.HasPrefix(subject, "service-account:") {
		account, err := store.GetServiceAccount(strings.TrimPrefix(subject, "service-account:"))
		if err != nil || account == nil || !account.Active || (account.ExpiresAt != nil && !account.ExpiresAt.After(time.Now())) {
			return errors.New("live table output actor is no longer active")
		}
		if !serviceAccountAllowsScopes(&AuthenticatedServiceAccount{ID: account.ID, Scopes: account.Scopes}, []string{"write"}) {
			return errors.New("live table output requires current data pool write scope")
		}
		user = authenticatedUserFromServiceAccount(account)
	} else {
		stored, err := store.GetUserBySubject(subject)
		if err != nil || stored == nil || !stored.Active {
			return errors.New("live table output actor is no longer active")
		}
		user = authenticatedUserFromStoredUser(stored)
		user.LocalScopeBoundViewer = mode == authModeLocal
	}
	if user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) || userHasProjectOrParentRole(store, user, projectID, RoleEditor, RoleProjectAdmin) {
		return nil
	}
	return errors.New("live table output requires current permission to manage project data pools")
}
