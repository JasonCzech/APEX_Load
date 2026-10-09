package http

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
	// isFinal marks the request's final response among scoped candidates.
	isFinal bool
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

// ExtractorDFETypes are the data-format transforms an extractor can apply to
// a response body before searching.
var ExtractorDFETypes = map[string]bool{
	"base64": true, "gzip": true, "json_to_xml": true, "xml_to_json": true,
	"url_decode": true, "url_encode": true, "html_unescape": true, "html_escape": true,
}

// MaxExtractorDFEs bounds an extractor's transform chain.
const MaxExtractorDFEs = 8

// withExtractorDFEs returns the response with its body transformed by the
// extractor's DFE chain. A body the chain cannot transform (for example one
// that is not Base64) is a miss, as LoadRunner finds no value in it.
func withExtractorDFEs(ext protocol.Extractor, response responseSurfaces) (responseSurfaces, error) {
	if len(ext.DFEs) == 0 {
		return response, nil
	}
	chain := make([]dataFormattingTransform, 0, len(ext.DFEs))
	for _, name := range ext.DFEs {
		name = strings.ToLower(strings.TrimSpace(name))
		if !ExtractorDFETypes[name] {
			return response, fmt.Errorf("unsupported extractor data format extension %q", name)
		}
		chain = append(chain, dataFormattingTransform{Type: name})
	}
	body, err := applyDataFormattingChain(response.body, false, chain, maxExtractorInputWorkBytes)
	if err != nil {
		return response, fmt.Errorf("%w: %v", protocol.ErrExtractionMiss, err)
	}
	response.body = body
	return response, nil
}
