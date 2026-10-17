//go:build !windows

package api

import (
	"os"
	"reflect"
	"testing"
)

// harArtifactTestIdentity is the persistent identity a completed HAR's
// retirement record binds: its device, inode and owner UID.
func harArtifactTestIdentity(t *testing.T, root *os.Root, name string) map[string]any {
	t.Helper()
	info, err := root.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	stat := reflect.Indirect(reflect.ValueOf(info.Sys()))
	field := func(name string) uint64 {
		t.Helper()
		if stat.Kind() != reflect.Struct {
			t.Skip("host has no qualified persistent HAR file identity")
		}
		value := stat.FieldByName(name)
		switch value.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			return uint64(value.Int())
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			return value.Uint()
		default:
			t.Skip("host has no qualified persistent HAR file identity")
			return 0
		}
	}
	return map[string]any{"device": field("Dev"), "inode": field("Ino"), "uid": field("Uid")}
}
