package config

import "testing"

func TestResolveControllerBindAddr(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantAddr  string
		wantScope string
	}{
		{name: "unset local", env: map[string]string{}, wantAddr: "127.0.0.1:8080", wantScope: BindScopeLoopback},
		{name: "unset production", env: map[string]string{"APEXLOAD_ENV": "production"}, wantAddr: ":8080", wantScope: BindScopeWide},
		{name: "unset prod alias", env: map[string]string{"APEXLOAD_ENV": "prod"}, wantAddr: ":8080", wantScope: BindScopeWide},
		{name: "explicit loopback", env: map[string]string{"APEXLOAD_ADDR": "127.0.0.1:9090"}, wantAddr: "127.0.0.1:9090", wantScope: BindScopeLoopback},
		{name: "explicit wide", env: map[string]string{"APEXLOAD_ADDR": "0.0.0.0:8080"}, wantAddr: "0.0.0.0:8080", wantScope: BindScopeWide},
		{name: "explicit port only", env: map[string]string{"APEXLOAD_ADDR": ":8080"}, wantAddr: ":8080", wantScope: BindScopeWide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddr, gotScope := ResolveControllerBindAddr(func(key string) string { return tt.env[key] })
			if gotAddr != tt.wantAddr {
				t.Fatalf("addr = %q, want %q", gotAddr, tt.wantAddr)
			}
			if gotScope != tt.wantScope {
				t.Fatalf("scope = %q, want %q", gotScope, tt.wantScope)
			}
		})
	}
}

func TestClassifyBindScope(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{addr: "127.0.0.1:8080", want: BindScopeLoopback},
		{addr: "localhost:8080", want: BindScopeLoopback},
		{addr: "[::1]:8080", want: BindScopeLoopback},
		{addr: "::1", want: BindScopeLoopback},
		{addr: ":8080", want: BindScopeWide},
		{addr: "0.0.0.0:8080", want: BindScopeWide},
		{addr: "[::]:8080", want: BindScopeWide},
		{addr: "192.168.1.12:8080", want: BindScopeWide},
		{addr: "apexload.internal:8080", want: BindScopeWide},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := ClassifyBindScope(tt.addr); got != tt.want {
				t.Fatalf("ClassifyBindScope(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
