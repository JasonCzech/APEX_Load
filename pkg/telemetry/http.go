package telemetry

import (
	"context"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type originalRequestContextKey struct{}

// HTTPMiddleware instruments inbound HTTP without recording raw paths, query
// strings, headers, or bodies. Once ServeMux selects a route, the span is
// renamed to the bounded method + route pattern and receives http.route.
func HTTPMiddleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	instrumented := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, sanitized *http.Request) {
		original, _ := sanitized.Context().Value(originalRequestContextKey{}).(*http.Request)
		if original == nil {
			original = sanitized
		}
		request := original.WithContext(sanitized.Context())
		next.ServeHTTP(w, request)

		if route := boundedRoutePattern(request.Pattern, request.Method); route != "" {
			span := trace.SpanFromContext(request.Context())
			span.SetName(request.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
		}
	}), "apexload.http", otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return "HTTP " + r.Method
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r == nil {
			instrumented.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), originalRequestContextKey{}, r)
		sanitized := r.Clone(ctx)
		urlCopy := *sanitized.URL
		urlCopy.Path = "/"
		urlCopy.RawPath = ""
		urlCopy.RawQuery = ""
		urlCopy.Fragment = ""
		sanitized.URL = &urlCopy
		instrumented.ServeHTTP(w, sanitized)
	})
}

func boundedRoutePattern(route, method string) string {
	route = strings.TrimSpace(route)
	route = strings.TrimSpace(strings.TrimPrefix(route, strings.TrimSpace(method)+" "))
	if route == "" {
		return ""
	}
	return boundedtext.TruncateUTF8(route, 256)
}
