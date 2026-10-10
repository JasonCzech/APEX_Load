package http

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvHTTP3 enables HTTP/3 execution on a controller or generator. It is off
// until general availability. Scripts holding HTTP/3 policies validate and
// save either way; the flag only decides whether runs may execute them.
const EnvHTTP3 = "APEXLOAD_HTTP3"

// http3TransportBuilt reports whether this build contains the HTTP/3
// transport. It stays false until exact HTTP/3 ships (Wave 2), so no
// installation can admit HTTP/3 work before then, whatever the flag says.
var http3TransportBuilt = false

// HTTP3EnabledByOperator reports whether EnvHTTP3 is set to a true value.
func HTTP3EnabledByOperator() bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnvHTTP3)))
	return err == nil && enabled
}

// HTTP3ExecutionError is nil when this process can execute HTTP/3 policies,
// and otherwise explains why not, naming the flag. Admission uses it for
// local, embedded, CLI and replay runs, and generators advertise
// native_http3_v1 only when it is nil.
func HTTP3ExecutionError() error {
	if !HTTP3EnabledByOperator() {
		return fmt.Errorf("%w: set %s=true on the controller and every generator to run auto+h3 or 3", ErrHTTP3NotEnabled, EnvHTTP3)
	}
	if !http3TransportBuilt {
		return fmt.Errorf("%w: this build has no HTTP/3 transport yet, so %s has no effect", ErrHTTP3NotEnabled, EnvHTTP3)
	}
	return nil
}
