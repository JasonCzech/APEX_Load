//go:build !apexload_fault_probe

package api

import "context"

// The release build has no deployed fault-injection pause.
func waitAfterEvacuationGuard(context.Context) {}
