//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func platformDesktopAppDataBases() (local, legacy string, err error) {
	local, err = windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", "", err
	}
	// UserConfigDir is %AppData% (Roaming) on Windows, which is where releases
	// before the LocalAppData fix stored the database and recorder CA.
	legacy, err = os.UserConfigDir()
	return local, legacy, err
}
