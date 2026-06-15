package api

import "testing"

// TestDeploymentKindAndLabel locks in the supported UI configurations: a browser
// is only ever connected to a (remote) server, and the only "local" surface is
// the Wails desktop client. A non-desktop controller must never report the
// retired "local-server" kind, regardless of bind scope.
func TestDeploymentKindAndLabel(t *testing.T) {
	cases := []struct {
		name        string
		desktop     bool
		environment string
		wantKind    string
		wantLabel   string
	}{
		{"wails desktop", true, "local", "desktop", "Desktop"},
		{"wails desktop ignores environment", true, "kubernetes", "desktop", "Desktop"},
		{"kubernetes", false, "kubernetes", "kubernetes", "Kubernetes"},
		{"aks alias", false, "aks", "kubernetes", "Kubernetes"},
		{"docker", false, "docker", "docker", "Docker"},
		{"container alias", false, "container", "docker", "Docker"},
		{"local browser is a server (not local-server)", false, "local", "server", "Server"},
		{"dev browser is a server", false, "dev", "server", "Server"},
		{"unknown environment is a server", false, "something-else", "server", "Server"},
		{"empty environment is a server", false, "", "server", "Server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(ServerConfig{DesktopMode: tc.desktop})
			kind, label := srv.deploymentKindAndLabel(tc.environment)
			if kind != tc.wantKind || label != tc.wantLabel {
				t.Fatalf("deploymentKindAndLabel(%q) desktop=%v = (%q, %q), want (%q, %q)", tc.environment, tc.desktop, kind, label, tc.wantKind, tc.wantLabel)
			}
			if kind == "local-server" {
				t.Fatal("kind must never be local-server (browsers are never a local server)")
			}
		})
	}
}
