package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/storage"
)

// scopedRouteAccessProfile is the role-only portion shared by specialized
// resource/session middleware and generated route documentation. The runtime
// middleware still owns the server-instance lookup that resolves a recorder or
// replay session to its project; this profile prevents its role lists from being
// duplicated in the OpenAPI generator.
type scopedRouteAccessProfile struct {
	GlobalRoles  []string
	ProjectRoles []string
}

var (
	scopedViewerRouteAccess = scopedRouteAccessProfile{
		GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
	}
	scopedEditorRouteAccess = scopedRouteAccessProfile{
		GlobalRoles:  []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles: []string{RoleEditor, RoleProjectAdmin},
	}
)

// specializedRouteAccessProfiles keys the identifiers used directly in the
// server.go registration chains. Route discovery records that identifier, so a
// missing or renamed profile is caught by the generated contract tests.
var specializedRouteAccessProfiles = map[string]scopedRouteAccessProfile{
	"recorderViewAuth":                scopedViewerRouteAccess,
	"recorderCreateAuth":              scopedEditorRouteAccess,
	"recorderSessionViewAuth":         scopedViewerRouteAccess,
	"recorderSessionEditAuth":         scopedEditorRouteAccess,
	"browserReplaySessionViewAuth":    scopedViewerRouteAccess,
	"browserReplaySessionEditAuth":    scopedEditorRouteAccess,
	"scriptDebugSessionViewAuth":      scopedViewerRouteAccess,
	"scriptDebugSessionEditAuth":      scopedEditorRouteAccess,
	"browserRecordingCreateAuth":      scopedEditorRouteAccess,
	"browserRecordingSessionViewAuth": scopedViewerRouteAccess,
	"browserRecordingSessionEditAuth": scopedEditorRouteAccess,
}

func mustSpecializedRouteAccessProfile(middleware string) scopedRouteAccessProfile {
	profile, ok := specializedRouteAccessProfiles[middleware]
	if !ok {
		panic("api: missing specialized route access profile for " + middleware)
	}
	return profile
}

func documentedSpecializedAccessRequirement(method, path, middleware string) (accessRequirement, bool) {
	profile, ok := specializedRouteAccessProfiles[middleware]
	if !ok {
		return accessRequirement{}, false
	}
	requirement := accessRequirement{
		GlobalRoles:   append([]string(nil), profile.GlobalRoles...),
		ProjectRoles:  append([]string(nil), profile.ProjectRoles...),
		ProjectLookup: documentedScopedProjectLookup,
	}
	return withRouteServiceAccountScope(method, path, requirement), true
}

// documentedScopedProjectLookup is never part of runtime authorization. It
// marks that a specialized middleware resolves a server-owned resource to a
// project, allowing the common access renderer and contract simulator to model
// the scoped branch without pretending storage alone can perform that lookup.
func documentedScopedProjectLookup(*http.Request, storage.Store) (string, int, string) {
	return "documented-resource-project", 0, ""
}
