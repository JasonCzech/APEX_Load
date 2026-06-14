package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// An /api/v1/ route that reaches the auth middleware without a catalog entry must
// be denied, not served. (TestGenericAuthAPIRoutesDeclareAccessRequirement keeps
// every real auth-wrapped route cataloged, so this guards the fail-closed default.)
func TestAuthMiddlewareFailsClosedForUncataloguedAPIRoute(t *testing.T) {
	served := false
	mw := authMiddleware(func() AuthSettings { return AuthSettings{} }, nil, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zzz-not-a-real-catalogued-route", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if served {
		t.Fatal("uncatalogued /api/v1/ route was served (fail-open)")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (fail closed)", rr.Code, http.StatusForbidden)
	}
}

// Non-/api/v1/ paths and CORS preflights still pass through untouched.
func TestAuthMiddlewarePassesThroughNonAPIPaths(t *testing.T) {
	mw := authMiddleware(func() AuthSettings { return AuthSettings{} }, nil, nil)

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"static asset", http.MethodGet, "/dashboard/index.html"},
		{"options preflight", http.MethodOptions, "/api/v1/zzz-not-a-real-catalogued-route"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			served := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				served = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if !served {
				t.Fatalf("%s was not passed through (status %d)", tc.name, rr.Code)
			}
		})
	}
}
