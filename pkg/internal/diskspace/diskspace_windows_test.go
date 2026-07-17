//go:build windows

package diskspace

import "testing"

func TestWindowsVolumeRoot(t *testing.T) {
	for _, testCase := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "volume path",
			path: `\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\folder\file`,
			want: `\\?\Volume{01234567-89ab-cdef-0123-456789abcdef}\`,
		},
		{
			name: "case insensitive prefix",
			path: `\\?\volume{01234567-89ab-cdef-0123-456789abcdef}\file`,
			want: `\\?\volume{01234567-89ab-cdef-0123-456789abcdef}\`,
		},
		{name: "DOS path", path: `\\?\C:\folder\file`},
		{name: "malformed volume path", path: `\\?\Volume{missing-close\file`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := windowsVolumeRoot(testCase.path); got != testCase.want {
				t.Fatalf("windowsVolumeRoot(%q) = %q, want %q", testCase.path, got, testCase.want)
			}
		})
	}
}
