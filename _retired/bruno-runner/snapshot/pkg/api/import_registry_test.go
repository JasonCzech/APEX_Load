package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// The registry is the inventory of import formats. These guards hold the route
// table, the batch-commit allowlist, and the support-matrix endpoints to it, so
// registering a format is the single act that wires it up — and forgetting one
// of the pieces fails here rather than shipping a half-registered importer.

func TestImporterRegistrationsAreUniqueAndSorted(t *testing.T) {
	seen := map[string]string{}
	previous := ""
	for _, registration := range importerRegistrations {
		if registration.Profile == "" {
			t.Fatalf("registration %q has no profile", registration.DisplayName)
		}
		if registration.DisplayName == "" {
			t.Fatalf("registration %q has no display name", registration.Profile)
		}
		if owner, clash := seen[registration.Profile]; clash {
			t.Fatalf("profile %q is registered twice (also %q)", registration.Profile, owner)
		}
		seen[registration.Profile] = registration.Profile
		for _, alias := range registration.ProfileAliases {
			if owner, clash := seen[alias]; clash {
				t.Fatalf("alias %q collides with %q", alias, owner)
			}
			seen[alias] = registration.Profile
		}
		if previous != "" && registration.Profile < previous {
			t.Fatalf("registrations are out of order: %q follows %q", registration.Profile, previous)
		}
		previous = registration.Profile
	}
}

// An alias must resolve to a registered profile, never to another alias.
func TestImporterProfileNormalizationResolvesToRegisteredProfiles(t *testing.T) {
	normalization := importerProfileNormalization()
	for wire, profile := range normalization {
		registration, ok := importerRegistrationByProfile(profile)
		if !ok {
			t.Fatalf("wire spelling %q normalizes to unregistered profile %q", wire, profile)
		}
		if !registration.BatchCommit {
			t.Fatalf("wire spelling %q is accepted for %q, which does not declare BatchCommit", wire, profile)
		}
	}
	values := importerProfileWireValues()
	if !sort.StringsAreSorted(values) {
		t.Fatalf("wire values are not sorted: %v", values)
	}
	if len(values) != len(normalization) {
		t.Fatalf("wire values = %d, normalization entries = %d", len(values), len(normalization))
	}
}

// Every format that publishes a matrix must build one without panicking and
// without answering empty — an entry that returns nothing is a registration
// mistake, not a format with nothing to say.
func TestImporterSupportMatricesBuildNonEmptyPayloads(t *testing.T) {
	server := NewServer(ServerConfig{})
	for _, registration := range importerRegistrations {
		if registration.SupportMatrix == nil {
			continue
		}
		t.Run(registration.Profile, func(t *testing.T) {
			payload := registration.SupportMatrix(server)
			if len(payload) == 0 {
				t.Fatalf("%s published an empty support matrix", registration.Profile)
			}
			if _, err := json.Marshal(payload); err != nil {
				t.Fatalf("%s support matrix does not marshal: %v", registration.Profile, err)
			}
		})
	}
}

// A registered route must actually be served. This drives the real mux rather
// than a parallel description of it, so a registration whose route was never
// added to server.go fails here.
func TestImporterRegistrationRoutesAreServed(t *testing.T) {
	server := NewServer(ServerConfig{})
	for _, registration := range importerRegistrations {
		if registration.Route == "" {
			continue
		}
		t.Run(registration.Profile, func(t *testing.T) {
			path := "/api/v1/scripts/import/" + registration.Route
			if status := probeRouteStatus(server, http.MethodPost, path); status == http.StatusNotFound {
				t.Fatalf("POST %s is registered in the importer registry but not served", path)
			}
			if registration.SupportMatrix == nil {
				return
			}
			matrixPath := path + "/support-matrix"
			if status := probeRouteStatus(server, http.MethodGet, matrixPath); status != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", matrixPath, status)
			}
		})
	}
}

// The inverse direction: a support-matrix endpoint under /scripts/import must
// belong to a registered format, so an endpoint cannot outlive its registration.
func TestImportSupportMatrixRoutesBelongToRegisteredFormats(t *testing.T) {
	routed := map[string]bool{}
	for _, registration := range importerRegistrations {
		if registration.Route != "" && registration.SupportMatrix != nil {
			routed[registration.Route] = true
		}
	}
	// Formats reviewed through the import surface without a converter of their
	// own. They publish guidance, not a conversion matrix, so they are not
	// importer registrations.
	reviewOnly := map[string]bool{"oracle-ebusiness": true}

	server := NewServer(ServerConfig{})
	for _, route := range importSupportMatrixRoutePaths() {
		segment := strings.TrimSuffix(strings.TrimPrefix(route, "/api/v1/scripts/import/"), "/support-matrix")
		if reviewOnly[segment] || routed[segment] {
			continue
		}
		if status := probeRouteStatus(server, http.MethodGet, route); status != http.StatusNotFound {
			t.Fatalf("%s is served but %q is not a registered importer with a support matrix", route, segment)
		}
	}
}

// importSupportMatrixRoutePaths is the documented set of support-matrix
// endpoints under the script import surface.
func importSupportMatrixRoutePaths() []string {
	return []string{
		"/api/v1/scripts/import/asyncapi/support-matrix",
		"/api/v1/scripts/import/broker-config/support-matrix",
		"/api/v1/scripts/import/bruno/support-matrix",
		"/api/v1/scripts/import/cloudtest/support-matrix",
		"/api/v1/scripts/import/devweb/support-matrix",
		"/api/v1/scripts/import/enterprise-script-archive/support-matrix",
		"/api/v1/scripts/import/eventhubs-capture/support-matrix",
		"/api/v1/scripts/import/jmeter/support-matrix",
		"/api/v1/scripts/import/k6/support-matrix",
		"/api/v1/scripts/import/openapi/support-matrix",
		"/api/v1/scripts/import/oracle-ebusiness/support-matrix",
		"/api/v1/scripts/import/postman/support-matrix",
		"/api/v1/scripts/import/raml/support-matrix",
	}
}

func probeRouteStatus(server *Server, method, path string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	return resp.Code
}

// An unregistered profile is rejected by the batch commit endpoint rather than
// silently accepted under a default.
func TestNormalizeGenericImportSourceProfileRejectsUnregisteredProfiles(t *testing.T) {
	if got := normalizeGenericImportSourceProfile("not-a-real-importer"); got != "" {
		t.Fatalf("normalize(unregistered) = %q, want empty", got)
	}
	if got := normalizeGenericImportSourceProfile("  SAMPLE-IMPORT  "); got != "sample" {
		t.Fatalf("normalize(alias) = %q, want sample", got)
	}
	// Formats with their own commit route must not leak into the create-only path.
	for _, profile := range []string{"openapi", "postman", "asyncapi", "bruno", "jmeter", "enterprise_script_archive"} {
		if got := normalizeGenericImportSourceProfile(profile); got != "" {
			t.Fatalf("normalize(%q) = %q, want empty: it commits through its own route", profile, got)
		}
	}
}
