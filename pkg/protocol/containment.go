package protocol

import "errors"

// ErrContainmentUnproven marks a teardown that cannot prove every child
// process capable of continuing target traffic has terminated. Tooling may
// return this stable sentinel to its caller. A generator's strict teardown
// path must retain its exact execution barrier so the process-level safety
// watchdog, rather than a normal completion callback, terminates the pod.
var ErrContainmentUnproven = errors.New("protocol process containment is unproven")
