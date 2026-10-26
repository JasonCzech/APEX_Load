//go:build windows

package procidentity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// containerTypeKey holds ContainerType inside a Windows container: its
// process IDs and boot belong to the container, so crash reclamation is
// disabled there.
const containerTypeKey = `SYSTEM\CurrentControlSet\Control`

func readHost() (Host, error) {
	if containerized, err := inWindowsContainer(); err != nil || containerized {
		return Host{}, errors.Join(err, errors.New("crash reclamation is disabled inside a Windows container"))
	}
	boot, err := windowsBootID()
	if err != nil {
		return Host{}, err
	}
	guid, guidErr := registryString(`SOFTWARE\Microsoft\Cryptography`, "MachineGuid")
	name, nameErr := physicalDNSHostName()
	return composeWindowsHost(boot, guid, guidErr, name, nameErr)
}

// windowsBootID is the kernel's boot sequence number, which increments
// at every boot.
func windowsBootID() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", fmt.Errorf("boot identity is unavailable: %w", err)
	}
	defer key.Close()
	boot, kind, err := key.GetIntegerValue("BootId")
	if err != nil || kind != registry.DWORD && kind != registry.QWORD {
		return "", errors.Join(fmt.Errorf("boot identity is unavailable"), err)
	}
	return strconv.FormatUint(boot, 10), nil
}

func inWindowsContainer() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, containerTypeKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false, err
	}
	defer key.Close()
	_, _, err = key.GetIntegerValue("ContainerType")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func registryString(path, name string) (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	return strings.TrimSpace(value), err
}

func physicalDNSHostName() (string, error) {
	size := uint32(256)
	for range 2 {
		buffer := make([]uint16, size)
		err := windows.GetComputerNameEx(windows.ComputerNamePhysicalDnsHostname, &buffer[0], &size)
		if err == nil {
			return strings.TrimSpace(windows.UTF16ToString(buffer[:size])), nil
		}
		if !errors.Is(err, windows.ERROR_MORE_DATA) {
			return "", err
		}
	}
	return "", errors.New("host name is unavailable")
}

// processStart reads a process's creation time (100 ns ticks since
// 1601); found is false when no such process exists or it has exited (an
// exited process whose handle another holder keeps open still answers
// OpenProcess).
func processStart(pid int) (string, bool, error) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return "", false, errors.New("invalid process ID")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return "", false, nil
	}
	if err != nil {
		// Access denied (another user's or a protected process) is not
		// proof the process is gone.
		return "", false, err
	}
	defer windows.CloseHandle(handle)
	if event, err := windows.WaitForSingleObject(handle, 0); err != nil {
		return "", false, err
	} else if event == windows.WAIT_OBJECT_0 {
		return "", false, nil
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", false, err
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10), true, nil
}
