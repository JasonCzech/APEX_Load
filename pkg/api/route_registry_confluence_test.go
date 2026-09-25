package api

import (
	"net/http"
	"reflect"
	"testing"
)

func TestConfluenceRoutesRequireProjectRoles(t *testing.T) {
	cases := []struct {
		method string
		path   string
		roles  []string
	}{
		{http.MethodGet, "/api/v1/projects/{id}/integrations/confluence-connections", []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
		{http.MethodPost, "/api/v1/projects/{id}/integrations/confluence-connections", []string{RoleProjectAdmin}},
		{http.MethodPut, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}", []string{RoleProjectAdmin}},
		{http.MethodDelete, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}", []string{RoleProjectAdmin}},
		{http.MethodPost, "/api/v1/projects/{id}/integrations/confluence-connections/{connection_id}/test", []string{RoleProjectAdmin}},
		{http.MethodGet, "/api/v1/tests/{id}/publish/confluence", []string{RoleViewer, RoleEditor, RoleProjectAdmin}},
		{http.MethodPost, "/api/v1/tests/{id}/publish/confluence", []string{RoleEditor, RoleProjectAdmin}},
	}
	for _, test := range cases {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			requirement, ok := accessRequirementFromRouteCatalog(test.method, test.path)
			if !ok || requirement.ProjectLookup == nil {
				t.Fatal("route has no project-scoped authorization requirement")
			}
			if !reflect.DeepEqual(requirement.ProjectRoles, test.roles) {
				t.Fatalf("project roles = %v, want %v", requirement.ProjectRoles, test.roles)
			}
		})
	}
}
