//go:build !linux

package javarunner

import "os/exec"

// recordProcessTreeAtRootExit is Linux-only; elsewhere WaitProcessTree keeps
// the platform's plain wait-then-kill order.
func recordProcessTreeAtRootExit(*exec.Cmd) {}
