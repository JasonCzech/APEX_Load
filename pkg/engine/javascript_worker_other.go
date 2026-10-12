//go:build !linux

package engine

import "errors"

func nativeJSContainmentAvailable() bool { return false }
func nativeJSApplyLimits() error {
	return errors.New("native JavaScript hard memory enforcement unavailable")
}
