package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestScopedRouteAccessProfilesPreserveRuntimeRoles(t *testing.T) {
	tests := []struct {
		name         string
		profile      scopedRouteAccessProfile
		globalRoles  []string
		projectRoles []string
	}{
		{
			name:         "viewer",
			profile:      scopedViewerRouteAccess,
			globalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			projectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		},
		{
			name:         "editor",
			profile:      scopedEditorRouteAccess,
			globalRoles:  []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			projectRoles: []string{RoleEditor, RoleProjectAdmin},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !reflect.DeepEqual(test.profile.GlobalRoles, test.globalRoles) {
				t.Errorf("global roles = %v, want %v", test.profile.GlobalRoles, test.globalRoles)
			}
			if !reflect.DeepEqual(test.profile.ProjectRoles, test.projectRoles) {
				t.Errorf("project roles = %v, want %v", test.profile.ProjectRoles, test.projectRoles)
			}
		})
	}
}

func TestMustSpecializedRouteAccessProfileFailsClosed(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing specialized profile did not panic")
		}
	}()
	_ = mustSpecializedRouteAccessProfile("missing-auth-middleware")
}

func TestSpecializedAuthRoutesDeclareAccessProfiles(t *testing.T) {
	routes, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	usedProfiles := map[string]bool{}
	for _, route := range routes {
		if !route.Authenticated || route.GenericAuth || inHandlerAuthenticatedRoute(route.Method, route.Path, route.Handler) || route.Path == bridgeStatusPath {
			continue
		}
		req := qaContractRequest(t, route)
		if _, ok := accessRequirementForRequest(req); ok {
			continue
		}
		if _, ok := documentedSpecializedAccessRequirement(route.Method, route.Path, route.AuthMiddleware); !ok {
			t.Errorf("%s %s uses %q but has neither runtime access metadata nor a specialized profile", route.Method, route.Path, route.AuthMiddleware)
			continue
		}
		usedProfiles[route.AuthMiddleware] = true
	}
	for middleware := range specializedRouteAccessProfiles {
		if !usedProfiles[middleware] {
			t.Errorf("specialized access profile %q is not used by a route missing runtime metadata", middleware)
		}
	}
}

func TestDocumentedSpecializedAccessRequirementIsProjectScoped(t *testing.T) {
	requirement, ok := documentedSpecializedAccessRequirement(http.MethodGet, "/api/v1/recorder/{id}", "recorderSessionViewAuth")
	if !ok {
		t.Fatal("recorder session view profile was not found")
	}
	if requirement.ProjectLookup == nil {
		t.Fatal("documented specialized access has no project lookup marker")
	}
	if requirement.ServiceAccountScope != "read" {
		t.Fatalf("documented specialized GET scope = %q, want read", requirement.ServiceAccountScope)
	}
	projectID, status, message := requirement.ProjectLookup(httptest.NewRequest("GET", "/", nil), nil)
	if projectID == "" || status != 0 || message != "" {
		t.Fatalf("project lookup marker = (%q, %d, %q), want a successful documented scope", projectID, status, message)
	}
}
