//go:build darwin

package main

import (
	"strings"

	"golang.org/x/sys/unix"
)

func operatingSystemVersion() string {
	version, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(version)
}
