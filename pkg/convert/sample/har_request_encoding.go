package sample

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// maxDecodedHARRequestBodyBytes bounds one compressed upload decoded into the
// script, and maxDecodedHARRequestBodiesBytes all of a HAR's together, so
// highly compressed bodies cannot grow an import without limit.
const (
	maxDecodedHARRequestBodyBytes   = 8 << 20
	maxDecodedHARRequestBodiesBytes = 64 << 20
)

// harReplayContentEncoding is the params.content_encoding that compresses a
// body the way the request's codings did: one gzip or deflate coding, or ""
// when replay cannot.
func harReplayContentEncoding(codings string) string {
	switch codings {
	case "gzip", "x-gzip":
		return "gzip"
	case "deflate":
		return "deflate"
	}
	return ""
}

// replayHARDecodedRequestBody keeps a compressed upload's decoded HAR body
// readable: replay compresses it again when it can (as LoadRunner sends a
// ContentEncoding=gzip step), and otherwise sends it uncompressed.
func replayHARDecodedRequestBody(ex *exchange, codings string, diagnostics *diag.Collector, location, captured string) {
	dropHARRequestHeaders(ex, "Content-Encoding")
	setHARRequestMetadata(ex, "har.request.content_encoding_removed", codings)
	if encoding := harReplayContentEncoding(codings); encoding != "" {
		ex.ContentEncoding = encoding
		diagnostics.Add(diag.Info, "HAR request Content-Encoding", location, fmt.Sprintf("request body was captured after decoding (%s); replay compresses it with %s again", captured, encoding))
		return
	}
	diagnostics.Add(diag.Warning, "HAR request Content-Encoding", location, fmt.Sprintf("request body was captured after decoding (%s). APEX compresses request bodies only with gzip or deflate, so replay sends it uncompressed without Content-Encoding; review if the server requires compressed uploads", captured))
}

// normalizeHARRequestContentEncoding handles a compressed upload whose length
// is not in question (normalizeHARContentLength already settled one). The
// proxy recorder stores the compressed bytes the app sent: they become the
// readable body that replay compresses again, so correlation and
// parameterization reach the values inside, as in a LoadRunner recording. A
// body stored decoded is sent compressed again. Bytes that do not decode
// completely to text, a coding replay cannot apply, or a body past the HAR's
// decoding budget (remaining, in bytes) stay as recorded.
func normalizeHARRequestContentEncoding(ex *exchange, remaining *int64, diagnostics *diag.Collector, location string) {
	if ex.ContentEncoding != "" || ex.Body == "" || harHasRequestHeader(ex, "Content-Length") {
		return
	}
	codings, ok := harRequestContentCodings(ex.HeaderList)
	if !ok || codings == "" {
		return
	}
	if ex.BodyEncoding == "" {
		if harLooksLikeText(ex.Body) {
			replayHARDecodedRequestBody(ex, codings, diagnostics, location, "Content-Encoding: "+codings)
		}
		return
	}
	encoding := harReplayContentEncoding(codings)
	if ex.BodyEncoding != "base64" || encoding == "" {
		return
	}
	raw, err := decodeBase64Text(ex.Body)
	if err != nil {
		return
	}
	limit := min(int64(maxDecodedHARRequestBodyBytes), *remaining)
	decoded, produced, ok := inflateHARRequestBody(raw, encoding, limit)
	// Every byte decoded counts, kept or not, so the budget bounds the work.
	*remaining -= produced
	if produced > limit && limit < maxDecodedHARRequestBodyBytes {
		diagnostics.Add(diag.Info, "HAR request Content-Encoding", location, fmt.Sprintf("the HAR's compressed uploads exceed the %d MiB it decodes in all, so this one stays as its recorded compressed bytes", maxDecodedHARRequestBodiesBytes>>20))
	}
	if !ok || !harLooksLikeText(string(decoded)) {
		return
	}
	ex.Body, ex.BodyEncoding = string(decoded), ""
	ex.ContentEncoding = encoding
	dropHARRequestHeaders(ex, "Content-Encoding")
	setHARRequestMetadata(ex, "har.request.content_encoding_decoded", codings)
	setHARRequestMetadata(ex, "har.request.compressed_length", len(raw))
	if _, recorded := ex.Metadata["har.request.content_length"]; recorded {
		// The script's body is the readable one now.
		setHARRequestMetadata(ex, "har.request.content_length", len(decoded))
	}
	diagnostics.Add(diag.Info, "HAR request Content-Encoding", location, fmt.Sprintf("request body was captured compressed (Content-Encoding: %s, %d bytes); the script holds the readable body (%d bytes) and replay compresses it with %s again", codings, len(raw), len(decoded), encoding))
}

// inflateHARRequestBody decodes a complete gzip or zlib (HTTP deflate) stream
// of at most limit bytes. produced counts the bytes decoded, one past limit
// for a stream that decodes further. A raw deflate stream is not decoded:
// replay would send it as zlib.
func inflateHARRequestBody(raw []byte, encoding string, limit int64) (decoded []byte, produced int64, ok bool) {
	var reader io.ReadCloser
	var err error
	switch encoding {
	case "gzip":
		reader, err = gzip.NewReader(bytes.NewReader(raw))
	case "deflate":
		reader, err = zlib.NewReader(bytes.NewReader(raw))
	default:
		return nil, 0, false
	}
	if err != nil {
		return nil, 0, false
	}
	defer reader.Close()
	limit = max(limit, 0)
	decoded, err = io.ReadAll(io.LimitReader(reader, limit+1))
	produced = int64(len(decoded))
	if err != nil || produced > limit {
		return nil, produced, false
	}
	return decoded, produced, true
}

func harHasRequestHeader(ex *exchange, name string) bool {
	for _, header := range ex.HeaderList {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}
