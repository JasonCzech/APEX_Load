package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvHandlesDegenerateQuotesWithoutPanicking(t *testing.T) {
	tests := []struct {
		name string
		line string
		key  string
		want string
	}{
		{name: "lone double quote", line: `A_LONE_DQUOTE="`, key: "A_LONE_DQUOTE", want: `"`},
		{name: "lone single quote", line: `A_LONE_SQUOTE='`, key: "A_LONE_SQUOTE", want: `'`},
		{name: "empty value", line: `AN_EMPTY=`, key: "AN_EMPTY", want: ``},
		{name: "quoted value", line: `A_QUOTED="value"`, key: "A_QUOTED", want: `value`},
		{name: "empty quotes", line: `AN_EMPTY_QUOTED=""`, key: "AN_EMPTY_QUOTED", want: ``},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			envPath := filepath.Join(dir, ".env")
			if err := os.WriteFile(envPath, []byte(test.line+"\n"), 0o600); err != nil {
				t.Fatalf("WriteFile returned error: %v", err)
			}
			os.Unsetenv(test.key)
			t.Cleanup(func() { os.Unsetenv(test.key) })

			if err := LoadDotEnv(envPath); err != nil {
				t.Fatalf("LoadDotEnv returned error: %v", err)
			}
			if got := os.Getenv(test.key); got != test.want {
				t.Fatalf("%s = %q, want %q", test.key, got, test.want)
			}
		})
	}
}

// A .env file must never override configuration already supplied by the process
// environment, or a file on disk could silently redirect a deployed binary.
func TestLoadDotEnvDoesNotOverrideExistingEnvironment(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("APEXLOAD_DOTENV_PRECEDENCE=from-file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("APEXLOAD_DOTENV_PRECEDENCE", "from-process")

	if err := LoadDotEnv(envPath); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}
	if got := os.Getenv("APEXLOAD_DOTENV_PRECEDENCE"); got != "from-process" {
		t.Fatalf("APEXLOAD_DOTENV_PRECEDENCE = %q, want the process value to win", got)
	}
}
