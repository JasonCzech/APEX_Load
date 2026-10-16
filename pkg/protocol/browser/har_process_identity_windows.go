//go:build windows

package browser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// harContainerTypeKey holds ContainerType inside a Windows container: its
// process IDs and boot belong to the container, so crash reclamation is
// disabled there.
const harContainerTypeKey = `SYSTEM\CurrentControlSet\Control`

func readHARHost() (harHost, error) {
	if containerized, err := harInWindowsContainer(); err != nil || containerized {
		return harHost{}, errors.Join(err, errors.New("crash reclamation is disabled inside a Windows container"))
	}
	boot, err := harWindowsBootID()
	if err != nil {
		return harHost{}, err
	}
	// MachineGuid alone is shared by machines cloned from one image; paired
	// with the physical DNS host name, as Linux pairs machine-id with the
	// host name, an earlier boot proves a process gone only on the same host.
	host := harHost{boot: boot, namespace: "windows"}
	if guid, err := harRegistryString(`SOFTWARE\Microsoft\Cryptography`, "MachineGuid"); err == nil && guid != "" {
		if name, err := harPhysicalDNSHostName(); err == nil && name != "" {
			host.machine = guid + "@" + name
		}
	}
	return host, nil
}

// harWindowsBootID is the kernel's boot sequence number, which increments
// at every boot.
func harWindowsBootID() (string, error) {
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

func harInWindowsContainer() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, harContainerTypeKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
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

func harRegistryString(path, name string) (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	return strings.TrimSpace(value), err
}

func harPhysicalDNSHostName() (string, error) {
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

// harProcessStart reads a process's creation time (100 ns ticks since
// 1601); found is false when no such process exists or it has exited (an
// exited process whose handle another holder keeps open still answers
// OpenProcess).
func harProcessStart(pid int) (string, bool, error) {
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
