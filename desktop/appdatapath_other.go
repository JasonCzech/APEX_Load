//go:build !windows

package main

import "os"

func platformDesktopAppDataBases() (local, legacy string, err error) {
	local, err = os.UserConfigDir()
	return local, "", err
}
