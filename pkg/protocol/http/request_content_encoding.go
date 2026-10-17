package http

import (
	"bytes"
	"compress/zlib"
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// RequestContentEncodingParam names the action param that compresses the
// request body before it is sent and labels it with Content-Encoding, as
// LoadRunner's ContentEncoding attribute does: gzip, or deflate (the zlib
// format HTTP means by deflate).
const RequestContentEncodingParam = httpcontract.RequestContentEncodingParam

// RequestContentEncodings lists the values params.content_encoding accepts.
var RequestContentEncodings = httpcontract.RequestContentEncodings

// ValidateRequestContentEncoding checks an action's params.content_encoding.
func ValidateRequestContentEncoding(params map[string]any) error {
	return httpcontract.ValidateRequestContentEncoding(params)
}

// requestContentEncoding is the action's request body encoding, or "" when
// the body goes as it is.
func requestContentEncoding(action *protocol.Action) (string, error) {
	return httpcontract.RequestContentEncoding(action)
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
