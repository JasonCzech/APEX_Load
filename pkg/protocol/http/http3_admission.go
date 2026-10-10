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

// http3TransportBuilt reports whether this build contains the exact-3
// transport (internal/h3transport).
var http3TransportBuilt = true

// http3UpgradeBuilt reports whether this build contains the auto+h3 router.
// It stays false until the upgrade ships (Wave 3), so no installation admits
// auto+h3 before then, whatever the flag says.
var http3UpgradeBuilt = false

// HTTP3EnabledByOperator reports whether EnvHTTP3 is set to a true value.
func HTTP3EnabledByOperator() bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnvHTTP3)))
	return err == nil && enabled
}

// HTTP3ExecutionError is nil when this process can execute exact HTTP/3
// (policy 3), and otherwise explains why not, naming the flag. Admission
// uses it for local, embedded, CLI and replay runs, and generators advertise
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

// HTTP3UpgradeExecutionError is nil when this process can execute auto+h3,
// and otherwise explains why not. Generators advertise http3_upgrade_v1 only
// when it is nil.
func HTTP3UpgradeExecutionError() error {
	if err := HTTP3ExecutionError(); err != nil {
		return err
	}
	if !http3UpgradeBuilt {
		return fmt.Errorf("%w: auto+h3 is not available in this build yet; use 3 for exact HTTP/3", ErrHTTP3NotEnabled)
	}
	return nil
}
