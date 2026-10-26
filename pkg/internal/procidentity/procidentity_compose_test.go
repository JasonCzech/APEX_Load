package procidentity

import (
	"errors"
	"testing"
)

// A Windows identity needs MachineGuid and the host name: the boot counter
// alone is shared by other machines, so either half missing refuses it.
func TestComposeWindowsHostNeedsBothMachineHalves(t *testing.T) {
	denied := errors.New("access denied")
	host, err := composeWindowsHost("17", "guid-1", nil, "host-a", nil)
	if err != nil || host != (Host{Machine: "guid-1@host-a", Boot: "17", Namespace: "windows"}) {
		t.Fatalf("complete identity: %+v %v", host, err)
	}
	for name, c := range map[string]struct {
		boot, guid, host string
		guidErr, hostErr error
	}{
		"MachineGuid unreadable": {"17", "", "host-a", denied, nil},
		"MachineGuid empty":      {"17", "  ", "host-a", nil, nil},
		"host name unreadable":   {"17", "guid-1", "", nil, denied},
		"host name empty":        {"17", "guid-1", " ", nil, nil},
		"boot empty":             {" ", "guid-1", "host-a", nil, nil},
		// A value read alongside an error is not trusted either.
		"MachineGuid read with an error": {"17", "guid-1", "host-a", denied, nil},
		"host name read with an error":   {"17", "guid-1", "host-a", nil, denied},
	} {
		if host, err := composeWindowsHost(c.boot, c.guid, c.guidErr, c.host, c.hostErr); err == nil || host != (Host{}) {
			t.Errorf("%s: accepted %+v", name, host)
		}
	}
}
