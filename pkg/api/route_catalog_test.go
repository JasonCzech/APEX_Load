package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessRequirementForRequestUsesRouteCatalogExactRules(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)

	requirement, ok := accessRequirementForRequest(req)
	if !ok {
		t.Fatal("accessRequirementForRequest() did not match catalog rule")
	}
	if len(requirement.GlobalRoles) != 1 || requirement.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("GlobalRoles = %#v, want system admin only", requirement.GlobalRoles)
	}
}

func TestAccessRequirementFromRouteCatalogDistinguishesMethod(t *testing.T) {
	getRequirement, ok := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/system/settings")
	if !ok {
		t.Fatal("GET /api/v1/system/settings did not match catalog")
	}
	if len(getRequirement.GlobalRoles) != 4 {
		t.Fatalf("GET GlobalRoles = %#v, want viewer/editor/project/system roles", getRequirement.GlobalRoles)
	}

	putRequirement, ok := accessRequirementFromRouteCatalog(http.MethodPut, "/api/v1/system/settings")
	if !ok {
		t.Fatal("PUT /api/v1/system/settings did not match catalog")
	}
	if len(putRequirement.GlobalRoles) != 1 || putRequirement.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("PUT GlobalRoles = %#v, want system admin only", putRequirement.GlobalRoles)
	}
}
