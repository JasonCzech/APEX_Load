package httpcontract

import (
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
	_, err := RequestContentEncoding(&protocol.Action{Params: params})
	return err
}

// requestContentEncoding is the action's request body encoding, or "" when
// the body goes as it is.
func RequestContentEncoding(action *protocol.Action) (string, error) {
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
