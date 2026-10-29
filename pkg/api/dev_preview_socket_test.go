package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevPreviewSocketQueryIsScopedToDevelopment(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	settings := AuthSettings{Mode: authModeDev}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws?apexload_dev_view=user&apexload_dev_project=project-a", nil)
	resolution, _, ok := devPreviewResolutionForRequest(settings, request)
	if !ok || resolution.User == nil || !resolution.User.hasProjectRole("project-a", RoleEditor) || resolution.User.hasProjectRole("project-b", RoleEditor) {
		t.Fatal("socket preview did not retain only its selected project")
	}

	for _, target := range []string{
		"/api/v1/ws?apexload_dev_view=user&apexload_dev_project=invalid%2Fproject",
		"/api/v1/ws?apexload_dev_view=user",
	} {
		resolution, _, ok := devPreviewResolutionForRequest(settings, httptest.NewRequest(http.MethodGet, target, nil))
		if !ok || resolution.User.hasProjectRole("project-a", RoleEditor) {
			t.Fatalf("invalid or absent project broadened preview: %s", target)
		}
	}

	for _, target := range []string{
		"/api/v1/scripts?apexload_dev_view=user&apexload_dev_project=project-a",
		"/api/v1/ws?apexload_dev_view=admin&apexload_dev_project=project-a",
	} {
		if _, _, ok := devPreviewResolutionForRequest(settings, httptest.NewRequest(http.MethodGet, target, nil)); ok {
			t.Fatalf("query unexpectedly activated preview: %s", target)
		}
	}
	if _, _, ok := devPreviewResolutionForRequest(AuthSettings{Mode: authModeTrustedHeaders}, request); ok {
		t.Fatal("preview query was honored in trusted-header mode")
	}
	t.Setenv("APEXLOAD_ENV", "production")
	if _, _, ok := devPreviewResolutionForRequest(settings, request); ok {
		t.Fatal("preview query was honored in production")
	}
}

func TestDevPreviewSocketQueryDoesNotOverrideHeaders(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws?apexload_dev_view=user&apexload_dev_project=project-a", nil)
	request.Header.Set(devViewHeader, devViewModeUser)
	resolution, _, ok := devPreviewResolutionForRequest(AuthSettings{Mode: authModeDev}, request)
	if !ok || resolution.User.hasProjectRole("project-a", RoleEditor) {
		t.Fatal("query supplied a project absent from the authoritative header preview")
	}
}
