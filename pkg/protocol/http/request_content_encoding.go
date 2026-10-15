package http

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// RequestContentEncodingParam names the action param that compresses the
// request body before it is sent and labels it with Content-Encoding, as
// LoadRunner's ContentEncoding attribute does: gzip, or deflate (the zlib
// format HTTP means by deflate).
const RequestContentEncodingParam = "content_encoding"

// RequestContentEncodings lists the values params.content_encoding accepts.
var RequestContentEncodings = []string{"gzip", "deflate"}

// ValidateRequestContentEncoding checks an action's params.content_encoding.
func ValidateRequestContentEncoding(params map[string]any) error {
	_, err := requestContentEncoding(&protocol.Action{Params: params})
	return err
}

// requestContentEncoding is the action's request body encoding, or "" when
// the body goes as it is.
func requestContentEncoding(action *protocol.Action) (string, error) {
	if action == nil {
		return "", nil
	}
	raw, ok := action.Params[RequestContentEncodingParam]
	if !ok || raw == nil {
		return "", nil
	}
	value, isString := raw.(string)
	encoding := strings.ToLower(strings.TrimSpace(value))
	switch {
	case !isString:
		return "", fmt.Errorf("params.%s must be a string", RequestContentEncodingParam)
	case encoding == "":
		return "", nil
	case encoding == "gzip" || encoding == "deflate":
		return encoding, nil
	}
	return "", fmt.Errorf("params.%s %q is not supported (gzip or deflate)", RequestContentEncodingParam, value)
}

// compressRequestBody encodes a request body for the wire.
func compressRequestBody(body []byte, encoding string) ([]byte, error) {
	switch encoding {
	case "gzip":
		return encodeGzipBody(body)
	case "deflate":
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		if _, err := writer.Write(body); err != nil {
			writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return compressed.Bytes(), nil
	}
	return nil, fmt.Errorf("unsupported request content encoding %q", encoding)
}
