package config

import (
	"log/slog"
	"testing"
)

func TestResolveLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		want    slog.Level
		wantErr bool
	}{
		{name: "default", want: slog.LevelInfo},
		{name: "prefixed", values: map[string]string{"APEXLOAD_LOG_LEVEL": "debug"}, want: slog.LevelDebug},
		{name: "legacy", values: map[string]string{"LOG_LEVEL": "warn"}, want: slog.LevelWarn},
		{name: "prefixed wins", values: map[string]string{"APEXLOAD_LOG_LEVEL": "error", "LOG_LEVEL": "debug"}, want: slog.LevelError},
		{name: "invalid", values: map[string]string{"APEXLOAD_LOG_LEVEL": "chatty"}, want: slog.LevelInfo, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.values[key] }
			got, err := ResolveLogLevel(getenv)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("level = %v, want %v", got, tt.want)
			}
		})
	}
}
