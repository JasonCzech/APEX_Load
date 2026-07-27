package archiveingest

import (
	"strings"
	"testing"
)

func TestValidateFilePathUsesArchiveReaderPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr string
	}{
		{name: "relative", path: "collection/request.yml", want: "collection/request.yml"},
		{name: "backslash", path: `collection\request.yml`, want: "collection/request.yml"},
		{name: "traversal component", path: "collection/../request.yml", wantErr: "dot or traversal"},
		{name: "dot component", path: "./request.yml", wantErr: "dot or traversal"},
		{name: "drive qualified", path: "C:/request.yml", wantErr: "drive-qualified"},
		{name: "directory", path: "collection/", wantErr: "directory"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateFilePath(test.path, 8)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ValidateFilePath(%q) error = %v, want containing %q", test.path, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateFilePath(%q): %v", test.path, err)
			}
			if got != test.want {
				t.Fatalf("ValidateFilePath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestValidateFilePathsRejectsNormalizedCollisions(t *testing.T) {
	t.Parallel()

	for _, paths := range [][]string{
		{`svc\service.wsdl`, "svc/service.wsdl"},
		{"svc/service.wsdl", "SVC/SERVICE.WSDL"},
		{"café.wsdl", "cafe\u0301.wsdl"},
	} {
		if _, err := ValidateFilePaths(paths, 8); err == nil {
			t.Fatalf("ValidateFilePaths accepted colliding names %#v", paths)
		}
	}
}
