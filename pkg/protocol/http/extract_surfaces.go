package http

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// responseSurfaces is everything an extractor can search in one response.
type responseSurfaces struct {
	headers http.Header
	body    []byte
	// url is the final request URL, after any redirects were followed.
	url string
	// status is the final response status code; zero when unknown.
	status int
}

// textSurface returns the text a cookies, url or status extractor searches:
// the Set-Cookie header lines (as LoadRunner's Scope=Cookies sees them), the
// final URL, or the status code.
func (response responseSurfaces) textSurface(source string) []byte {
	switch source {
	case "cookies":
		return setCookieSurface(response.headers)
	case "url":
		return []byte(response.url)
	case "status":
		if response.status == 0 {
			return nil
		}
		return []byte(strconv.Itoa(response.status))
	}
	return nil
}

// setCookieSurface renders every Set-Cookie header as "Set-Cookie: value"
// followed by CRLF, in the order received, so boundaries written with or
// without the header name both match.
func setCookieSurface(headers http.Header) []byte {
	var surface []byte
	for _, value := range headers.Values("Set-Cookie") {
		surface = append(surface, "Set-Cookie: "...)
		surface = append(surface, value...)
		surface = append(surface, "\r\n"...)
	}
	return surface
}

// extractFromSurface reads a cookies, url or status surface. Type "" takes
// the whole surface; the text types search it.
func extractFromSurface(ext protocol.Extractor, content []byte) (string, error) {
	switch ext.Type {
	case "":
		if len(content) == 0 {
			return "", fmt.Errorf("%w: %s is empty", protocol.ErrExtractionMiss, ext.Source)
		}
		return string(content), nil
	case "regex", "regex_count", "regex_ordinal", "boundary":
		return extractFromBody(ext, content)
	default:
		return "", fmt.Errorf("unsupported %s extraction type: %s", ext.Source, ext.Type)
	}
}
