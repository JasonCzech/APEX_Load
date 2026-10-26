//go:build darwin

package localfs

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinLocalNeedsALocalAllowlistedMount(t *testing.T) {
	for name, c := range map[string]struct {
		flags    uint32
		typeName string
		local    bool
	}{
		"apfs local":         {unix.MNT_LOCAL, "apfs", true},
		"hfs local":          {unix.MNT_LOCAL, "hfs", true},
		"apfs not local":     {0, "apfs", false},
		"nfs":                {0, "nfs", false},
		"smbfs":              {0, "smbfs", false},
		"afpfs":              {0, "afpfs", false},
		"webdav":             {0, "webdav", false},
		"macfuse flagged":    {unix.MNT_LOCAL, "macfuse", false},
		"osxfuse flagged":    {unix.MNT_LOCAL, "osxfuse", false},
		"unknown local type": {unix.MNT_LOCAL, "exfat", false},
	} {
		if got := localDarwin(c.flags, c.typeName); got != c.local {
			t.Errorf("%s: local=%v, want %v", name, got, c.local)
		}
	}
}
