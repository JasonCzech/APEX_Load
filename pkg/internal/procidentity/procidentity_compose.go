package procidentity

import (
	"errors"
	"strings"
)

// composeWindowsHost builds a Windows host identity. MachineGuid alone is
// shared by machines cloned from one image; paired with the physical DNS
// host name, as Linux pairs machine-id with the host name, an earlier boot
// proves a process gone only on the same host. The boot sequence number is a
// small per-machine counter that other hosts share, so without both halves
// of the machine identity no identity exists and nothing is judged.
//
// It is portable so its refusals are tested on every platform.
func composeWindowsHost(boot, guid string, guidErr error, name string, nameErr error) (Host, error) {
	guid, name = strings.TrimSpace(guid), strings.TrimSpace(name)
	if guidErr != nil || guid == "" {
		return Host{}, errors.Join(guidErr, errors.New("machine identity is unavailable"))
	}
	if nameErr != nil || name == "" {
		return Host{}, errors.Join(nameErr, errors.New("host name is unavailable"))
	}
	if strings.TrimSpace(boot) == "" {
		return Host{}, errors.New("boot identity is empty")
	}
	return Host{Machine: guid + "@" + name, Boot: boot, Namespace: "windows"}, nil
}
