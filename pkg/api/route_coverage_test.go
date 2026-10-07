package api

import (
	"strings"
	"testing"
)

// intentionallyPublicAPIRoutes are the /api/v1/ routes registered with NO auth
// middleware on purpose: the OIDC login flow, liveness/version/readiness probes, the
// public recording-agent download, the HMAC-validated GitHub webhook receiver, and the
// monitoring-collector heartbeat (collector-token authenticated in-handler).
// Every other /api/v1/ route must be wrapped in an auth middleware (generic or custom).
//
// This list is the explicit, reviewed surface of anonymous endpoints. A new route added
// without auth that is not listed here fails TestUnauthenticatedAPIRoutesAreAllowlisted —
// closing the gap that the route-catalog/fingerprint tests cannot catch, because those
// only enumerate auth()-wrapped routes (a route registered with no auth() is invisible to
// them, as the recording-agent download was).
var intentionallyPublicAPIRoutes = map[string]bool{
	// Collector jobs use the same in-handler constant-time token validation as
	// heartbeat; tokens identify the collector and project, never a browser user.
	"GET /api/v1/monitoring/collectors/{id}/jobs":                   true,
	"POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/samples": true,
	// Poll authority requires the collector bearer token in-handler, then exact
	// immutable source/job/instance ownership; browser roles grant no authority.
	"POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/management-polls": true,
	// Inspection operations validate the assigned collector token in-handler;
	// submitted results also require a signed, expiring operation grant.
	"GET /api/v1/monitoring/collectors/{id}/inspections":                                 true,
	"POST /api/v1/monitoring/collectors/{id}/inspections/{operation_id}/result":          true,
	"GET /api/v1/monitoring/collectors/{id}/dynatrace-operations":                        true,
	"POST /api/v1/monitoring/collectors/{id}/dynatrace-operations/{operation_id}/result": true,
	"POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/scan-reservations":            true,
	// Elastic inspection and cleanup handlers authenticate the assigned collector
	// token and fence results against signed, expiring grants and immutable jobs.
	"GET /api/v1/monitoring/collectors/{id}/elastic-inspections":                        true,
	"POST /api/v1/monitoring/collectors/{id}/elastic-inspections/{operation_id}/result": true,
	"GET /api/v1/monitoring/collectors/{id}/elastic-cleanups":                           true,
	"POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/cleanup":                     true,
	"GET /api/v1/auth/login":    true,
	"POST /api/v1/auth/login":   true,
	"GET /api/v1/auth/callback": true,
	// Desktop callback finalization is anonymous at the HTTP auth layer but is
	// protected by the per-tab capability and one-time server-side OAuth state.
	"POST /api/v1/auth/callback":        true,
	"POST /api/v1/auth/logout":          true,
	"GET /api/v1/auth/me":               true,
	"GET /api/v1/auth/diagnostics":      true,
	"GET /api/v1/auth/roles":            true,
	"GET /api/v1/auth/access-matrix":    true,
	"GET /api/v1/auth/policy":           true,
	"GET /api/v1/auth/effective-access": true,
	"GET /api/v1/system/health":         true,
	"GET /api/v1/system/version":        true,
	"GET /api/v1/system/readiness":      true,
	// The leader-only Service and deployment smoke checks need an anonymous
	// readiness probe; the response exposes only routing/leadership booleans.
	"GET /api/v1/system/execution-readiness": true,
	// The exchange is the pre-authentication boundary: it validates a signed
	// GitHub Actions ID token and returns a short-lived project CI token.
	"POST /api/v1/auth/ci/github/exchange": true,
	// Local browser recording pairs before an authenticated bridge session
	// exists; the handler requires a high-entropy, expiring token and applies
	// per-client lockout to failed attempts.
	"POST /api/v1/bridge/pair": true,
	// The reverse generator gateway is anonymous only at the HTTP middleware
	// layer: its handler requires a write-only, target-scoped enrollment token
	// and, for burst workers, the persisted one-time launch identity before the
	// WebSocket upgrade. Browser Origins are refused as a separate CSRF guard.
	"GET /api/v1/generator-gateway/connect":             true,
	"POST /api/v1/integrations/github/webhook":          true,
	"POST /api/v1/monitoring/collectors/{id}/heartbeat": true,
}

func TestUnauthenticatedAPIRoutesAreAllowlisted(t *testing.T) {
	seen := map[string]bool{}
	for _, route := range registeredServerRoutes(t) {
		if !strings.HasPrefix(route.Path, "/api/v1/") || !route.Unauthenticated {
			continue
		}
		key := route.Method + " " + route.Path
		seen[key] = true
		if !intentionallyPublicAPIRoutes[key] {
			t.Errorf("%s is registered with no auth middleware and is not in intentionallyPublicAPIRoutes — "+
				"wrap it in auth() (and add it to the route catalog), or, if it is intentionally public, "+
				"add it to the allow-list with a justification", key)
		}
	}
	for key := range intentionallyPublicAPIRoutes {
		if !seen[key] {
			t.Errorf("intentionallyPublicAPIRoutes lists %q but no matching unauthenticated route is registered "+
				"(stale allow-list entry — remove it or fix the route)", key)
		}
	}
}
