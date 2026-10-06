package api

import (
	"context"
	"net/http"
	"time"
)

// The deadline surrounds authentication so the durable contextual project
// resolver receives the same bounded request as the resource scan. Existing
// contextless global authentication calls do not acquire fake cancellation via
// goroutines; their limitation remains separate from this reader guarantee.
func withAPIRouteRequestTimeout(next http.Handler, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Keep the handler binder inline in the catalog, as for existing routes. A
// constructor that references the handler creates a package initialization
// dependency through its final authorization back into the route catalog.
func browserResourceDeadlineRoute(route apiRouteSpec) apiRouteSpec {
	route.RequestTimeout = browserResourceReadTimeout
	return route
}
