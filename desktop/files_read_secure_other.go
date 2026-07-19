//go:build !(darwin || linux || freebsd || openbsd || netbsd || dragonfly)

package main

import (
	"os"
)

func openBridgeReadFile(path string) (*os.File, error) {
	return os.Open(path)
}
